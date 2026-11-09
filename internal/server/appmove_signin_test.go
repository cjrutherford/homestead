package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/craftfiles"
	"homestead/internal/operation"
	"homestead/internal/sso"
	"homestead/internal/store"
)

// S400: a path-served app whose move changes no environment (a seed file
// or nothing at all) still moves; the stack comes back up.
func TestAppMoveWithoutEnvironmentChanges(t *testing.T) {
	srv, st, steps, _ := moveRig(t)
	moveConfigure = catalog.ConfigureDefinition // the real one refuses an empty change
	installHostApp(t, st, "wordcraft", "wordcraft", 80)
	moveSeeds = func(catalog.Manifest, string) ([]catalog.MoveSeed, error) { return nil, nil }
	plan := movePlan(t, srv, "wordcraft")
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/wordcraft/move/apply", map[string]any{"digest": plan.Digest})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	if r := movedRoute(t, st, "wordcraft.lan"); !r.IsPath() || len(*steps) != 0 {
		t.Fatalf("route = %+v steps = %v", r, *steps)
	}
}

// Seeds and no environment: stopped, written, brought back up, with the
// real configure (which refuses an empty change) never called.
func TestAppMoveSeedsOnlyComesBackUp(t *testing.T) {
	srv, st, steps, _ := moveRig(t)
	moveConfigure = catalog.ConfigureDefinition
	installJellyfin(t, st)
	plan := movePlan(t, srv, "jellyfin")
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/jellyfin/move/apply", map[string]any{"digest": plan.Digest})
	if op := waitOperation(t, st, rec.Body.Bytes()); op.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v", op)
	}
	if got := strings.Join(*steps, ","); got != "stop jellyfin,seed /apps/jellyfin,up jellyfin" {
		t.Fatalf("steps = %s", got)
	}
}

// moveSignInRig is the sign-in rig with the move and craft files mounted,
// Nextcloud installed on its own name with sign-in applied and the craft
// files linked to that name.
func moveSignInRig(t *testing.T) (*ssoApp, *[]string) {
	t.Helper()
	a, _ := publishSignInRig(t)
	steps := &[]string{}
	prevConf, prevStop, prevStart, prevSeeds, prevApply := moveConfigure, moveStop, moveStart, moveSeeds, moveApplySeed
	moveConfigure = func(plan catalog.ConfigPlan, _, project string, _ *store.Store, _ catalog.SecretStore) (operation.Definition, error) {
		return operation.Definition{Type: "catalog.configure", Steps: []operation.StepDef{{Name: "write-config", Run: func(context.Context) error {
			*steps = append(*steps, "configure "+project)
			return nil
		}}}}, nil
	}
	t.Cleanup(func() {
		moveConfigure, moveStop, moveStart, moveSeeds, moveApplySeed = prevConf, prevStop, prevStart, prevSeeds, prevApply
	})
	prevTrust := trustAppHosts
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error { return nil }
	t.Cleanup(func() { trustAppHosts = prevTrust })
	installHostApp(t, a.st, "nextcloud", "nextcloud", 80)
	in, err := a.srv.appSignIn.inputs("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	p, err := appsso.BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "nextcloud", Adapter: p.Adapter,
		ClientID: p.ClientID, Issuer: p.Issuer, RedirectURIs: p.RedirectURIs}); err != nil {
		t.Fatal(err)
	}
	if err := a.srv.files.Manager.Secrets.SetSystem(craftfiles.SecretName, "app-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.DB().Exec(`INSERT INTO craft_file_link (id, base_url, account, root, daemon_created, created_at, updated_at)
		VALUES (1, 'https://nextcloud.lan', 'admin', 'Craft', 1, 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	return a, steps
}

func moveOf(a *ssoApp) *appMove {
	return &appMove{s: a.srv, st: a.st, secrets: a.srv.appSignIn.cfg.Secrets, catalogDir: filepath.Join("..", "..", "catalog")}
}

func moveNextcloud(t *testing.T, a *ssoApp) (appMovePlan, *operationResult) {
	t.Helper()
	plan, err := moveOf(a).buildPlan("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/nextcloud/move/apply", strings.NewReader(`{"digest":"`+plan.Digest+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "nextcloud")
	rec := httptest.NewRecorder()
	moveOf(a).apply(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	return plan, &operationResult{waitOperation(t, a.st, rec.Body.Bytes())}
}

// The move previews the sign-in change and the craft files link, binds
// both into its digest, and applies them after the edge.
func TestAppMoveKeepsSignInAndFilesLinkInStep(t *testing.T) {
	a, _ := moveSignInRig(t)
	plan, res := moveNextcloud(t, a)
	text := strings.Join(plan.SignIn, " ")
	if plan.SignInDigest == "" || !strings.Contains(text, "adds redirect URI https://homestead.lan/apps/nextcloud/") ||
		!strings.Contains(text, "removes redirect URI https://nextcloud.lan/") {
		t.Fatalf("sign-in = %v", plan.SignIn)
	}
	if len(plan.Links) != 1 || plan.Links[0] != "Craft files link moves to https://homestead.lan/apps/nextcloud (same account and credential; no new password)." {
		t.Fatalf("links = %v", plan.Links)
	}
	for name, edit := range map[string]func(*appMovePlan){
		"sign-in": func(p *appMovePlan) { p.SignInDigest = "other" },
		"files":   func(p *appMovePlan) { p.filesTo = "https://elsewhere" },
	} {
		changed := plan
		edit(&changed)
		if changed.digest() == plan.Digest {
			t.Errorf("the digest does not cover the %s change", name)
		}
	}
	if res.Status != operation.StatusSucceeded {
		t.Fatalf("operation: %s %s", res.Status, res.Error)
	}
	rec, _, _ := (appsso.Records{DB: a.st.DB()}).Get("nextcloud")
	if !strings.Contains(strings.Join(rec.RedirectURIs, ","), "https://homestead.lan/apps/nextcloud/") {
		t.Fatalf("record = %+v", rec)
	}
	link, _, _ := a.srv.files.Manager.CurrentLink()
	if link.BaseURL != "https://homestead.lan/apps/nextcloud" || link.Account != "admin" || link.Root != "Craft" {
		t.Fatalf("link = %+v", link)
	}
	if pw, _ := a.srv.files.Manager.Password(); pw != "app-password" {
		t.Fatal("the credential changed")
	}
}

// A sign-in failure fails the operation after the move is done.
func TestAppMoveSignInFailureReported(t *testing.T) {
	a, _ := moveSignInRig(t)
	prev := reapplyAppSignIn
	reapplyAppSignIn = func(context.Context, *appSignIn, cert.IssuerMove, string) error {
		return errors.New("authentik refused")
	}
	t.Cleanup(func() { reapplyAppSignIn = prev })
	_, res := moveNextcloud(t, a)
	if res.Status != operation.StatusFailed || !strings.Contains(res.Error, "nextcloud moved, but its sign-in still uses the old address: authentik refused") {
		t.Fatalf("op = %s %q", res.Status, res.Error)
	}
	if r := movedRoute(t, a.st, "nextcloud.lan"); !r.IsPath() {
		t.Fatalf("not moved: %+v", r)
	}
}

// Without the kept token the plan leaves sign-in to a re-plan, and a
// link pointing elsewhere is left alone.
func TestAppMoveWithoutTokenLeavesSignInAndOtherLinks(t *testing.T) {
	a, _ := moveSignInRig(t)
	if _, err := a.st.DB().Exec(`UPDATE craft_file_link SET base_url = 'https://files.example.org'`); err != nil {
		t.Fatal(err)
	}
	a.srv.appSignIn.cfg.Secrets.Delete(sso.APITokenName)
	plan, err := moveOf(a).buildPlan("nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SignInDigest != "" || len(plan.Links) != 0 || !strings.Contains(strings.Join(plan.SignIn, " "), "re-plan it on the app page") {
		t.Fatalf("plan = %+v", plan)
	}
}

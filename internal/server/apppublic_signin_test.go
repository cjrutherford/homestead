package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/cert"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/sso"
)

// publishSignInRig is the app sign-in rig (Authentik reachable, token
// kept) with the public tunnel rig on the same server: the dashboard and
// Authentik's path are published and answered on 443, and Immich's
// sign-in was applied at the local issuer.
func publishSignInRig(t *testing.T) (*ssoApp, *fakeCloudflare) {
	t.Helper()
	a := newSSOApp(t)
	a.configureKeeping(t, true)
	secrets := a.srv.appSignIn.cfg.Secrets
	a.srv.appSignIn.cfg.CatalogDir = filepath.Join("..", "..", "catalog")
	// Authentik and the app's compose stack are not on a test box: the
	// sign-in apply is the real plan, checked against the approved digest
	// as the real path does, with its record saved.
	prev := reapplyAppSignIn
	reapplyAppSignIn = func(_ context.Context, sign *appSignIn, move cert.IssuerMove, _ string) error {
		in, err := sign.inputs(move.App)
		if err != nil {
			return err
		}
		plan, err := appsso.BuildPlan(in, true)
		if err != nil {
			return err
		}
		if plan.Digest != move.Digest {
			return errPlanChanged
		}
		return (appsso.Records{DB: sign.cfg.Store.DB()}).Save(appsso.Record{Package: move.App, Adapter: plan.Adapter,
			ClientID: plan.ClientID, Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs})
	}
	t.Cleanup(func() { reapplyAppSignIn = prev })
	tunnelConnectorDir = t.TempDir()
	t.Cleanup(func() { tunnelConnectorDir = "/etc/homestead/tunnel" })
	a.srv.appPublic.catalogDir = filepath.Join("..", "..", "catalog")
	_, _, cf, _ := appPublicRigOn(t, a.srv, a.st, secrets)
	set := docker.RouteSet{Routes: []docker.Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
			Tunnel: []docker.Hostname{"homestead.example.com"}},
		{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000}, Exposure: docker.ExposureLocal,
			TLS: docker.TLSInternal, Path: "/apps/auth/", Published: true},
		{Host: "immich.lan", Target: docker.Target{Host: "server", Port: 2283}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
	}}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	prevServed := signInSiteServed
	signInSiteServed = func(docker.RouteSet) (docker.Hostname, bool) { return "homestead.example.com", true }
	t.Cleanup(func() { signInSiteServed = prevServed })
	local := a.w.srv.URL + "/application/o/homestead-immich/"
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "immich", Adapter: "immich",
		ClientID: "homestead-immich", Issuer: local, RedirectURIs: []string{"https://immich.lan/auth/login"}}); err != nil {
		t.Fatal(err)
	}
	return a, cf
}

func runAppPublic(t *testing.T, a *ssoApp, req appPublicRequest) (appPublicPlan, *operationResult, error) {
	t.Helper()
	plan, err := a.srv.appPublic.buildPlan(context.Background(), "immich", req)
	if err != nil {
		t.Fatal(err)
	}
	req.Digest = plan.Digest
	id, err := a.srv.appPublic.start(context.Background(), "immich", req, "owner")
	if err != nil {
		return plan, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	op, err := operation.NewRunner(a.st).Wait(ctx, id)
	if err != nil {
		got, _ := operation.NewRunner(a.st).Wait(context.Background(), -1)
		_ = got
		rows, _ := a.st.DB().Query(`SELECT name, status, COALESCE(error,'') FROM operation_steps`)
		for rows != nil && rows.Next() {
			var n, st, e string
			_ = rows.Scan(&n, &st, &e)
			t.Logf("step %s %s %s", n, st, e)
		}
		t.Fatal(err)
	}
	return plan, &operationResult{op}, nil
}

type operationResult struct{ operation.Operation }

// S399: publishing an app with sign-in on moves its sign-in to the
// public issuer in the same operation, and unpublishing moves it back.
func TestAppPublicKeepsSignInInStep(t *testing.T) {
	a, _ := publishSignInRig(t)
	on, off := true, false
	add := appPublicRequest{Enabled: &on, Route: "immich.lan", Hostname: "immich.example.com"}
	plan, res, err := runAppPublic(t, a, add)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SignInDigest == "" || !strings.Contains(plan.SignIn, "https://homestead.example.com/apps/auth/application/o/homestead-immich/") ||
		!strings.Contains(plan.SignIn, "https://immich.example.com/auth/login") {
		t.Fatalf("plan sign-in = %q", plan.SignIn)
	}
	// The sign-in change is part of the digest.
	covered := plan
	covered.SignInDigest = "other"
	if covered.digest() == plan.Digest {
		t.Fatal("the digest does not cover the sign-in change")
	}
	if res.Status != operation.StatusSucceeded {
		t.Fatalf("publish: %s %s", res.Status, res.Error)
	}
	rec, ok, _ := (appsso.Records{DB: a.st.DB()}).Get("immich")
	if !ok || !strings.HasPrefix(rec.Issuer, "https://homestead.example.com/apps/auth/") ||
		!strings.Contains(strings.Join(rec.RedirectURIs, ","), "https://immich.example.com/auth/login") {
		t.Fatalf("record after publish = %+v", rec)
	}

	remove := appPublicRequest{Enabled: &off, Route: "immich.lan", Hostname: "immich.example.com"}
	plan, res, err = runAppPublic(t, a, remove)
	if err != nil || plan.SignInDigest == "" || !strings.Contains(plan.SignIn, "local issuer") {
		t.Fatalf("unpublish plan = %v %q", err, plan.SignIn)
	}
	if res.Status != operation.StatusSucceeded {
		t.Fatalf("unpublish: %s %s", res.Status, res.Error)
	}
	rec, _, _ = (appsso.Records{DB: a.st.DB()}).Get("immich")
	if rec.Issuer != a.w.srv.URL+"/application/o/homestead-immich/" || strings.Contains(strings.Join(rec.RedirectURIs, ","), "immich.example.com") {
		t.Fatalf("record after unpublish = %+v", rec)
	}
}

// An app without sign-in is published as before: no sign-in step.
func TestAppPublicWithoutSignInUnchanged(t *testing.T) {
	a, _ := publishSignInRig(t)
	if _, err := a.st.DB().Exec(`DELETE FROM app_sign_in`); err != nil {
		t.Fatal(err)
	}
	on := true
	plan, res, err := runAppPublic(t, a, appPublicRequest{Enabled: &on, Route: "immich.lan", Hostname: "immich.example.com"})
	if err != nil || plan.SignInDigest != "" || plan.SignIn != "" {
		t.Fatalf("plan = %v %+v", err, plan)
	}
	if res.Status != operation.StatusSucceeded {
		t.Fatalf("publish: %s %s", res.Status, res.Error)
	}
	var n int
	_ = a.st.DB().QueryRow(`SELECT COUNT(*) FROM app_sign_in`).Scan(&n)
	if n != 0 {
		t.Fatal("a sign-in record appeared")
	}
}

// A sign-in step failure fails the operation after the name is live and
// says the old issuer is still in use.
func TestAppPublicSignInFailureReported(t *testing.T) {
	a, _ := publishSignInRig(t)
	prev := reapplyAppSignIn
	reapplyAppSignIn = func(context.Context, *appSignIn, cert.IssuerMove, string) error {
		return errors.New("authentik refused")
	}
	t.Cleanup(func() { reapplyAppSignIn = prev })
	on := true
	_, res, err := runAppPublic(t, a, appPublicRequest{Enabled: &on, Route: "immich.lan", Hostname: "immich.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != operation.StatusFailed || !strings.Contains(res.Error, "immich is public, but its sign-in still uses the old issuer: authentik refused") {
		t.Fatalf("op = %s %q", res.Status, res.Error)
	}
	set, _ := loadEdgeRoutes(a.st)
	var named bool
	for _, r := range set.Routes {
		if r.Host == "immich.lan" && len(r.Tunnel) == 1 {
			named = true
		}
	}
	if !named {
		t.Fatalf("the public name is not live: %+v", set.Routes)
	}
}

// installPathNextcloud puts Nextcloud at its path with sign-in applied at
// the plan the edge table gives now.
func installPathNextcloud(t *testing.T, a *ssoApp) {
	t.Helper()
	installApp(t, a.st, "nextcloud", pathRoute("nextcloud", "nextcloud", 80, false))
	saveNextcloudSignIn(t, a)
}

func saveNextcloudSignIn(t *testing.T, a *ssoApp) {
	t.Helper()
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
}

func setNextcloudPublished(t *testing.T, a *ssoApp, published bool) {
	t.Helper()
	set, _ := loadEdgeRoutes(a.st)
	for i := range set.Routes {
		if set.Routes[i].Host == "nextcloud.lan" {
			set.Routes[i].Published = published
		}
	}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
}

// S400: publishing a path-served app (the Published toggle) previews and
// applies its sign-in change like a name does.
func TestAppPublicPathPublishKeepsSignInInStep(t *testing.T) {
	a, _ := publishSignInRig(t)
	installPathNextcloud(t, a)
	prevTrust := trustAppHosts
	trustAppHosts = func(context.Context, catalog.Manifest, string, []string, []string) error { return nil }
	t.Cleanup(func() { trustAppHosts = prevTrust })
	on := true
	req := appPublicRequest{Enabled: &on}
	plan, err := a.srv.appPublic.buildPlan(context.Background(), "nextcloud", req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SignInDigest == "" || !strings.Contains(plan.SignIn, "https://homestead.example.com/apps/nextcloud/") {
		t.Fatalf("plan sign-in = %q", plan.SignIn)
	}
	req.Digest = plan.Digest
	id, err := a.srv.appPublic.start(context.Background(), "nextcloud", req, "owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	op, err := operation.NewRunner(a.st).Wait(ctx, id)
	if err != nil || op.Status != operation.StatusSucceeded {
		t.Fatalf("publish: %v %+v", err, op)
	}
	rec, _, _ := (appsso.Records{DB: a.st.DB()}).Get("nextcloud")
	if !strings.Contains(strings.Join(rec.RedirectURIs, ","), "https://homestead.example.com/apps/nextcloud/") {
		t.Fatalf("record = %+v", rec)
	}
}

// Nothing changing in sign-in is not a reason to ask for a re-plan.
func TestAppPublicSignInNoChangeDoesNotAskForReplan(t *testing.T) {
	a, _ := publishSignInRig(t)
	installPathNextcloud(t, a)
	setNextcloudPublished(t, a, true)
	saveNextcloudSignIn(t, a)
	setNextcloudPublished(t, a, false)
	on := true
	plan, err := a.srv.appPublic.buildPlan(context.Background(), "nextcloud", appPublicRequest{Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SignInDigest != "" || strings.Contains(plan.SignIn, "re-plan") || !strings.Contains(plan.SignIn, "needs no change") {
		t.Fatalf("sign-in = %q (%q)", plan.SignIn, plan.SignInDigest)
	}
}

// Without the kept Authentik token the plan says to re-plan sign-in, and
// the operation has no sign-in step.
func TestAppPublicTokenNotKeptLeavesSignInToReplan(t *testing.T) {
	a, _ := publishSignInRig(t)
	if err := a.srv.appSignIn.cfg.Secrets.Delete(sso.APITokenName); err != nil {
		t.Fatal(err)
	}
	on := true
	plan, res, err := runAppPublic(t, a, appPublicRequest{Enabled: &on, Route: "immich.lan", Hostname: "immich.example.com"})
	if err != nil || plan.SignInDigest != "" || !strings.Contains(plan.SignIn, "re-plan it on this page") {
		t.Fatalf("plan = %v %q", err, plan.SignIn)
	}
	if res.Status != operation.StatusSucceeded {
		t.Fatalf("publish: %s %s", res.Status, res.Error)
	}
	rec, _, _ := (appsso.Records{DB: a.st.DB()}).Get("immich")
	if strings.Contains(strings.Join(rec.RedirectURIs, ","), "immich.example.com") {
		t.Fatalf("sign-in changed without a token: %+v", rec)
	}
}

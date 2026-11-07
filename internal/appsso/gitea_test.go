package appsso

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// giteaSignInVerified is the Gitea version the sign-in script in
// catalog/gitea.yaml was verified against (S416): the login_source columns,
// type 6 (OAuth2), the oauth2.Source JSON, `gitea --version` and the
// startup-only registration of OAuth providers.
const giteaSignInVerified = "28.2.0"

func realGitea(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("gitea")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("gitea manifest: %v", err)
	}
	return m
}

// TestGiteaSignInPinnedToVerifiedVersion: the sign-in script writes Gitea's
// own settings database, an internal schema that can change in any
// release. Bumping the package is not enough; the script must be
// re-verified first.
func TestGiteaSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realGitea(t)
	image := m.Services[0].Image
	if m.Version != giteaSignInVerified || !strings.HasSuffix(image, ":"+giteaSignInVerified) {
		t.Fatalf("gitea is %s (%s) but its Authentik sign-in script (the inline onapply/onoff script in catalog/gitea.yaml) "+
			"was verified against %s. Before bumping: re-verify the login_source table and columns in /data/gitea/gitea.db, "+
			"the oauth2.Source JSON fields (models/auth, services/auth/source/oauth2), `gitea --version` and when Gitea registers OAuth providers, "+
			"update the script's VERIFIED version, then set giteaSignInVerified here (ADR-052, S416).", m.Version, image, giteaSignInVerified)
	}
	script := m.SSO.OIDC.Env.OnApply[2]
	if !strings.Contains(script, `VERIFIED=`+giteaSignInVerified) {
		t.Fatalf("the script's VERIFIED constant is not %s", giteaSignInVerified)
	}
}

func TestGiteaSchemaValidation(t *testing.T) {
	cases := map[string]func(e *catalog.EnvOIDC){
		"restart needs onapply": func(e *catalog.EnvOIDC) { e.OnApply, e.OnOff = nil, nil },
		"static text":           func(e *catalog.EnvOIDC) { e.OnApply = []string{"sh", "{{client_id}}"} },
		"empty":                 func(e *catalog.EnvOIDC) { e.OnOff = []string{"sh", ""} },
		"execuser":              func(e *catalog.EnvOIDC) { e.ExecUser = "a b" },
		"must use {{client_secret}}": func(e *catalog.EnvOIDC) {
			e.SecretFile = &catalog.EnvSecretFile{Secret: "gitea_signin", Template: `{"a":"{{client_id}}"}`}
		},
	}
	for want, mutate := range cases {
		m := realGitea(t)
		e := *m.SSO.OIDC.Env
		mutate(&e)
		m.SSO.OIDC.Env = &e
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	m := realGitea(t)
	e := *m.SSO.OIDC.Env
	e.Restart, e.OnApply, e.OnOff = true, nil, nil
	m.SSO.OIDC.Env = &e
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "restart needs onapply") {
		t.Errorf("restart without commands: %v", err)
	}
}

func TestGiteaDerivedSecretIsEscapedJSON(t *testing.T) {
	a := envOIDC{cfg: *realGitea(t).SSO.OIDC.Env}
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-gitea/",
		ClientID: `id"x`, ClientSecret: `s\"e`, Groups: GroupNames{Admins: "homestead-gitea-admins"}})
	if !ok || name != "gitea_signin" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	if got["client_id"] != `id"x` || got["client_secret"] != `s\"e` || got["admin_group"] != "homestead-gitea-admins" ||
		got["metadata_url"] != "https://homestead.lan/apps/auth/application/o/homestead-gitea/.well-known/openid-configuration" {
		t.Fatalf("values = %v", got)
	}
	// The secret reaches neither the environment nor the redirect host.
	for k, v := range a.Env(Settings{ClientSecret: "zzz-secret"}) {
		if strings.Contains(v, "zzz-secret") {
			t.Fatalf("secret in %s", k)
		}
	}
}

func TestGiteaPlanRedirectWarningsAndScript(t *testing.T) {
	m := realGitea(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("gitea.lan", "gitea")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RedirectURIs[0] != "https://gitea.lan/user/oauth2/authentik/callback" || plan.SignInURL != "https://gitea.lan/user/oauth2/authentik" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"own database", "re-checked on every Gitea update", "Run this after the app has started once",
		"only at the address", "ROOT_URL", "local password", "no silent takeover", "last administrator", "local registration is closed", "plaintext", "gitea.db"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	settings := strings.Join(plan.AppSettings, "\n")
	if !strings.Contains(settings, "sh -c <") || !strings.Contains(settings, "-line script> sh on") ||
		!strings.Contains(settings, "restarted afterwards") || strings.Contains(settings, "BEGIN IMMEDIATE") {
		t.Fatalf("settings:\n%s", settings)
	}
	if !strings.Contains(strings.Join(plan.Changes, "\n"), "local admin keeps working") {
		t.Fatalf("a non-exclusive app keeps its local login in the plan:\n%s", strings.Join(plan.Changes, "\n"))
	}
	off, err := BuildPlan(Inputs{Manifest: m, Installed: true, Applied: &Record{Package: "gitea", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(off.AppSettings, "\n"), "-line script> sh off") {
		t.Fatalf("off plan:\n%s", strings.Join(off.AppSettings, "\n"))
	}
}

func TestGiteaTrustFollowsIssuer(t *testing.T) {
	a := envOIDC{cfg: *realGitea(t).SSO.OIDC.Env}
	if got := a.TrustEnvFor("https://homestead.lan/apps/auth/application/o/homestead-gitea/"); got["SSL_CERT_FILE"] != BundleFile {
		t.Fatalf("private issuer trust = %v", got)
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/homestead-gitea/"); got != nil {
		t.Fatalf("public issuer trust = %v", got)
	}
}

// TestGiteaCommandsRunAsAppUserThenRestart: the script runs as git with the
// mode as its last argument, nothing secret travels in argv or stdin, and
// the container restarts only after the script succeeded.
func TestGiteaCommandsRunAsAppUserThenRestart(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realGitea(t).SSO.OIDC.Env}
	tgt := Target{Container: "gitea-gitea-1"}
	if err := a.Enable(context.Background(), tgt, Settings{ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	if err := a.Disable(context.Background(), tgt); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || len(f.restarts) != 2 || f.restarts[0] != tgt.Container {
		t.Fatalf("calls %d restarts %v", len(f.calls), f.restarts)
	}
	for i, mode := range []string{"on", "off"} {
		c := f.calls[i]
		n := len(c.args)
		if c.container != tgt.Container || c.user != "git" || c.stdin != "" || c.args[0] != "sh" || c.args[1] != "-c" || c.args[n-2] != "sh" || c.args[n-1] != mode {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	// A failing script restarts nothing and says what failed.
	f.restarts = nil
	f.fail = func(call) (string, error) { return "", errors.New("table login_source lacks expected columns") }
	err := a.Enable(context.Background(), tgt, Settings{})
	if err == nil || len(f.restarts) != 0 || !strings.Contains(err.Error(), "lacks expected columns") || strings.Contains(err.Error(), "BEGIN IMMEDIATE") {
		t.Fatalf("err = %v restarts %v", err, f.restarts)
	}
	// A failed restart is reported.
	f.fail = nil
	old := restart
	restart = func(context.Context, string) error { return errors.New("no daemon") }
	defer func() { restart = old }()
	if err := a.Enable(context.Background(), tgt, Settings{}); err == nil || !strings.Contains(err.Error(), "restarting") {
		t.Fatalf("restart error = %v", err)
	}
	// An app without the field is not restarted (every other env-oidc app).
	restart = old
	f.restarts = nil
	other := envOIDC{cfg: *realFreshRSS(t).SSO.OIDC.Env}
	if err := other.Enable(context.Background(), tgt, Settings{}); err != nil || len(f.restarts) != 0 {
		t.Fatalf("freshrss restarted: %v %v", err, f.restarts)
	}
}

// TestGiteaApplyKeepsSecretInTheFile applies and turns sign-in off on a
// fresh install: the client secret lives only in the 0600 file and the
// secret store, the address is in the environment, and the app restarts.
func TestGiteaApplyKeepsSecretInTheFile(t *testing.T) {
	f := stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, "-----BEGIN CERTIFICATE-----\nsystem\n-----END CERTIFICATE-----\n")
	m := realGitea(t)
	dir := t.TempDir()
	files, err := catalog.Generate(m, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "gitea"}},
		RouteHosts: map[string]string{"gitea": "gitea.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	if err := catalog.EnsureSecrets(app, m); err != nil {
		t.Fatal(err)
	}
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("gitea.lan", "gitea")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	run := func(p Plan) {
		def, err := Definition(rt, p, m, dashboard, &fakeAuthentik{}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range def.Steps {
			if err := step.Run(context.Background()); err != nil {
				t.Fatalf("%s: %v", step.Name, err)
			}
		}
	}
	store := catalog.SecretStoreName("gitea", "gitea_signin")
	file := filepath.Join(dir, catalog.SecretsDirName, "gitea_gitea_signin")
	run(plan)
	secret := app.m[catalog.SecretStoreName("gitea", "oidc_client_secret")]
	if secret == "" || !strings.Contains(app.m[store], secret) {
		t.Fatalf("client secret %q not rendered into %q", secret, app.m[store])
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != app.m[store] {
		t.Fatalf("secret file = %q, %v", body, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"GITEA_SIGNIN_FILE: /run/secrets/gitea_gitea_signin", "GITEA__server__ROOT_URL: https://gitea.lan/"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	if !strings.Contains(string(override), "SSL_CERT_FILE: /etc/homestead/ca/bundle.pem") || !strings.Contains(string(override), "GITEA__oauth2_client__ACCOUNT_LINKING: login") {
		t.Fatalf("override:\n%s", override)
	}
	if strings.Contains(string(compose), "entrypoint") {
		t.Fatalf("an unexported file secret changed the entrypoint:\n%s", compose)
	}
	for name, b := range map[string][]byte{"compose": compose, "override": override} {
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "client_secret") {
			t.Fatalf("secret in %s", name)
		}
	}
	if len(f.calls) != 1 || len(f.restarts) != 1 || f.argvHolds(secret) {
		t.Fatalf("calls %d restarts %v", len(f.calls), f.restarts)
	}
	rec, _, _ := Records{DB: st.DB()}.Get("gitea")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	run(off)
	body, _ = os.ReadFile(file)
	if string(body) != "{}" || app.m[store] != "{}" {
		t.Fatalf("off leaves the secret file %q store %q", body, app.m[store])
	}
	if len(f.calls) != 2 || f.calls[1].args[len(f.calls[1].args)-1] != "off" || len(f.restarts) != 2 {
		t.Fatalf("off: calls %d restarts %v", len(f.calls), f.restarts)
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "GITEA_OIDC_ISSUER") || strings.Contains(string(override), "SSL_CERT_FILE") || strings.Contains(string(override), "ENABLE_AUTO_REGISTRATION") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

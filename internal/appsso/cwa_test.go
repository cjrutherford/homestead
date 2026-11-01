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

// cwaSignInVerified is the Calibre-Web Automated version the sign-in
// script in catalog/calibre-web-automated.yaml was verified against
// (S415): its oauthProvider and settings columns, /app/CWA_RELEASE and
// the startup-only read of the sign-in rows.
const cwaSignInVerified = "4.0.8"

func realCWA(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("calibre-web-automated")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("calibre-web-automated manifest: %v", err)
	}
	return m
}

// TestCWASignInPinnedToVerifiedVersion: the sign-in script writes CWA's
// own settings database, an internal schema that can change in any
// release. Bumping the package is not enough; the script must be
// re-verified first.
func TestCWASignInPinnedToVerifiedVersion(t *testing.T) {
	m := realCWA(t)
	image := m.Services[0].Image
	if m.Version != cwaSignInVerified || !strings.HasSuffix(image, ":v"+cwaSignInVerified) {
		t.Fatalf("calibre-web-automated is %s (%s) but its Authentik sign-in script (the inline onapply/onoff script in catalog/calibre-web-automated.yaml) "+
			"was verified against %s. Before bumping: re-verify the oauthProvider and settings tables and columns in /config/app.db, "+
			"/app/CWA_RELEASE and when CWA reads the sign-in rows (cps/oauth_bb.py), update the script's VERIFIED version, "+
			"then set cwaSignInVerified here (ADR-052, S415).", m.Version, image, cwaSignInVerified)
	}
	script := m.SSO.OIDC.Env.OnApply[2]
	if !strings.Contains(script, `VERIFIED = "`+cwaSignInVerified+`"`) {
		t.Fatalf("the script's VERIFIED constant is not %s", cwaSignInVerified)
	}
}

func TestCWASchemaValidation(t *testing.T) {
	cases := map[string]func(e *catalog.EnvOIDC){
		"restart needs onapply": func(e *catalog.EnvOIDC) { e.OnApply, e.OnOff = nil, nil },
		"static text":           func(e *catalog.EnvOIDC) { e.OnApply = []string{"python", "{{client_id}}"} },
		"empty":                 func(e *catalog.EnvOIDC) { e.OnOff = []string{"python", ""} },
		"execuser":              func(e *catalog.EnvOIDC) { e.ExecUser = "a b" },
		"must use {{client_secret}}": func(e *catalog.EnvOIDC) {
			e.SecretFile = &catalog.EnvSecretFile{Secret: "cwa_signin", Template: `{"a":"{{client_id}}"}`}
		},
	}
	for want, mutate := range cases {
		m := realCWA(t)
		e := *m.SSO.OIDC.Env
		mutate(&e)
		m.SSO.OIDC.Env = &e
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	m := realCWA(t)
	e := *m.SSO.OIDC.Env
	e.Restart, e.OnApply, e.OnOff = true, nil, nil
	m.SSO.OIDC.Env = &e
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "restart needs onapply") {
		t.Errorf("restart without commands: %v", err)
	}
}

func TestCWADerivedSecretIsEscapedJSON(t *testing.T) {
	a := envOIDC{cfg: *realCWA(t).SSO.OIDC.Env}
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-calibre-web-automated/",
		ClientID: `id"x`, ClientSecret: `s\"e`, Groups: GroupNames{Admins: "homestead-calibre-web-automated-admins"}})
	if !ok || name != "cwa_signin" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	if got["client_id"] != `id"x` || got["client_secret"] != `s\"e` || got["admin_group"] != "homestead-calibre-web-automated-admins" ||
		got["metadata_url"] != "https://homestead.lan/apps/auth/application/o/homestead-calibre-web-automated/.well-known/openid-configuration" {
		t.Fatalf("values = %v", got)
	}
	// The secret reaches neither the environment nor the redirect host.
	for k, v := range a.Env(Settings{ClientSecret: "zzz-secret"}) {
		if strings.Contains(v, "zzz-secret") {
			t.Fatalf("secret in %s", k)
		}
	}
}

func TestCWAPlanRedirectWarningsAndScript(t *testing.T) {
	m := realCWA(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("calibre-web-automated.lan", "calibre-web-automated")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RedirectURIs[0] != "https://calibre-web-automated.lan/login/generic/authorized" || plan.SignInURL != "https://calibre-web-automated.lan/login/generic" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"own database", "re-checked on every Calibre-Web Automated update", "Run this after the app has started once",
		"only at the address", "takes over that account", "default admin", "groups claim is empty", "plaintext", "app.db", "local login form stays"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	settings := strings.Join(plan.AppSettings, "\n")
	if !strings.Contains(settings, "/lsiopy/bin/python -c <") || !strings.Contains(settings, "-line script> on") ||
		!strings.Contains(settings, "restarted afterwards") || strings.Contains(settings, "sqlite3.connect") {
		t.Fatalf("settings:\n%s", settings)
	}
	if !strings.Contains(strings.Join(plan.Changes, "\n"), "local admin keeps working") {
		t.Fatalf("a non-exclusive app keeps its local login in the plan:\n%s", strings.Join(plan.Changes, "\n"))
	}
	off, err := BuildPlan(Inputs{Manifest: m, Installed: true, Applied: &Record{Package: "calibre-web-automated", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(off.AppSettings, "\n"), "-line script> off") {
		t.Fatalf("off plan:\n%s", strings.Join(off.AppSettings, "\n"))
	}
}

func TestCWATrustFollowsIssuer(t *testing.T) {
	a := envOIDC{cfg: *realCWA(t).SSO.OIDC.Env}
	if got := a.TrustEnvFor("https://homestead.lan/apps/auth/application/o/homestead-calibre-web-automated/"); got["REQUESTS_CA_BUNDLE"] != BundleFile {
		t.Fatalf("private issuer trust = %v", got)
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/homestead-calibre-web-automated/"); got != nil {
		t.Fatalf("public issuer trust = %v", got)
	}
}

// TestCWACommandsRunAsAppUserThenRestart: the script runs as abc with the
// mode as its last argument, nothing secret travels in argv or stdin, and
// the container restarts only after the script succeeded.
func TestCWACommandsRunAsAppUserThenRestart(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realCWA(t).SSO.OIDC.Env}
	tgt := Target{Container: "cwa-calibre-web-automated-1"}
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
		if c.container != tgt.Container || c.user != "abc" || c.stdin != "" || c.args[0] != "/lsiopy/bin/python" || c.args[1] != "-c" || c.args[n-1] != mode {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	// A failing script restarts nothing and says what failed.
	f.restarts = nil
	f.fail = func(call) (string, error) { return "", errors.New("table oauthProvider lacks column(s) x") }
	err := a.Enable(context.Background(), tgt, Settings{})
	if err == nil || len(f.restarts) != 0 || !strings.Contains(err.Error(), "lacks column") || strings.Contains(err.Error(), "sqlite3.connect") {
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

// TestCWAApplyKeepsSecretInTheFile applies and turns sign-in off on a
// fresh install: the client secret lives only in the 0600 file and the
// secret store, the address is in the environment, and the app restarts.
func TestCWAApplyKeepsSecretInTheFile(t *testing.T) {
	f := stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, "-----BEGIN CERTIFICATE-----\nsystem\n-----END CERTIFICATE-----\n")
	m := realCWA(t)
	dir := t.TempDir()
	files, err := catalog.Generate(m, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "calibre-web-automated"}},
		RouteHosts: map[string]string{"calibre-web-automated": "calibre-web-automated.lan"}})
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
		Routes: AppRoutes(m, []docker.Route{route("calibre-web-automated.lan", "calibre-web-automated")})}
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
	store := catalog.SecretStoreName("calibre-web-automated", "cwa_signin")
	file := filepath.Join(dir, catalog.SecretsDirName, "calibre_web_automated_cwa_signin")
	run(plan)
	secret := app.m[catalog.SecretStoreName("calibre-web-automated", "oidc_client_secret")]
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
	for _, w := range []string{"CWA_SIGNIN_FILE: /run/secrets/calibre_web_automated_cwa_signin", "HOMESTEAD_CWA_URL: https://calibre-web-automated.lan"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	if !strings.Contains(string(override), "REQUESTS_CA_BUNDLE: /etc/homestead/ca/bundle.pem") {
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
	rec, _, _ := Records{DB: st.DB()}.Get("calibre-web-automated")
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
	if strings.Contains(string(override), "CWA_OIDC_ISSUER") || strings.Contains(string(override), "REQUESTS_CA_BUNDLE") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

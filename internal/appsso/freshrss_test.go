package appsso

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

func realFreshRSS(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("freshrss")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("freshrss manifest: %v", err)
	}
	return m
}

func TestFreshRSSSchemaValidation(t *testing.T) {
	cases := map[string]func(e *catalog.EnvOIDC){
		"onapply and onoff come together": func(e *catalog.EnvOIDC) { e.OnOff = nil },
		"static text":                     func(e *catalog.EnvOIDC) { e.OnApply = []string{"php", "{{client_id}}"} },
		"empty":                           func(e *catalog.EnvOIDC) { e.OnOff = []string{"php", ""} },
		"absolute trustpath":              func(e *catalog.EnvOIDC) { e.TrustPath = "etc/ssl" },
		"no trust variable":               func(e *catalog.EnvOIDC) { e.Trust = "SSL_CERT_FILE" },
		"needs trustbundle":               func(e *catalog.EnvOIDC) { e.TrustBundle, e.TrustPath = "", "/etc/ssl/x.crt" },
		"extra secret":                    func(e *catalog.EnvOIDC) { e.ExtraSecrets = []string{"nope"} },
		"execuser":                        func(e *catalog.EnvOIDC) { e.ExecUser = "www data" },
	}
	for want, mutate := range cases {
		m := realFreshRSS(t)
		e := *m.SSO.OIDC.Env
		mutate(&e)
		m.SSO.OIDC.Env = &e
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestFreshRSSTrustFollowsIssuer(t *testing.T) {
	m := realFreshRSS(t)
	a := envOIDC{cfg: *m.SSO.OIDC.Env}
	want := "./" + CADir + "/bundle.pem:/etc/ssl/certs/ca-certificates.crt:ro"
	if got := a.TrustMount("https://homestead.lan/apps/auth/application/o/homestead-freshrss/"); got != want {
		t.Fatalf("private mount = %q", got)
	}
	if a.TrustMount("https://auth.example.com/application/o/homestead-freshrss/") != "" || a.TrustMountAny() != want {
		t.Fatal("a public issuer wants no mount, and the stale one is still known")
	}
	if a.TrustEnv() != nil || a.LegacyTrustEnv() != nil || !a.CombinedBundleFor("https://homestead.lan/x/") {
		t.Fatal("system mode sets no variable but wants the bundle for a private issuer")
	}
}

func TestFreshRSSCommandsAreStaticArgv(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realFreshRSS(t).SSO.OIDC.Env}
	tgt := Target{Container: "freshrss-freshrss-1"}
	if err := a.Enable(context.Background(), tgt, Settings{ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	if err := a.Disable(context.Background(), tgt); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %+v", f.calls)
	}
	for i, want := range []string{"php cli/reconfigure.php --auth-type http_auth", "php cli/reconfigure.php --auth-type form"} {
		c := f.calls[i]
		if c.container != "freshrss-freshrss-1" || c.user != "www-data" || c.stdin != "" || strings.Join(c.args, " ") != want {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	f.fail = func(call) (string, error) { return "", errors.New("not running") }
	err := a.Enable(context.Background(), tgt, Settings{})
	if err == nil || !strings.Contains(err.Error(), "--auth-type http_auth") {
		t.Fatalf("err = %v", err)
	}
	// An app with no command (every other env-oidc app) execs nothing.
	n := len(f.calls)
	if err := (envOIDC{}).Enable(context.Background(), tgt, Settings{}); err != nil || len(f.calls) != n {
		t.Fatalf("no-command adapter: err %v calls %d", err, len(f.calls)-n)
	}
}

func TestFreshRSSPlanWarnsAboutAdmin(t *testing.T) {
	m := realFreshRSS(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("freshrss.lan", "freshrss")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RedirectURIs) != 2 || plan.RedirectURIs[0] != "https://freshrss.lan/i/oidc/" || plan.SignInURL != "https://freshrss.lan/i/" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"default user", "preferred_username", "login form stops working", "turn sign-in off"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	changes := strings.Join(plan.Changes, "\n")
	if strings.Contains(changes, "local admin keeps working") || !strings.Contains(changes, "own login form stops working") {
		t.Fatalf("changes promise a local login:\n%s", changes)
	}
	if !strings.Contains(strings.Join(plan.AppSettings, "\n"), "php cli/reconfigure.php --auth-type http_auth") {
		t.Fatalf("settings:\n%s", strings.Join(plan.AppSettings, "\n"))
	}
	off, err := BuildPlan(Inputs{Manifest: m, Installed: true, Applied: &Record{Package: "freshrss", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}}, false)
	if err != nil {
		t.Fatal(err)
	}
	offText := strings.Join(append(off.AppSettings, off.Changes...), "\n")
	if !strings.Contains(offText, "--auth-type form") || strings.Contains(offText, "local admin keeps working") || !strings.Contains(offText, "login form") {
		t.Fatalf("off plan:\n%s", offText)
	}
}

// TestFreshRSSPlanApplyAndLifecycle (S412): sign-in on an install that
// predates both secrets wires them into the supervisor, sets the
// mod_auth_openidc environment, lays the combined bundle over the
// image's CAs for a private issuer only, runs the in-app commands, and
// takes it all out when sign-in is off.
func TestFreshRSSPlanApplyAndLifecycle(t *testing.T) {
	f := stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, string(testCertPEM(t, "system")))
	m := realFreshRSS(t)
	dir := t.TempDir()
	old := m
	old.SSO.OIDC = nil
	old.Secrets = nil
	old.Services = append([]catalog.Service(nil), old.Services...)
	old.Services[0].Entrypoint = nil
	files, err := catalog.Generate(old, catalog.Generation{
		Services: []catalog.ServiceGeneration{{Service: "freshrss"}}, RouteHosts: map[string]string{"freshrss": "freshrss.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		StackNeedsSecret: true, Routes: AppRoutes(m, []docker.Route{route("freshrss.lan", "freshrss")})}
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
	run(plan)
	secret := app.m[catalog.SecretStoreName("freshrss", "oidc_client_secret")]
	crypto := app.m[catalog.SecretStoreName("freshrss", "oidc_crypto_key")]
	if secret == "" || crypto == "" || secret == crypto {
		t.Fatalf("secrets not both generated: %q %q", secret, crypto)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	sup, _ := os.ReadFile(filepath.Join(dir, catalog.EntrypointFile("freshrss")))
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"OIDC_CLIENT_SECRET_FILE: /run/secrets/freshrss_oidc_client_secret", "OIDC_CLIENT_CRYPTO_KEY_FILE: /run/secrets/freshrss_oidc_crypto_key",
		"entrypoint:\n      - /homestead/secret-entrypoint.sh", "- ./Docker/entrypoint.sh", "exec apache2 -D FOREGROUND", "$$OIDC_ENABLED"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	for _, w := range []string{"export OIDC_CLIENT_SECRET\n", "export OIDC_CLIENT_CRYPTO_KEY\n"} {
		if !strings.Contains(string(sup), w) {
			t.Fatalf("supervisor lacks %q:\n%s", w, sup)
		}
	}
	for _, w := range []string{"OIDC_ENABLED: \"1\"", "OIDC_CLIENT_ID: homestead-freshrss",
		"OIDC_PROVIDER_METADATA_URL: https://authentik.lan/application/o/homestead-freshrss/.well-known/openid-configuration",
		"OIDC_REMOTE_USER_CLAIM: preferred_username", "OIDC_SCOPES: openid email profile",
		"OIDC_X_FORWARDED_HEADERS: X-Forwarded-Host X-Forwarded-Proto",
		"./" + CADir + "/bundle.pem:/etc/ssl/certs/ca-certificates.crt:ro"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	for name, body := range map[string][]byte{"compose": compose, "override": override, "supervisor": sup} {
		if strings.Contains(string(body), secret) || strings.Contains(string(body), crypto) {
			t.Fatalf("secret value in %s", name)
		}
	}
	if len(f.calls) != 1 || strings.Join(f.calls[0].args, " ") != "php cli/reconfigure.php --auth-type http_auth" {
		t.Fatalf("calls = %+v", f.calls)
	}
	pem := filepath.Join(dir, CADir, "bundle.pem")
	before, _ := os.Stat(pem)
	run(plan)
	after, _ := os.Stat(pem)
	if !os.SameFile(before, after) {
		t.Fatal("the bundle was replaced, so the single-file mount would go stale")
	}
	// Public issuer: the mount and the bundle go.
	rec, _, _ := Records{DB: st.DB()}.Get("freshrss")
	pub := in
	pub.Applied = &rec
	pub.PublicAuthentikURL = "https://auth.example.com"
	pub.Routes = AppRoutes(m, []docker.Route{route("freshrss.lan", "freshrss", "freshrss.example.com")})
	plan2, err := BuildPlan(pub, true)
	if err != nil {
		t.Fatal(err)
	}
	run(plan2)
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "ca-certificates.crt") || !strings.Contains(string(override), "https://auth.example.com/application/o/homestead-freshrss/.well-known/openid-configuration") {
		t.Fatalf("public issuer override:\n%s", override)
	}
	if _, err := os.Stat(pem); !os.IsNotExist(err) {
		t.Fatalf("bundle remains on a public issuer: %v", err)
	}
	run(plan)
	if _, err := os.Stat(pem); err != nil {
		t.Fatalf("bundle not regenerated: %v", err)
	}
	// Off: the form command runs before the wiring goes.
	rec, _, _ = Records{DB: st.DB()}.Get("freshrss")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	n := len(f.calls)
	run(off)
	if last := f.calls[len(f.calls)-1]; len(f.calls) != n+1 || strings.Join(last.args, " ") != "php cli/reconfigure.php --auth-type form" {
		t.Fatalf("off calls = %+v", f.calls[n:])
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "OIDC_") || strings.Contains(string(override), "ca-certificates") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
	if _, err := os.Stat(filepath.Join(dir, CADir)); !os.IsNotExist(err) {
		t.Fatalf("CA dir remains after off: %v", err)
	}
}

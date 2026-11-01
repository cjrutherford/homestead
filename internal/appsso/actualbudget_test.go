package appsso

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// actualSignInVerified is the Actual Budget version the sign-in
// configuration in catalog/actual-budget.yaml was verified against
// (S358): the ACTUAL_OPENID_* environment names and the
// ACTUAL_OPENID_CLIENT_SECRET_FILE form (load-config.js), the forced
// OpenID bootstrap at startup (app.ts), the auth table in
// server-files/account.sqlite (account-db.js), the callback
// /openid/callback built from the server hostname, the first sign-in
// becoming owner and ADMIN, and ACTUAL_USER_CREATION_MODE=login.
const actualSignInVerified = "26.10.0"

func realActual(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("actual-budget")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("actual-budget manifest: %v", err)
	}
	return m
}

// TestActualBudgetSignInPinnedToVerifiedVersion: the environment names,
// the account database shape and the first-user-is-owner rule are
// Actual's own and can change in any release. Re-verify before bumping.
func TestActualBudgetSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realActual(t)
	image := m.Services[0].Image
	if m.Version != actualSignInVerified || !strings.HasSuffix(image, ":"+actualSignInVerified) {
		t.Fatalf("actual-budget is %s (%s) but its Authentik sign-in configuration was verified against %s. Before bumping: re-check "+
			"the ACTUAL_OPENID_* names and ACTUAL_OPENID_CLIENT_SECRET_FILE (packages/sync-server/src/load-config.js), the startup bootstrap (app.ts), "+
			"the auth table in account.sqlite (account-db.js), the /openid/callback redirect and owner/user creation (accounts/openid.ts), "+
			"/app/package.json and node:sqlite in the image, then update the script's VERIFIED version and set actualSignInVerified here (ADR-052, S358).",
			m.Version, image, actualSignInVerified)
	}
	if !strings.Contains(m.SSO.OIDC.Env.OnApply[2], `const VERIFIED = '`+actualSignInVerified+`'`) {
		t.Fatalf("the script's VERIFIED constant is not %s", actualSignInVerified)
	}
}

func TestActualBudgetEnvKeepsTheSecretInAFile(t *testing.T) {
	m := realActual(t)
	a := envOIDC{cfg: *m.SSO.OIDC.Env}
	env := a.Env(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-actual-budget/", ClientID: "homestead-actual-budget", ClientSecret: "zzz-secret"})
	want := map[string]string{
		"ACTUAL_OPENID_DISCOVERY_URL": "https://homestead.lan/apps/auth/application/o/homestead-actual-budget/.well-known/openid-configuration",
		"ACTUAL_OPENID_CLIENT_ID":     "homestead-actual-budget", "ACTUAL_OPENID_PROVIDER_NAME": "Authentik", "ACTUAL_USER_CREATION_MODE": "login"}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for k, v := range env {
		if strings.Contains(v, "zzz-secret") || k == "ACTUAL_OPENID_CLIENT_SECRET" || k == "ACTUAL_OPENID_ENFORCE" {
			t.Errorf("unexpected %s", k)
		}
	}
	var found bool
	for _, s := range m.Secrets {
		for _, c := range s.Consumers {
			found = found || (s.Name == "oidc_client_secret" && c.Env == "ACTUAL_OPENID_CLIENT_SECRET_FILE")
		}
	}
	if !found || m.Services[0].Environment["ACTUAL_OPENID_SERVER_HOSTNAME"] != "{{app_url}}" {
		t.Fatal("the secret must reach Actual as ACTUAL_OPENID_CLIENT_SECRET_FILE and the hostname follow the app address")
	}
	if got := a.TrustEnvFor("https://homestead.lan/apps/auth/application/o/x/"); got["NODE_EXTRA_CA_CERTS"] != CAFile || len(got) != 1 {
		t.Fatalf("trust = %v", got)
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/x/"); got != nil {
		t.Fatalf("public issuer trust = %v", got)
	}
}

func TestActualBudgetPlanRedirectAndWarnings(t *testing.T) {
	m := realActual(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("actual-budget.lan", "actual-budget")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RedirectURIs) < 1 || plan.RedirectURIs[0] != "https://actual-budget.lan/openid/callback" || plan.SignInURL != "https://actual-budget.lan/login" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"server owner", "cannot be changed", "second choice", "asks for a new server password", "account database", "never in Compose"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
}

func TestActualBudgetCommandsRunWithoutRestartOrSecret(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realActual(t).SSO.OIDC.Env}
	tgt := Target{Container: "actual-budget-actual-budget-1"}
	if err := a.Enable(context.Background(), tgt, Settings{ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	if err := a.Disable(context.Background(), tgt); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || len(f.restarts) != 0 {
		t.Fatalf("calls %d restarts %v", len(f.calls), f.restarts)
	}
	for i, mode := range []string{"on", "off"} {
		c := f.calls[i]
		if c.user != "" || c.stdin != "" || len(c.args) != 4 || c.args[0] != "node" || c.args[3] != mode {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
}

// TestActualBudgetScriptAgainstARealAuthTable runs the inline script with
// real node and node:sqlite (skipped when this host's node lacks it)
// against an auth table shaped like Actual's.
func TestActualBudgetScriptAgainstARealAuthTable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	if out, err := exec.Command(node, "-e", "require('node:sqlite')").CombinedOutput(); err != nil {
		t.Skipf("no node:sqlite: %s", out)
	}
	script := realActual(t).SSO.OIDC.Env.OnApply[2]
	dir := t.TempDir()
	db := filepath.Join(dir, "account.sqlite")
	pkg := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"version":"`+actualSignInVerified+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	patched := strings.NewReplacer("/data/server-files/account.sqlite", db, "/app/package.json", pkg).Replace(script)
	run := func(mode string) (string, error) {
		out, err := exec.Command(node, "-e", patched, mode).CombinedOutput()
		return string(out), err
	}
	sql := func(q string) string {
		out, err := exec.Command(node, "--no-warnings", "-e", `const {DatabaseSync}=require('node:sqlite');const d=new DatabaseSync(process.argv[1]);const q=process.argv[2];
if(/^select/i.test(q)){console.log(JSON.stringify(d.prepare(q).all()))}else{d.exec(q)}`, db, q).Output()
		if err != nil {
			t.Fatalf("sql %q: %v %s", q, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if _, err := run("on"); err == nil {
		t.Fatal("on without a database succeeded")
	}
	sql("CREATE TABLE auth (method TEXT, display_name TEXT, extra_data TEXT, active INTEGER)")
	if out, err := run("on"); err == nil || !strings.Contains(out, "not switched to OpenID yet") {
		t.Fatalf("on before the switch: %v %s", err, out)
	}
	sql("INSERT INTO auth VALUES ('password','Password','hash',0),('openid','OpenID','{\"client_secret\":\"s\"}',1)")
	if out, err := run("on"); err != nil || !strings.Contains(out, "sign-in on") {
		t.Fatalf("on: %v %s", err, out)
	}
	if out, err := run("off"); err != nil || !strings.Contains(out, "sign-in off") {
		t.Fatalf("off: %v %s", err, out)
	}
	if got := sql("SELECT method, active FROM auth"); got != `[{"method":"password","active":1}]` {
		t.Fatalf("auth after off = %s", got)
	}
	if out, err := run("off"); err != nil || !strings.Contains(out, "already off") {
		t.Fatalf("second off: %v %s", err, out)
	}
	sql("DELETE FROM auth")
	sql("INSERT INTO auth VALUES ('openid','OpenID','x',1)")
	if _, err := run("off"); err != nil {
		t.Fatal(err)
	}
	if got := sql("SELECT method FROM auth"); got != "[]" {
		t.Fatalf("auth = %s", got)
	}
	// A different version refuses to turn sign-in on, but off still works.
	_ = os.WriteFile(pkg, []byte(`{"version":"99.0.0"}`), 0o600)
	if out, err := run("on"); err == nil || !strings.Contains(out, "verified against") {
		t.Fatalf("version guard: %v %s", err, out)
	}
	if _, err := run("off"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"console.log(row", "SELECT extra_data", "execSync", "spawn"} {
		if strings.Contains(script, bad) {
			t.Errorf("script contains %q", bad)
		}
	}
}

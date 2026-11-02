package appsso

import (
	"context"
	"errors"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// bookstackSignInVerified is the BookStack version the role script in
// catalog/bookstack.yaml was verified against (S366): the roles table
// (display_name, system_name, external_auth_id, updated_at), GroupSyncService
// role matching, /app/www/version and the migrations table.
const bookstackSignInVerified = "26.09.2"

func realBookStack(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("bookstack")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("bookstack manifest: %v", err)
	}
	return m
}

// TestBookStackSignInPinnedToVerifiedVersion: the role script writes
// BookStack's own database (ADR-052 amendments). Bumping the package is not
// enough; the script must be re-verified first.
func TestBookStackSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realBookStack(t)
	image := m.Services[0].Image
	if m.Version != bookstackSignInVerified || !strings.HasSuffix(image, ":"+bookstackSignInVerified) {
		t.Fatalf("bookstack is %s (%s) but its Authentik sign-in script (the inline onapply/onoff script in catalog/bookstack.yaml) "+
			"was verified against %s. Before bumping: re-verify the roles table columns and how GroupSyncService matches external_auth_id "+
			"(app/Access/GroupSyncService.php), /app/www/version, the migrations table, the OIDC env names (app/Config/oidc.php) and the callback path, "+
			"update the script's VERIFIED version, then set bookstackSignInVerified here (ADR-052, S366).", m.Version, image, bookstackSignInVerified)
	}
	env := m.SSO.OIDC.Env
	script := env.OnApply[2]
	if !strings.Contains(script, `$VERIFIED = 'v`+bookstackSignInVerified+`'`) {
		t.Fatalf("the script's VERIFIED constant is not v%s", bookstackSignInVerified)
	}
	if len(env.OnOff) != 4 || env.OnOff[2] != script || env.OnApply[3] != "on" || env.OnOff[3] != "off" {
		t.Fatalf("on/off commands = %v / %v", env.OnApply[:2], env.OnOff[:2])
	}
}

func TestBookStackScriptGuards(t *testing.T) {
	script := realBookStack(t).SSO.OIDC.Env.OnApply[2]
	for _, want := range []string{
		"/app/www/version",                                         // version guard
		"information_schema.COLUMNS",                               // schema guard
		"beginTransaction", "FOR UPDATE", "commit()", "rollBack()", // one transaction
		"already has External Authentication IDs of its own", // never overwrite the owner's values
		"still migrating",   // never before the schema exists
		"FILE__DB_PASSWORD", // password read in-process
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	for _, bad := range []string{"getMessage", "echo $pw", "print_r", "var_dump", "exec(", "shell_exec", "system("} {
		if strings.Contains(script, bad) {
			t.Errorf("script contains %q", bad)
		}
	}
	// It writes the roles table and nothing else.
	for _, stmt := range []string{"INSERT", "DELETE", "DROP", "ALTER", "UPDATE users", "UPDATE settings"} {
		if strings.Contains(script, stmt) {
			t.Errorf("script contains %q", stmt)
		}
	}
	if strings.Count(script, "UPDATE roles SET external_auth_id") != 1 {
		t.Error("expected exactly one UPDATE of roles.external_auth_id")
	}
}

func TestBookStackPlanRedirectsRolesAndWarnings(t *testing.T) {
	m := realBookStack(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("bookstack.lan", "bookstack")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RedirectURIs) != 2 || plan.RedirectURIs[0] != "https://bookstack.lan/oidc/callback" || plan.SignInURL != "https://bookstack.lan/login" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"own login form", "Editor", "External Authentication IDs", "same email", "random passwords", "bookstack:create-admin"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	settings := strings.Join(plan.AppSettings, "\n")
	for _, w := range []string{"AUTH_METHOD", "OIDC_USER_TO_GROUPS", "OIDC_ISSUER_DISCOVER", "HOMESTEAD_ADMIN_GROUP", "php -r <", "-line script> on"} {
		if !strings.Contains(settings, w) {
			t.Errorf("settings lack %q:\n%s", w, settings)
		}
	}
	if strings.Contains(settings, "restarted afterwards") {
		t.Errorf("BookStack reads its environment per request after the container is recreated; no extra restart:\n%s", settings)
	}
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	g := groupNames("bookstack")
	rm := a.Roles(g)
	if !rm.Mapped || !strings.Contains(rm.Note, g.Admins) {
		t.Fatalf("roles = %+v", rm)
	}
	off, err := BuildPlan(Inputs{Manifest: m, Installed: true, Applied: &Record{Package: "bookstack", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(off.AppSettings, "\n"), "-line script> off") {
		t.Fatalf("off plan:\n%s", strings.Join(off.AppSettings, "\n"))
	}
}

func TestBookStackEnvRendersGroupsAndKeepsSecretOut(t *testing.T) {
	a := envOIDC{cfg: *realBookStack(t).SSO.OIDC.Env}
	g := groupNames("bookstack")
	env := a.Env(Settings{Issuer: "https://auth.example.com/application/o/homestead-bookstack/", ClientID: "cid", ClientSecret: "zzz-secret", Groups: g})
	want := map[string]string{"AUTH_METHOD": "oidc", "OIDC_CLIENT_ID": "cid", "OIDC_ISSUER": "https://auth.example.com/application/o/homestead-bookstack/",
		"OIDC_ISSUER_DISCOVER": "true", "OIDC_USER_TO_GROUPS": "true", "OIDC_GROUPS_CLAIM": "groups", "OIDC_REMOVE_FROM_GROUPS": "true",
		"OIDC_END_SESSION_ENDPOINT": "true", "HOMESTEAD_ADMIN_GROUP": g.Admins, "HOMESTEAD_USER_GROUP": g.Users}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for k, v := range env {
		if strings.Contains(v, "zzz-secret") || strings.Contains(k, "SECRET") {
			t.Fatalf("secret in %s", k)
		}
	}
	if !a.Exclusive() {
		t.Error("sign-in should be exclusive")
	}
	// The appliance's own issuer needs its CA laid over the image bundle.
	if mount := a.TrustMount("https://homestead.lan/apps/auth/application/o/homestead-bookstack/"); !strings.HasSuffix(mount, ":/etc/ssl/certs/ca-certificates.crt:ro") {
		t.Fatalf("mount = %q", mount)
	}
	if a.TrustMount("https://auth.example.com/application/o/homestead-bookstack/") != "" {
		t.Fatal("a public issuer needs no trust mount")
	}
}

func TestBookStackCommandsRunAsImageUser(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realBookStack(t).SSO.OIDC.Env}
	tgt := Target{Container: "bookstack-bookstack"}
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
		if c.container != tgt.Container || c.stdin != "" || len(c.args) != 4 || c.args[0] != "php" || c.args[1] != "-r" || c.args[3] != mode {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	f.fail = func(call) (string, error) {
		return "", errors.New("bookstack sign-in: this is BookStack v26.10.0 but the script was verified against v26.09.2")
	}
	if err := a.Enable(context.Background(), tgt, Settings{}); err == nil || !strings.Contains(err.Error(), "verified against") {
		t.Fatalf("err = %v", err)
	}
}

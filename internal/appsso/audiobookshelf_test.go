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

// absSignInVerified is the Audiobookshelf version the sign-in script in
// catalog/audiobookshelf.yaml was verified against (S417): the settings
// table and the server-settings auth fields, /app/package.json,
// node:sqlite in the image's node, and the startup-only read.
const absSignInVerified = "2.37.1"

func realAudiobookshelf(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("audiobookshelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("audiobookshelf manifest: %v", err)
	}
	return m
}

// TestAudiobookshelfSignInPinnedToVerifiedVersion: the sign-in script
// writes Audiobookshelf's own settings database, an internal schema that
// can change in any release (and so can its logging of secrets and
// userinfo). Bumping the package is not enough; the script must be
// re-verified first.
func TestAudiobookshelfSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realAudiobookshelf(t)
	image := m.Services[0].Image
	if m.Version != absSignInVerified || !strings.HasSuffix(image, ":"+absSignInVerified) {
		t.Fatalf("audiobookshelf is %s (%s) but its Authentik sign-in script (the inline onapply/onoff script in catalog/audiobookshelf.yaml) "+
			"was verified against %s. Before bumping: re-verify the settings table and the server-settings auth fields in /config/absdatabase.sqlite "+
			"(server/objects/settings/ServerSettings.js), /app/package.json, node:sqlite in the image, that settings are read only at startup, "+
			"the redirect paths and the group claim handling (server/auth/OidcAuthStrategy.js), what the app logs (userinfo, the client secret), "+
			"update the script's VERIFIED version, then set absSignInVerified here (ADR-052, S417).", m.Version, image, absSignInVerified)
	}
	script := m.SSO.OIDC.Env.OnApply[2]
	if !strings.Contains(script, `const VERIFIED = '`+absSignInVerified+`'`) {
		t.Fatalf("the script's VERIFIED constant is not %s", absSignInVerified)
	}
}

func TestAudiobookshelfSchemaValidation(t *testing.T) {
	cases := map[string]func(e *catalog.EnvOIDC){
		"restart needs onapply":           func(e *catalog.EnvOIDC) { e.OnApply, e.OnOff = nil, nil },
		"static text":                     func(e *catalog.EnvOIDC) { e.OnApply = []string{"node", "{{client_id}}"} },
		"adminvalue and uservalue come":   func(e *catalog.EnvOIDC) { e.UserValue = "" },
		"role value \"A b\" invalid":      func(e *catalog.EnvOIDC) { e.AdminValue = "A b" },
		"must differ":                     func(e *catalog.EnvOIDC) { e.UserValue = e.AdminValue },
		"must use {{client_secret}}":      func(e *catalog.EnvOIDC) { e.SecretFile.Template = `{"a":"{{client_id}}"}` },
		"unknown placeholder \"app_url\"": func(e *catalog.EnvOIDC) { e.SecretFile.Template += "{{app_url}}{{client_secret}}" },
	}
	for want, mutate := range cases {
		m := realAudiobookshelf(t)
		e := *m.SSO.OIDC.Env
		sf := *e.SecretFile
		e.SecretFile = &sf
		mutate(&e)
		m.SSO.OIDC.Env = &e
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestAudiobookshelfDerivedSecretIsEscapedJSON(t *testing.T) {
	a := envOIDC{cfg: *realAudiobookshelf(t).SSO.OIDC.Env}
	// Every endpoint is filled from Authentik's fixed paths: the app does no
	// discovery and drops OpenID unless all of them are set.
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-audiobookshelf/",
		ClientID: `id"x`, ClientSecret: `s\"e`})
	if !ok || name != "audiobookshelf_signin" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	base := "https://homestead.lan/apps/auth/application/o/"
	want := map[string]string{"client_id": `id"x`, "client_secret": `s\"e`, "issuer": base + "homestead-audiobookshelf/",
		"authorize_url": base + "authorize/", "token_url": base + "token/", "userinfo_url": base + "userinfo/",
		"jwks_url": base + "homestead-audiobookshelf/jwks/", "logout_url": base + "homestead-audiobookshelf/end-session/"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("values = %v", got)
	}
	// The secret reaches neither the environment nor the redirect host.
	for k, v := range a.Env(Settings{ClientSecret: "zzz-secret"}) {
		if strings.Contains(v, "zzz-secret") {
			t.Fatalf("secret in %s", k)
		}
	}
}

func TestAudiobookshelfPlanRedirectsRolesWarningsAndScript(t *testing.T) {
	m := realAudiobookshelf(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("audiobookshelf.lan", "audiobookshelf")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RedirectURIs) != 4 || plan.RedirectURIs[0] != "https://audiobookshelf.lan/auth/openid/callback" ||
		plan.RedirectURIs[1] != "https://audiobookshelf.lan/auth/openid/mobile-redirect" ||
		plan.RedirectURIs[3] != "https://audiobookshelf.local/auth/openid/mobile-redirect" || plan.SignInURL != "https://audiobookshelf.lan/login" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"own database", "re-checked on every Audiobookshelf update", "Run this after the app has started once", "logs the signed-in person's profile",
		"owner accepted", "address the person opened", "never matched or taken over", "second account", "administrators and everyone else a user", "root account",
		"recovery administrator stay", "plaintext", "absdatabase.sqlite", "audiobookshelf://oauth", "mobile-redirect"} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	settings := strings.Join(plan.AppSettings, "\n")
	if !strings.Contains(settings, "node -e <") || !strings.Contains(settings, "-line script> on") || !strings.Contains(settings, "restarted afterwards") ||
		!strings.Contains(settings, `role "admin"`) || strings.Contains(settings, "BEGIN IMMEDIATE") {
		t.Fatalf("settings:\n%s", settings)
	}
	if !strings.Contains(strings.Join(plan.Changes, "\n"), "local admin keeps working") {
		t.Fatalf("a non-exclusive app keeps its local login in the plan:\n%s", strings.Join(plan.Changes, "\n"))
	}
	off, err := BuildPlan(Inputs{Manifest: m, Installed: true, Applied: &Record{Package: "audiobookshelf", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(off.AppSettings, "\n"), "-line script> off") {
		t.Fatalf("off plan:\n%s", strings.Join(off.AppSettings, "\n"))
	}
}

// TestAudiobookshelfGroupClaimCarriesTheRoleValues: the app reads the
// values "admin" and "user" from the groups claim (it also asks for a
// scope of the claim's name, so Homestead's groups mapping applies). A
// person with neither value is refused, so everyone bound to the app
// gets one.
func TestAudiobookshelfGroupClaimCarriesTheRoleValues(t *testing.T) {
	a, err := AdapterFor(*realAudiobookshelf(t).SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	g := groupNames("audiobookshelf")
	rm := a.Roles(g)
	if !rm.Mapped || rm.AdminAlias != "admin" || rm.UserAlias != "user" || !strings.Contains(rm.Note, g.Admins) {
		t.Fatalf("roles = %+v", rm)
	}
	expr := claimExpression(g, rm)
	for _, want := range []string{`"homestead-audiobookshelf-admins" in names`, `startswith("homestead-")`,
		"if admin:\n    claims[\"groups\"].append(\"admin\")", "if not admin:\n    claims[\"groups\"].append(\"user\")"} {
		if !strings.Contains(expr, want) {
			t.Errorf("expression lacks %q:\n%s", want, expr)
		}
	}
	// Other apps do not get a user value.
	if strings.Contains(claimExpression(g, nextcloud{}.Roles(g)), "if not admin") {
		t.Fatal("a user alias leaked into the Nextcloud claim")
	}
}

func TestAudiobookshelfTrustFollowsIssuer(t *testing.T) {
	a := envOIDC{cfg: *realAudiobookshelf(t).SSO.OIDC.Env}
	// Node adds this file to its own CAs: the bare appliance root, no bundle.
	if got := a.TrustEnvFor("https://homestead.lan/apps/auth/application/o/homestead-audiobookshelf/"); got["NODE_EXTRA_CA_CERTS"] != CAFile || len(got) != 1 {
		t.Fatalf("private issuer trust = %v", got)
	}
	if a.CombinedBundleFor("https://homestead.lan/apps/auth/application/o/homestead-audiobookshelf/") {
		t.Fatal("a combined bundle is written for Node")
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/homestead-audiobookshelf/"); got != nil {
		t.Fatalf("public issuer trust = %v", got)
	}
}

// TestAudiobookshelfCommandsRunThenRestart: the script runs as the image's
// own user (root) with the mode as its last argument, nothing secret
// travels in argv or stdin, and the container restarts only after the
// script succeeded.
func TestAudiobookshelfCommandsRunThenRestart(t *testing.T) {
	f := stubDocker(t)
	a := envOIDC{cfg: *realAudiobookshelf(t).SSO.OIDC.Env}
	tgt := Target{Container: "audiobookshelf-audiobookshelf-1"}
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
		if c.container != tgt.Container || c.user != "" || c.stdin != "" || len(c.args) != 4 || c.args[0] != "node" || c.args[1] != "-e" || c.args[3] != mode {
			t.Fatalf("call %d = %+v", i, c)
		}
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	f.restarts = nil
	f.fail = func(call) (string, error) {
		return "", errors.New("audiobookshelf sign-in: this is Audiobookshelf 2.38.0 but the script was verified against 2.37.1")
	}
	err := a.Enable(context.Background(), tgt, Settings{})
	if err == nil || len(f.restarts) != 0 || !strings.Contains(err.Error(), "verified against") {
		t.Fatalf("err = %v restarts %v", err, f.restarts)
	}
	f.fail = nil
	old := restart
	restart = func(context.Context, string) error { return errors.New("no daemon") }
	defer func() { restart = old }()
	if err := a.Enable(context.Background(), tgt, Settings{}); err == nil || !strings.Contains(err.Error(), "restarting") {
		t.Fatalf("restart error = %v", err)
	}
}

// TestAudiobookshelfScriptKeepsTheSecretOutOfOutput: the script prints no
// settings, never an exception message that could quote the secret file,
// and reads the file inside the process.
func TestAudiobookshelfScriptKeepsTheSecretOutOfOutput(t *testing.T) {
	script := realAudiobookshelf(t).SSO.OIDC.Env.OnApply[2]
	for _, bad := range []string{"e.message", "JSON.stringify(v", "console.log(s", "console.log(v", "console.error(s", "console.error(v", "execSync", "spawn"} {
		if strings.Contains(script, bad) {
			t.Errorf("script contains %q", bad)
		}
	}
	if !strings.Contains(script, "readFileSync(process.env.ABS_SIGNIN_FILE") || !strings.Contains(script, "BEGIN IMMEDIATE") || !strings.Contains(script, "busy_timeout") {
		t.Fatal("script does not read the file in-process inside an immediate transaction")
	}
}

// TestAudiobookshelfApplyKeepsSecretInTheFile applies and turns sign-in off
// on a fresh install: the client secret lives only in the 0600 file and
// the secret store, and the app restarts.
func TestAudiobookshelfApplyKeepsSecretInTheFile(t *testing.T) {
	f := stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	m := realAudiobookshelf(t)
	dir := t.TempDir()
	files, err := catalog.Generate(m, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "audiobookshelf"}},
		RouteHosts: map[string]string{"audiobookshelf": "audiobookshelf.lan"}})
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
		Routes: AppRoutes(m, []docker.Route{route("audiobookshelf.lan", "audiobookshelf")})}
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
	store := catalog.SecretStoreName("audiobookshelf", "audiobookshelf_signin")
	file := filepath.Join(dir, catalog.SecretsDirName, "audiobookshelf_audiobookshelf_signin")
	run(plan)
	secret := app.m[catalog.SecretStoreName("audiobookshelf", "oidc_client_secret")]
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
	if !strings.Contains(string(compose), "ABS_SIGNIN_FILE: /run/secrets/audiobookshelf_audiobookshelf_signin") {
		t.Fatalf("compose lacks the file path:\n%s", compose)
	}
	if !strings.Contains(string(override), "NODE_EXTRA_CA_CERTS: /etc/homestead/ca/ca.crt") || !strings.Contains(string(override), "ABS_OIDC_ADMIN_GROUP: homestead-audiobookshelf-admins") {
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
	rec, _, _ := Records{DB: st.DB()}.Get("audiobookshelf")
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
	if strings.Contains(string(override), "ABS_OIDC_ISSUER") || strings.Contains(string(override), "NODE_EXTRA_CA_CERTS") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

// S473 (ADR-061): Audiobookshelf 2.37.1 cannot be served at /apps/<app>/.
// ROUTER_BASE_PATH moves the API and socket paths, but the web client is
// built with /audiobookshelf baked into every asset URL (index.html and the
// _nuxt bundles), so under any other prefix the page never loads. The
// package therefore keeps its own name. Re-verify on a version bump.
func TestAudiobookshelfStaysOnItsOwnName(t *testing.T) {
	m := realAudiobookshelf(t)
	if m.Publish != catalog.PublishHost || m.PublishReason != catalog.ReasonNoSubpath || m.ServedByPath() {
		t.Fatalf("audiobookshelf publish %q reason %q serve %q: it must keep its own name (S473)", m.Publish, m.PublishReason, m.Serve)
	}
	if _, set := m.Services[0].Environment["ROUTER_BASE_PATH"]; set {
		t.Fatal("ROUTER_BASE_PATH must stay unset: the client assets only load under /audiobookshelf")
	}
}

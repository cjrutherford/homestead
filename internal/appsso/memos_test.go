package appsso

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// memosSignInVerified is the Memos version the sign-in configuration file
// in catalog/memos.yaml was verified against (S359): the declarative
// configuration directory (/etc/memos/config, *.json, applied at start),
// the identityProviders / removed document shape, the callback
// <origin>/auth/callback and that new SSO users are created as ordinary
// users only while registration is allowed.
const memosSignInVerified = "26.10.0"

const memosRemoveDoc = `{"removed":[{"resource":"identity-providers/authentik","destroy":true}],"instanceSettings":[{"name":"instance/settings/GENERAL","generalSetting":{"disallowUserRegistration":true}}]}`

func realMemos(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("memos")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("memos manifest: %v", err)
	}
	return m
}

// TestMemosSignInPinnedToVerifiedVersion: the configuration document is
// Memos's own, which can change in any release. Re-verify it before
// bumping the package.
func TestMemosSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realMemos(t)
	if m.Version != memosSignInVerified || !strings.HasSuffix(m.Services[0].Image, ":"+memosSignInVerified) {
		t.Fatalf("memos is %s (%s) but its Authentik sign-in file was verified against %s. Before bumping: re-check "+
			"the configuration directory and file rules, the identityProviders and removed shapes (a managed provider "+
			"that vanishes from the files fails the plan), the /auth/callback redirect and the SSO user creation rules "+
			"(core/configuration, server/api/auth_service_sso.go in usememos/memos), then set memosSignInVerified here (ADR-052, S359).",
			m.Version, m.Services[0].Image, memosSignInVerified)
	}
}

func TestMemosConfigFileIsEscapedProviderJSON(t *testing.T) {
	a := envOIDC{cfg: *realMemos(t).SSO.OIDC.Env}
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-memos/",
		ClientID: `id"x`, ClientSecret: `s\"e`})
	if !ok || name != "memos_config" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got struct {
		IdentityProviders []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Title  string `json:"title"`
			Config struct {
				OAuth2 struct {
					ClientID     string   `json:"clientId"`
					ClientSecret string   `json:"clientSecret"`
					AuthURL      string   `json:"authUrl"`
					TokenURL     string   `json:"tokenUrl"`
					UserInfoURL  string   `json:"userInfoUrl"`
					Scopes       []string `json:"scopes"`
				} `json:"oauth2Config"`
			} `json:"config"`
		} `json:"identityProviders"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	if len(got.IdentityProviders) != 1 {
		t.Fatalf("providers = %+v", got.IdentityProviders)
	}
	p, c := got.IdentityProviders[0], got.IdentityProviders[0].Config.OAuth2
	if p.Name != "identity-providers/authentik" || p.Type != "OAUTH2" || p.Title != "Authentik" ||
		c.ClientID != `id"x` || c.ClientSecret != `s\"e` ||
		c.AuthURL != "https://homestead.lan/apps/auth/application/o/authorize/" ||
		c.TokenURL != "https://homestead.lan/apps/auth/application/o/token/" ||
		c.UserInfoURL != "https://homestead.lan/apps/auth/application/o/userinfo/" ||
		strings.Join(c.Scopes, " ") != "openid profile email" {
		t.Fatalf("provider = %+v", p)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(value), &raw)
	fm := raw["identityProviders"].([]any)[0].(map[string]any)["config"].(map[string]any)["oauth2Config"].(map[string]any)["fieldMapping"].(map[string]any)
	if fm["identifier"] != "preferred_username" || fm["email"] != "email" {
		t.Fatalf("fieldMapping = %v", fm)
	}
	// While sign-in is on, password sign-up is off (nobody can register a
	// password account) but registration stays open for Authentik's first
	// sign-in (S446).
	if !strings.Contains(value, `"disallowPasswordAuth":true`) || strings.Contains(value, "disallowUserRegistration") {
		t.Fatalf("instance settings: %s", value)
	}
	if env := a.Env(Settings{Issuer: "https://x/application/o/s/"}); len(env) != 1 || env["HOMESTEAD_OIDC_ISSUER"] == "" {
		t.Fatalf("env = %v", env)
	}
}

func TestMemosPlanRedirectAndWarnings(t *testing.T) {
	m := realMemos(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("memos.lan", "memos")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RedirectURIs[0] != "https://memos.lan/auth/callback" || plan.SignInURL != "https://memos.lan/auth" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	if all := strings.Join(plan.Warnings, "\n"); !strings.Contains(all, "administrator is homestead-admin") {
		t.Fatalf("warnings = %s", all)
	}
}

// TestMemosApplyMountsConfigWithoutLeakingSecret installs from a fresh
// stack, applies, re-applies and turns sign-in off.
func TestMemosApplyMountsConfigWithoutLeakingSecret(t *testing.T) {
	stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, string(testCertPEM(t, "system")))
	m := realMemos(t)
	dir := t.TempDir()
	files, err := catalog.Generate(m, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "memos"}},
		RouteHosts: map[string]string{"memos": "memos.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	app.m[catalog.SecretStoreName("memos", "memos_config")] = memosRemoveDoc
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("memos.lan", "memos")})}
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
	config := catalog.SecretStoreName("memos", "memos_config")
	file := filepath.Join(dir, catalog.SecretsDirName, "memos_memos_config")
	composeBefore, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	for _, w := range []string{"source: memos_memos_config", "target: /etc/memos/config/homestead.json"} {
		if !strings.Contains(string(composeBefore), w) {
			t.Fatalf("compose lacks %q:\n%s", w, composeBefore)
		}
	}
	run(plan)
	secret := app.m[catalog.SecretStoreName("memos", "oidc_client_secret")]
	if secret == "" || !strings.Contains(app.m[config], secret) {
		t.Fatalf("client secret %q not rendered into the config %q", secret, app.m[config])
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != app.m[config] {
		t.Fatalf("secret file = %q, %v", body, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"HOMESTEAD_OIDC_ISSUER", "SSL_CERT_FILE"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	for name, b := range map[string][]byte{"compose": compose, "override": override} {
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "clientSecret") {
			t.Fatalf("secret or provider JSON in %s", name)
		}
	}
	if strings.Contains(string(override), "DISALLOW") || strings.Contains(string(override), "PASSWORD") {
		t.Fatalf("local login touched:\n%s", override)
	}
	run(plan)
	if app.m[catalog.SecretStoreName("memos", "oidc_client_secret")] != secret {
		t.Fatal("the client secret changed on re-apply")
	}
	rec, _, _ := Records{DB: st.DB()}.Get("memos")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	run(off)
	body, _ = os.ReadFile(file)
	if string(body) != memosRemoveDoc || app.m[config] != memosRemoveDoc {
		t.Fatalf("off leaves provider setting: file %q store %q", body, app.m[config])
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "HOMESTEAD_OIDC_ISSUER") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

// TestMemosStartingFileRemovesTheProvider: Memos fails to start when a
// provider it manages disappears from the files without a removed entry,
// so the file's off value is an explicit removal, not an empty document.
func TestMemosStartingFileRemovesTheProvider(t *testing.T) {
	m := realMemos(t)
	for _, d := range m.Secrets {
		if d.Name == "memos_config" {
			if d.Initial != memosRemoveDoc {
				t.Fatalf("initial = %s", d.Initial)
			}
			return
		}
	}
	t.Fatal("memos_config not declared")
}

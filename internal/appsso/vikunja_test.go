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

// vikunjaSignInVerified is the Vikunja version the sign-in configuration
// shape in catalog/vikunja.yaml was verified against (S357): the config
// file search path (/etc/vikunja/config.yml), the auth.openid.providers
// keys (name, authurl, clientid, clientsecret, scope), the callback
// /auth/openid/<provider key> and that providers are read only from the
// config file, not from *_FILE variables.
const vikunjaSignInVerified = "2.7.1"

func realVikunja(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("vikunja")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("vikunja manifest: %v", err)
	}
	return m
}

// TestVikunjaSignInPinnedToVerifiedVersion: the provider file shape is
// Vikunja's own configuration, which can change in any release. Re-verify
// it before bumping the package.
func TestVikunjaSignInPinnedToVerifiedVersion(t *testing.T) {
	m := realVikunja(t)
	if m.Version != vikunjaSignInVerified || !strings.HasSuffix(m.Services[0].Image, ":"+vikunjaSignInVerified) {
		t.Fatalf("vikunja is %s (%s) but its Authentik sign-in file shape was verified against %s. Before bumping: re-check "+
			"the config file search path, auth.openid.providers keys, the /auth/openid/<key> callback and that providers cannot come from "+
			"*_FILE variables (pkg/config in go-vikunja/vikunja), then set vikunjaSignInVerified here (ADR-052, S357).",
			m.Version, m.Services[0].Image, vikunjaSignInVerified)
	}
}

func TestVikunjaConfigFileIsEscapedProviderJSON(t *testing.T) {
	a := envOIDC{cfg: *realVikunja(t).SSO.OIDC.Env}
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-vikunja/",
		ClientID: `id"x`, ClientSecret: `s\"e`})
	if !ok || name != "oidc_config" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got struct {
		Auth struct {
			OpenID struct {
				Providers map[string]struct {
					Name         string `json:"name"`
					AuthURL      string `json:"authurl"`
					ClientID     string `json:"clientid"`
					ClientSecret string `json:"clientsecret"`
					Scope        string `json:"scope"`
				} `json:"providers"`
			} `json:"openid"`
		} `json:"auth"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	p := got.Auth.OpenID.Providers["authentik"]
	if p.Name != "Authentik" || p.ClientID != `id"x` || p.ClientSecret != `s\"e` || p.Scope != "openid profile email" ||
		p.AuthURL != "https://homestead.lan/apps/auth/application/o/homestead-vikunja/" {
		t.Fatalf("provider = %+v", p)
	}
	if env := a.Env(Settings{ClientSecret: "zzz"}); env["VIKUNJA_AUTH_OPENID_ENABLED"] != "true" || len(env) != 1 {
		t.Fatalf("env = %v", env)
	}
}

func TestVikunjaPlanRedirectAndWarnings(t *testing.T) {
	m := realVikunja(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("vikunja.lan", "vikunja")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RedirectURIs[0] != "https://vikunja.lan/auth/openid/authentik" || plan.SignInURL != "https://vikunja.lan/login" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	if all := strings.Join(plan.Warnings, "\n"); !strings.Contains(all, "no administrator role") {
		t.Fatalf("warnings = %s", all)
	}
}

// TestVikunjaApplyMountsConfigWithoutLeakingSecret installs from a fresh
// stack, applies, re-applies and turns sign-in off.
func TestVikunjaApplyMountsConfigWithoutLeakingSecret(t *testing.T) {
	stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, string(testCertPEM(t, "system")))
	m := realVikunja(t)
	dir := t.TempDir()
	files, err := catalog.Generate(m, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "vikunja"}},
		RouteHosts: map[string]string{"vikunja": "vikunja.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	app.m[catalog.SecretStoreName("vikunja", "jwt_secret")] = "earlier-jwt"
	app.m[catalog.SecretStoreName("vikunja", "oidc_config")] = "{}"
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("vikunja.lan", "vikunja")})}
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
	config := catalog.SecretStoreName("vikunja", "oidc_config")
	file := filepath.Join(dir, catalog.SecretsDirName, "vikunja_oidc_config")
	composeBefore, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	for _, w := range []string{"source: vikunja_oidc_config", "target: /etc/vikunja/config.yml", "VIKUNJA_SERVICE_SECRET_FILE: /run/secrets/vikunja_jwt_secret"} {
		if !strings.Contains(string(composeBefore), w) {
			t.Fatalf("compose lacks %q:\n%s", w, composeBefore)
		}
	}
	run(plan)
	secret := app.m[catalog.SecretStoreName("vikunja", "oidc_client_secret")]
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
	for _, w := range []string{"VIKUNJA_AUTH_OPENID_ENABLED", "SSL_CERT_FILE"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	for name, b := range map[string][]byte{"compose": compose, "override": override} {
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "clientsecret") {
			t.Fatalf("secret or provider JSON in %s", name)
		}
	}
	if strings.Contains(string(override), "VIKUNJA_AUTH_LOCAL_ENABLED") {
		t.Fatalf("local login touched:\n%s", override)
	}
	run(plan)
	if app.m[catalog.SecretStoreName("vikunja", "oidc_client_secret")] != secret {
		t.Fatal("the client secret changed on re-apply")
	}
	rec, _, _ := Records{DB: st.DB()}.Get("vikunja")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	run(off)
	body, _ = os.ReadFile(file)
	if string(body) != "{}" || app.m[config] != "{}" {
		t.Fatalf("off leaves provider setting: file %q store %q", body, app.m[config])
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "VIKUNJA_AUTH_OPENID_ENABLED") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

// TestVikunjaRegistrationClosed (S455): local registration is closed in the
// package; the owner and granted people are created by Authentik sign-in,
// which Vikunja does not gate on that setting (verified on 2.7.1).
func TestVikunjaRegistrationClosed(t *testing.T) {
	m := realVikunja(t)
	if got := m.Services[0].Environment["VIKUNJA_SERVICE_ENABLEREGISTRATION"]; got != "false" {
		t.Fatalf("VIKUNJA_SERVICE_ENABLEREGISTRATION = %q, want false", got)
	}
}

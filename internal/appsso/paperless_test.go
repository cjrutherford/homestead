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

func realPaperless(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("paperless-ngx")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("paperless manifest: %v", err)
	}
	return m
}

func TestPaperlessSecretFileValidation(t *testing.T) {
	cases := map[string]func(m *catalog.Manifest, e *catalog.EnvOIDC){
		"must use {{client_secret}}": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			e.SecretFile = &catalog.EnvSecretFile{Secret: "oidc_providers", Template: `{"a":"{{client_id}}"}`}
		},
		"unknown placeholder": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			e.SecretFile = &catalog.EnvSecretFile{Secret: "oidc_providers", Template: `{{client_secret}}{{nope}}`}
		},
		"declared secret of its own": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			e.SecretFile = &catalog.EnvSecretFile{Secret: "oidc_client_secret", Template: `{{client_secret}}`}
		},
		"needs an initial value": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			for i := range m.Secrets {
				m.Secrets[i].Initial = ""
			}
		},
		"must be immutable": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			for i := range m.Secrets {
				if m.Secrets[i].Name == "oidc_providers" {
					m.Secrets[i].Immutable = false
				}
			}
		},
		"is already": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			e.Set["PAPERLESS_DBHOST"] = "x"
		},
		// The client secret may not reach the stack's environment.
		"uses placeholder {{client_secret}}": func(m *catalog.Manifest, e *catalog.EnvOIDC) {
			e.Set["PAPERLESS_X"] = "{{client_secret}}"
		},
	}
	for want, mutate := range cases {
		m := realPaperless(t)
		m.Secrets = append([]catalog.SecretDecl(nil), m.Secrets...)
		e := *m.SSO.OIDC.Env
		e.Set = map[string]string{}
		for k, v := range m.SSO.OIDC.Env.Set {
			e.Set[k] = v
		}
		mutate(&m, &e)
		oidc := *m.SSO.OIDC
		oidc.Env = &e
		m.SSO.OIDC = &oidc
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestPaperlessDerivedSecretIsEscapedJSON(t *testing.T) {
	a := envOIDC{cfg: *realPaperless(t).SSO.OIDC.Env}
	name, value, ok := a.DerivedSecret(Settings{Issuer: "https://homestead.lan/apps/auth/application/o/homestead-paperless-ngx/",
		ClientID: `id"x`, ClientSecret: `s\"e`})
	if !ok || name != "oidc_providers" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	var got struct {
		OpenIDConnect struct {
			Apps []struct {
				ProviderID string `json:"provider_id"`
				ClientID   string `json:"client_id"`
				Secret     string `json:"secret"`
				Settings   struct {
					ServerURL string `json:"server_url"`
				} `json:"settings"`
			} `json:"APPS"`
		} `json:"openid_connect"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, value)
	}
	app := got.OpenIDConnect.Apps[0]
	if app.ProviderID != "authentik" || app.ClientID != `id"x` || app.Secret != `s\"e` ||
		app.Settings.ServerURL != "https://homestead.lan/apps/auth/application/o/homestead-paperless-ngx/.well-known/openid-configuration" {
		t.Fatalf("provider = %+v", app)
	}
	if _, _, ok := (envOIDC{}).DerivedSecret(Settings{}); ok {
		t.Fatal("an app without a secretfile has no derived secret")
	}
	if envOIDC := (envOIDC{cfg: *realPaperless(t).SSO.OIDC.Env}); envOIDC.Env(Settings{ClientSecret: "zzz"})["PAPERLESS_APPS"] == "" {
		t.Fatal("PAPERLESS_APPS not set")
	}
}

func TestPaperlessPlanRedirectAndGroups(t *testing.T) {
	m := realPaperless(t)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("paperless-ngx.lan", "webserver")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RedirectURIs[0] != "https://paperless-ngx.lan/accounts/oidc/authentik/login/callback/" || plan.SignInURL != "https://paperless-ngx.lan/accounts/login/" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
	}
	all := strings.Join(plan.Warnings, "\n")
	if !strings.Contains(all, "not taken from Authentik") && !strings.Contains(all, "admin rights are not taken from Authentik") {
		t.Fatalf("warnings = %s", all)
	}
}

// TestPaperlessApplyKeepsSecretOutOfTheStack applies, re-applies and
// turns sign-in off on an install made before the manifest declared the
// sign-in secrets.
func TestPaperlessApplyKeepsSecretOutOfTheStack(t *testing.T) {
	stubDocker(t)
	t.Cleanup(catalog.SetSecretChown(func(*os.File, int, int) error { return nil }))
	useSystemBundle(t, string(testCertPEM(t, "system")))
	m := realPaperless(t)
	dir := t.TempDir()
	old := m
	old.SSO.OIDC = nil
	old.Secrets = nil
	old.Database = nil
	old.Services = append([]catalog.Service(nil), old.Services...)
	old.Services[0].Entrypoint = nil
	files, err := catalog.Generate(old, catalog.Generation{
		Services:   []catalog.ServiceGeneration{{Service: "webserver"}, {Service: "db"}, {Service: "broker"}},
		RouteHosts: map[string]string{"webserver": "paperless-ngx.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	// The install's earlier secrets exist; the sign-in ones do not.
	for _, n := range []string{"db_password", "django_secret", "admin_password"} {
		app.m[catalog.SecretStoreName("paperless-ngx", n)] = "earlier-" + n
	}
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		StackNeedsSecret: true, Routes: AppRoutes(m, []docker.Route{route("paperless-ngx.lan", "webserver")})}
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
	providers := catalog.SecretStoreName("paperless-ngx", "oidc_providers")
	file := filepath.Join(dir, catalog.SecretsDirName, "webserver_oidc_providers")
	run(plan)
	secret := app.m[catalog.SecretStoreName("paperless-ngx", "oidc_client_secret")]
	if secret == "" || !strings.Contains(app.m[providers], secret) {
		t.Fatalf("client secret %q not rendered into the derived secret %q", secret, app.m[providers])
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != app.m[providers] {
		t.Fatalf("secret file = %q, %v", body, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	sup, _ := os.ReadFile(filepath.Join(dir, catalog.EntrypointFile("webserver")))
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"PAPERLESS_SOCIALACCOUNT_PROVIDERS_FILE: /run/secrets/webserver_oidc_providers", "- /init"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	if !strings.Contains(string(sup), "export PAPERLESS_SOCIALACCOUNT_PROVIDERS\n") {
		t.Fatalf("supervisor:\n%s", sup)
	}
	for _, w := range []string{"PAPERLESS_APPS: allauth.socialaccount.providers.openid_connect", "PAPERLESS_SOCIAL_AUTO_SIGNUP", "REQUESTS_CA_BUNDLE"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	for name, b := range map[string][]byte{"compose": compose, "override": override, "supervisor": sup} {
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "client_secret") || strings.Contains(string(b), `"secret"`) {
			t.Fatalf("secret or provider JSON in %s", name)
		}
	}
	if strings.Contains(string(override), "PAPERLESS_DISABLE_REGULAR_LOGIN") || strings.Contains(string(override), "PAPERLESS_REDIRECT_LOGIN_TO_SSO") {
		t.Fatalf("local login form touched:\n%s", override)
	}
	// A re-apply keeps the same client secret and rewrites the file.
	run(plan)
	if app.m[catalog.SecretStoreName("paperless-ngx", "oidc_client_secret")] != secret {
		t.Fatal("the client secret changed on re-apply")
	}
	// Off: the derived secret returns to its empty value, the variables go.
	rec, _, _ := Records{DB: st.DB()}.Get("paperless-ngx")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	run(off)
	body, _ = os.ReadFile(file)
	if string(body) != "{}" || app.m[providers] != "{}" {
		t.Fatalf("off leaves provider setting: file %q store %q", body, app.m[providers])
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "PAPERLESS_APPS") || strings.Contains(string(override), "REQUESTS_CA_BUNDLE") {
		t.Fatalf("off keeps wiring:\n%s", override)
	}
}

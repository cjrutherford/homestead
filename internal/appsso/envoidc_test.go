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
	"homestead/internal/identity"
	"homestead/internal/secret"
	"homestead/internal/store"
)

type appSecrets struct{ m map[string]string }

func (s *appSecrets) Get(n string) (string, error) {
	v, ok := s.m[n]
	if !ok {
		return "", secret.ErrNotFound
	}
	return v, nil
}
func (s *appSecrets) SetApp(n, v string) error { s.m[n] = v; return nil }
func (s *appSecrets) Delete(n string) error    { delete(s.m, n); return nil }

func envManifest() catalog.Manifest {
	return catalog.Manifest{
		Name: "envapp", Title: "Env App",
		Services: []catalog.Service{{Name: "app", Image: "envapp:1", Exposes: "web"}},
		Secrets: []catalog.SecretDecl{{Name: "oidc_client_secret",
			Consumers: []catalog.SecretConsumer{{Service: "app", Env: "OIDC_CLIENT_SECRET_FILE"}}}},
		SSO: catalog.SSODeclaration{Class: catalog.SSOOptional, Protocols: []identity.Capability{identity.CapabilityOIDC},
			OIDC: &catalog.OIDCAdapter{Adapter: catalog.AdapterEnvOIDC, Service: "app", Env: &catalog.EnvOIDC{
				RedirectPaths: []string{"/login", "/login?direct=1"}, SignInPath: "/login", Secret: "oidc_client_secret",
				Trust: "OIDC_TLS_CACERTFILE",
				Set: map[string]string{
					"OIDC_AUTH_ENABLED":      "true",
					"OIDC_CONFIGURATION_URL": "{{issuer}}.well-known/openid-configuration",
					"OIDC_CLIENT_ID":         "{{client_id}}",
					"OIDC_ADMIN_GROUP":       "{{admin_group}}",
					"OIDC_USER_GROUP":        "{{user_group}}",
				}}}},
	}
}

func TestEnvOIDCAdapter(t *testing.T) {
	m := envManifest()
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	uris := a.RedirectURIs([]string{"https://envapp.lan", "https://envapp.local"})
	if len(uris) != 4 || uris[0] != "https://envapp.lan/login" || uris[3] != "https://envapp.local/login?direct=1" {
		t.Fatalf("uris: %v", uris)
	}
	if got := a.SignInURL("https://envapp.lan"); got != "https://envapp.lan/login" {
		t.Fatalf("sign in: %s", got)
	}
	if got := a.TrustEnv(); len(got) != 1 || got["OIDC_TLS_CACERTFILE"] != CAFile {
		t.Fatalf("trust: %v", got)
	}
	g := groupNames("envapp")
	if r := a.Roles(g); !r.Mapped || !strings.Contains(r.Note, g.Admins) {
		t.Fatalf("roles: %+v", r)
	}
	env := a.(EnvSetter).Env(Settings{Issuer: "https://a.lan/application/o/x/", ClientID: "cid", Groups: g})
	if env["OIDC_CONFIGURATION_URL"] != "https://a.lan/application/o/x/.well-known/openid-configuration" ||
		env["OIDC_CLIENT_ID"] != "cid" || env["OIDC_ADMIN_GROUP"] != g.Admins || env["OIDC_USER_GROUP"] != g.Users {
		t.Fatalf("env: %v", env)
	}
	if lines := strings.Join(a.OnSettings("i", "c"), "\n"); !strings.Contains(lines, "OIDC_CLIENT_ID") {
		t.Fatalf("on: %s", lines)
	}
	if _, err := AdapterFor(catalog.OIDCAdapter{Adapter: catalog.AdapterEnvOIDC}); err == nil {
		t.Fatal("env-oidc without env accepted")
	}
}

func TestEnvOIDCApplyUsesDeclaredSecret(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	compose := "services:\n  app:\n    image: envapp:1\nsecrets:\n  app_oidc_client_secret:\n    file: ./secrets/app_oidc_client_secret\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, catalog.OverrideFileName), []byte("services:\n  app:\n    environment:\n      TZ: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{}}
	var audits []string
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: &memSecrets{m: map[string]string{}}, AppSecrets: app, StackDir: func(string) string { return dir },
		RootPEM: func() ([]byte, error) {
			return []byte("-----BEGIN CERTIFICATE-----\nroot\n-----END CERTIFICATE-----\n"), nil
		},
		Audit: func(actor, action, outcome, detail string) { audits = append(audits, action+" "+outcome+" "+detail) }}
	m := envManifest()
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, []docker.Route{route("envapp.lan", "app")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	ak := &fakeAuthentik{}
	def, err := Definition(rt, plan, m, dashboard, ak, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
	}
	// The secret was missing, so it was created, and it is Authentik's client secret.
	value := app.m[catalog.SecretStoreName("envapp", "oidc_client_secret")]
	if value == "" || ak.secret != value {
		t.Fatalf("secret %q vs reconcile %q", value, ak.secret)
	}
	if _, ok := rt.Secrets.(*memSecrets).m[SecretName("envapp")]; ok {
		t.Fatal("generated a separate sys_sso secret")
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets", "app_oidc_client_secret")); err != nil {
		t.Fatalf("secret file: %v", err)
	}
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, want := range []string{"TZ: UTC", "OIDC_AUTH_ENABLED: \"true\"", "OIDC_CLIENT_ID: homestead-envapp",
		"https://authentik.lan/application/o/homestead-envapp/.well-known/openid-configuration",
		"OIDC_ADMIN_GROUP: " + groupNames("envapp").Admins, "OIDC_TLS_CACERTFILE: /etc/homestead/ca/ca.crt"} {
		if !strings.Contains(string(override), want) {
			t.Fatalf("override lacks %q:\n%s", want, override)
		}
	}
	planJSON, _ := json.Marshal(plan)
	for name, text := range map[string]string{"override": string(override), "plan": string(planJSON), "audit": strings.Join(audits, "\n")} {
		if strings.Contains(text, value) {
			t.Fatalf("secret in %s", name)
		}
	}
	// Applying again keeps the secret.
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("again %s: %v", step.Name, err)
		}
	}
	if app.m[catalog.SecretStoreName("envapp", "oidc_client_secret")] != value {
		t.Fatal("secret churned")
	}
	// Off removes exactly the keys, keeps the owner's, and keeps the secret.
	rec, _, _ := Records{DB: st.DB()}.Get("envapp")
	in.Applied = &rec
	off, _ := BuildPlan(in, false)
	def, _ = Definition(rt, off, m, dashboard, ak, "owner")
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("off %s: %v", step.Name, err)
		}
	}
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "OIDC_") || !strings.Contains(string(override), "TZ: UTC") {
		t.Fatalf("off override:\n%s", override)
	}
	if app.m[catalog.SecretStoreName("envapp", "oidc_client_secret")] != value {
		t.Fatal("off dropped the app's secret")
	}
}

func TestEnvOIDCNeedsSecretWiring(t *testing.T) {
	dir := t.TempDir()
	rt := Runtime{AppSecrets: &appSecrets{m: map[string]string{}}}
	if _, err := ensureDeclaredSecret(rt, envManifest(), dir); err == nil || !strings.Contains(err.Error(), "installed before") {
		t.Fatalf("unwired stack: %v", err)
	}
}

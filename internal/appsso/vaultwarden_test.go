package appsso

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

func realVaultwarden(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("vaultwarden")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("vaultwarden manifest: %v", err)
	}
	// The data owner (1000:1000) makes the secret file a chown the daemon
	// does as root; tests run unprivileged, so drop it here.
	data := append([]catalog.DataPath(nil), m.Services[0].Data...)
	for i := range data {
		data[i].UID, data[i].GID = nil, nil
	}
	m.Services = append([]catalog.Service(nil), m.Services...)
	m.Services[0].Data = data
	return m
}

// TestVaultwardenPlanAndApplyExistingInstall (S414): a Vaultwarden
// installed before the manifest declared the client secret has no secrets.
// Sign-in apply wires the file-form secret (SSO_CLIENT_SECRET_FILE, read by
// Vaultwarden itself), sets the SSO_* environment without SSO_ONLY, trusts
// the appliance root through SSL_CERT_FILE, and the secret value is in no
// Compose or override file.
func TestVaultwardenPlanAndApplyExistingInstall(t *testing.T) {
	stubDocker(t)
	useSystemBundle(t, "-----BEGIN CERTIFICATE-----\nsystem\n-----END CERTIFICATE-----\n")
	m := realVaultwarden(t)
	dir := t.TempDir()
	old := m
	old.SSO.OIDC = nil
	old.Secrets = nil
	files, err := catalog.Generate(old, catalog.Generation{
		Services: []catalog.ServiceGeneration{{Service: "vaultwarden"}}, RouteHosts: map[string]string{"vaultwarden": "vaultwarden.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.WriteStack(dir, files, nil); err != nil {
		t.Fatal(err)
	}
	if catalog.SecretConsumerWired(dir, m, "oidc_client_secret") {
		t.Fatal("old stack reads as wired")
	}
	app := &appSecrets{m: map[string]string{}}
	rt, _, baselines := mealieRuntime(t, dir, app)
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		StackNeedsSecret: true, Routes: AppRoutes(m, []docker.Route{route("vaultwarden.lan", "vaultwarden")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan.AppSettings, "\n")
	if !strings.Contains(text, "stack file is rewritten to deliver the sign-in secret") {
		t.Fatalf("plan does not say the stack is rewritten:\n%s", text)
	}
	if len(plan.RedirectURIs) == 0 || plan.RedirectURIs[0] != "https://vaultwarden.lan/identity/connect/oidc-signin" {
		t.Fatalf("redirects %v", plan.RedirectURIs)
	}
	warned := strings.Join(plan.Warnings, "\n")
	if !strings.Contains(warned, "master password") {
		t.Fatalf("plan lacks the master password warning:\n%v", plan.Warnings)
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
	value := app.m[catalog.SecretStoreName("vaultwarden", "oidc_client_secret")]
	if value == "" || ak.secret != value {
		t.Fatalf("secret %q vs reconcile %q", value, ak.secret)
	}
	if *baselines == 0 {
		t.Fatal("drift not re-baselined after the stack file changed")
	}
	if fi, err := os.Stat(filepath.Join(dir, "secrets", "vaultwarden_oidc_client_secret")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret file: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	for _, w := range []string{"SSO_CLIENT_SECRET_FILE: /run/secrets/vaultwarden_oidc_client_secret", "file: ./secrets/vaultwarden_oidc_client_secret",
		"- vaultwarden_oidc_client_secret", "DOMAIN: https://vaultwarden.lan", "SIGNUPS_ALLOWED"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"SSO_ENABLED: \"true\"", "SSO_AUTHORITY: https://authentik.lan/application/o/homestead-vaultwarden/",
		"SSO_CLIENT_ID: homestead-vaultwarden", "SSO_AUTH_ONLY_NOT_SESSION: \"true\"",
		"SSL_CERT_FILE: /etc/homestead/ca/bundle.pem"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	// Local login keeps working: nothing turns it off.
	if strings.Contains(string(override), "SSO_ONLY") || strings.Contains(string(compose), "SSO_ONLY") {
		t.Fatal("SSO_ONLY is set")
	}
	for name, body := range map[string][]byte{"compose": compose, "override": override} {
		if strings.Contains(string(body), value) {
			t.Fatalf("secret value in %s", name)
		}
		if strings.Contains(string(body), "SSO_CLIENT_SECRET:") {
			t.Fatalf("plain secret variable in %s", name)
		}
	}
	if !catalog.SecretConsumerWired(dir, m, "oidc_client_secret") {
		t.Fatal("not wired after apply")
	}
	if wrote, err := catalog.WireDeclaredSecret(dir, m, "oidc_client_secret"); err != nil || wrote {
		t.Fatalf("second wiring wrote=%v err=%v", wrote, err)
	}
}

// TestVaultwardenTrustFollowsIssuer: SSL_CERT_FILE is set only for the
// appliance's own issuer; a public issuer needs nothing.
func TestVaultwardenTrustFollowsIssuer(t *testing.T) {
	m := realVaultwarden(t)
	a := envOIDC{cfg: *m.SSO.OIDC.Env}
	if got := a.TrustEnvFor("https://authentik.lan/application/o/homestead-vaultwarden/"); got["SSL_CERT_FILE"] != BundleFile {
		t.Fatalf("private issuer trust = %v", got)
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/homestead-vaultwarden/"); got != nil {
		t.Fatalf("public issuer trust = %v, want none", got)
	}
}

// TestVaultwardenGroupsClaimVerifiesEmail (S454): Authentik 2026.8 sends
// email_verified=false and Vaultwarden refuses it, so the app's groups
// claim says true, and the app asks for the groups scope.
func TestVaultwardenGroupsClaimVerifiesEmail(t *testing.T) {
	m := realVaultwarden(t)
	cfg := m.SSO.OIDC.Env
	if cfg == nil || !cfg.VerifiedEmail {
		t.Fatal("manifest does not set verifiedemail")
	}
	if !strings.Contains(cfg.Set["SSO_SCOPES"], "groups") {
		t.Fatalf("SSO_SCOPES = %q, want the groups scope", cfg.Set["SSO_SCOPES"])
	}
	g := groupNames("vaultwarden")
	rm := envOIDC{cfg: *cfg}.Roles(g)
	if !strings.Contains(claimExpression(g, rm), `claims["email_verified"] = True`) {
		t.Fatalf("claim expression lacks email_verified:\n%s", claimExpression(g, rm))
	}
	other := catalog.EnvOIDC{}
	if strings.Contains(claimExpression(g, envOIDC{cfg: other}.Roles(g)), "email_verified") {
		t.Fatal("email_verified leaks into apps that did not ask")
	}
}

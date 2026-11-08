package appsso

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

func realKomga(t *testing.T) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get("komga")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("komga manifest: %v", err)
	}
	return m
}

func testCertPEM(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestJavaTrustStore (S411): the store holds the system CAs and then the
// appliance root, and the JDK's keytool (when installed) reads it with
// the documented password.
func TestJavaTrustStore(t *testing.T) {
	useSystemBundle(t, string(testCertPEM(t, "system-a"))+string(testCertPEM(t, "system-b")))
	dir := t.TempDir()
	if err := writeBundleFile(dir, testCertPEM(t, "appliance-root"), true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bundle.jks")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle.pem")); !os.IsNotExist(err) {
		t.Fatalf("a PEM bundle was written for a Java store: %v", err)
	}
	if kt, err := exec.LookPath("keytool"); err == nil {
		out, err := exec.Command(kt, "-list", "-keystore", path, "-storepass", JavaStorePassword).CombinedOutput()
		if err != nil {
			t.Fatalf("keytool: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "Your keystore contains 3 entries") || !strings.Contains(string(out), "homestead-root, ") ||
			!strings.Contains(string(out), "trustedCertEntry") {
			t.Fatalf("keytool:\n%s", out)
		}
		if out, err := exec.Command(kt, "-list", "-keystore", path, "-storepass", "wrong").CombinedOutput(); err == nil {
			t.Fatalf("a wrong password read the store:\n%s", out)
		}
	}
}

func TestJavaTrustStoreNeedsRealCertificates(t *testing.T) {
	useSystemBundle(t, "")
	if _, err := JavaTrustStore(testCertPEM(t, "r"), time.Now()); err == nil || !strings.Contains(err.Error(), "no system CA bundle") {
		t.Fatalf("err = %v", err)
	}
	useSystemBundle(t, "-----BEGIN CERTIFICATE-----\nsystem\n-----END CERTIFICATE-----\n")
	if _, err := JavaTrustStore(testCertPEM(t, "r"), time.Now()); err == nil || !strings.Contains(err.Error(), "system CA bundle holds no readable certificate") {
		t.Fatalf("err = %v", err)
	}
	useSystemBundle(t, string(testCertPEM(t, "s")))
	if _, err := JavaTrustStore([]byte(testRootPEM), time.Now()); err == nil || !strings.Contains(err.Error(), "appliance root holds no readable certificate") {
		t.Fatalf("err = %v", err)
	}
}

func TestKomgaTrustFollowsIssuer(t *testing.T) {
	m := realKomga(t)
	a := envOIDC{cfg: *m.SSO.OIDC.Env}
	got := a.TrustEnvFor("https://homestead.lan/apps/auth/application/o/homestead-komga/")
	want := "-Djavax.net.ssl.trustStore=/etc/homestead/ca/bundle.jks -Djavax.net.ssl.trustStorePassword=changeit"
	if len(got) != 1 || got["JDK_JAVA_OPTIONS"] != want {
		t.Fatalf("private issuer trust = %v", got)
	}
	if got := a.TrustEnvFor("https://auth.example.com/application/o/homestead-komga/"); got != nil {
		t.Fatalf("public issuer trust = %v, want none", got)
	}
	if !a.CombinedBundleFor("https://homestead.lan/x/") || a.LegacyTrustEnv() != nil {
		t.Fatal("java store: bundle wanted for a private issuer, no legacy value")
	}
}

// TestKomgaPlanApplyAndLifecycle (S411): sign-in on an existing install
// wires the secret into the supervisor, sets the Spring environment, the
// Java trust options and the store for a private issuer, removes them for
// a public one and when sign-in is off, and never writes the secret.
func TestKomgaPlanApplyAndLifecycle(t *testing.T) {
	stubDocker(t)
	useSystemBundle(t, string(testCertPEM(t, "system")))
	m := realKomga(t)
	dir := t.TempDir()
	old := m
	old.SSO.OIDC = nil
	old.Secrets = old.Secrets[:1]
	old.Services = append([]catalog.Service(nil), old.Services...)
	old.Services[0].Entrypoint = nil
	files, err := catalog.Generate(old, catalog.Generation{
		Services: []catalog.ServiceGeneration{{Service: "komga"}}, RouteHosts: map[string]string{"komga": "komga.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	app := &appSecrets{m: map[string]string{catalog.SecretStoreName("komga", "recovery_password"): "recovery-value"}}
	if err := catalog.WriteStack(dir, files, app); err != nil {
		t.Fatal(err)
	}
	if catalog.SecretConsumerWired(dir, m, "oidc_client_secret") {
		t.Fatal("old stack reads as wired")
	}
	rt, st, _ := mealieRuntime(t, dir, app)
	rootPEM := testCertPEM(t, "appliance-root")
	rt.RootPEM = func() ([]byte, error) { return rootPEM, nil }
	in := Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		StackNeedsSecret: true, Routes: AppRoutes(m, []docker.Route{route("komga.lan", "komga")})}
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RedirectURIs) != 2 || plan.RedirectURIs[0] != "https://komga.lan/login/oauth2/code/authentik" ||
		plan.SignInURL != "https://komga.lan/oauth2/authorization/authentik" {
		t.Fatalf("redirects %v sign in %s", plan.RedirectURIs, plan.SignInURL)
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
	value := app.m[catalog.SecretStoreName("komga", "oidc_client_secret")]
	if value == "" {
		t.Fatal("no client secret generated")
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	sup, _ := os.ReadFile(filepath.Join(dir, catalog.EntrypointFile("komga")))
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, w := range []string{"SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTSECRET_FILE: /run/secrets/komga_oidc_client_secret",
		"entrypoint:\n      - /homestead/secret-entrypoint.sh", "- sh", "exec java ", "-jar application.jar", "source: ./" + catalog.EntrypointFile("komga"),
		"JAVA_TOOL_OPTIONS: -Xmx768m"} {
		if !strings.Contains(string(compose), w) {
			t.Fatalf("compose lacks %q:\n%s", w, compose)
		}
	}
	if !strings.Contains(string(sup), "export SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTSECRET\n") {
		t.Fatalf("supervisor:\n%s", sup)
	}
	for _, w := range []string{
		"SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_CLIENTID: homestead-komga",
		"SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_AUTHORIZATIONGRANTTYPE: authorization_code",
		"SPRING_SECURITY_OAUTH2_CLIENT_REGISTRATION_AUTHENTIK_SCOPE: openid,profile,email",
		"SPRING_SECURITY_OAUTH2_CLIENT_PROVIDER_AUTHENTIK_AUTHORIZATIONURI: https://authentik.lan/application/o/authorize/",
		"SPRING_SECURITY_OAUTH2_CLIENT_PROVIDER_AUTHENTIK_TOKENURI: https://authentik.lan/application/o/token/",
		"SPRING_SECURITY_OAUTH2_CLIENT_PROVIDER_AUTHENTIK_USERINFOURI: https://authentik.lan/application/o/userinfo/",
		"SPRING_SECURITY_OAUTH2_CLIENT_PROVIDER_AUTHENTIK_JWKSETURI: https://authentik.lan/application/o/homestead-komga/jwks/",
		"KOMGA_OAUTH2ACCOUNTCREATION: \"true\"",
		"JDK_JAVA_OPTIONS: -Djavax.net.ssl.trustStore=/etc/homestead/ca/bundle.jks -Djavax.net.ssl.trustStorePassword=changeit"} {
		if !strings.Contains(string(override), w) {
			t.Fatalf("override lacks %q:\n%s", w, override)
		}
	}
	for name, body := range map[string][]byte{"compose": compose, "override": override, "supervisor": sup} {
		if strings.Contains(string(body), value) {
			t.Fatalf("secret value in %s", name)
		}
	}
	if !catalog.SecretConsumerWired(dir, m, "oidc_client_secret") {
		t.Fatal("not wired after apply")
	}
	if wrote, err := catalog.WireDeclaredSecret(dir, m, "oidc_client_secret"); err != nil || wrote {
		t.Fatalf("second wiring wrote=%v err=%v (a supervised service must not be rewrapped)", wrote, err)
	}
	jks := filepath.Join(dir, CADir, "bundle.jks")
	if _, err := os.Stat(jks); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Public issuer: variable and store go.
	rec, _, _ := Records{DB: st.DB()}.Get("komga")
	pub := in
	pub.Applied = &rec
	pub.PublicAuthentikURL = "https://auth.example.com"
	pub.Routes = AppRoutes(m, []docker.Route{route("komga.lan", "komga", "komga.example.com")})
	plan2, err := BuildPlan(pub, true)
	if err != nil {
		t.Fatal(err)
	}
	run(plan2)
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "JDK_JAVA_OPTIONS") || !strings.Contains(string(override), "https://auth.example.com/application/o/authorize/") {
		t.Fatalf("public issuer override:\n%s", override)
	}
	if _, err := os.Stat(jks); !os.IsNotExist(err) {
		t.Fatalf("store remains on a public issuer: %v", err)
	}
	run(plan)
	if _, err := os.Stat(jks); err != nil {
		t.Fatalf("store not regenerated: %v", err)
	}
	rec, _, _ = Records{DB: st.DB()}.Get("komga")
	offIn := in
	offIn.Applied = &rec
	off, err := BuildPlan(offIn, false)
	if err != nil {
		t.Fatal(err)
	}
	run(off)
	override, _ = os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override), "JDK_JAVA_OPTIONS") || strings.Contains(string(override), "SPRING_SECURITY") || strings.Contains(string(override), "KOMGA_OAUTH2") {
		t.Fatalf("off keeps variables:\n%s", override)
	}
	if _, err := os.Stat(jks); !os.IsNotExist(err) {
		t.Fatalf("store remains after off: %v", err)
	}
}

func TestEnvOIDCTrustBundleJavaValidation(t *testing.T) {
	m := realKomga(t)
	e := *m.SSO.OIDC.Env
	e.Trust = ""
	m.SSO.OIDC.Env = &e
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "trust variable") {
		t.Fatalf("err = %v", err)
	}
}

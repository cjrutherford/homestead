package appsso

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/sso"
	"homestead/internal/store"
)

func manifest(t *testing.T, name string) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func route(host, service string, tunnel ...string) docker.Route {
	r := docker.Route{Host: docker.Hostname(host), Target: docker.Target{Host: service, Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	for _, n := range tunnel {
		r.Tunnel = append(r.Tunnel, docker.Hostname(n))
	}
	return r
}

var dashboard = sso.Spec{AuthentikURL: "https://authentik.lan", AuthorizationFlow: sso.DefaultAuthorizationFlow,
	InvalidationFlow: sso.DefaultInvalidationFlow, SigningKey: sso.DefaultSigningKey}

func inputs(t *testing.T, name string) Inputs {
	m := manifest(t, name)
	service := m.SSO.OIDC.Service
	table := []docker.Route{route(name+".lan", service), route("authentik.lan", "server"), route("other.lan", service)}
	return Inputs{Manifest: m, Installed: true, AuthentikInstalled: true, Dashboard: dashboard, DashboardOK: true,
		Routes: AppRoutes(m, table)}
}

func TestManifestsDeclareTheThreeAdapters(t *testing.T) {
	for name, adapter := range map[string]string{"immich": catalog.AdapterImmich, "nextcloud": catalog.AdapterNextcloud, "jellyfin": catalog.AdapterJellyfinSSO} {
		m := manifest(t, name)
		if m.SSO.OIDC == nil || m.SSO.OIDC.Adapter != adapter {
			t.Fatalf("%s: %+v", name, m.SSO.OIDC)
		}
	}
	p := manifest(t, "jellyfin").SSO.OIDC.Plugin
	if p == nil || p.Version != "4.0.0.4" || len(p.SHA256) != 64 || !strings.HasPrefix(p.URL, "https://github.com/9p4/jellyfin-plugin-sso/") {
		t.Fatalf("jellyfin pin: %+v", p)
	}
}

func TestPlanOnListsObjectsRedirectsAndSettings(t *testing.T) {
	in := inputs(t, "immich")
	if len(in.Routes) != 1 {
		t.Fatalf("app routes: %+v", in.Routes)
	}
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "Homestead: Immich" || p.ClientID != "homestead-immich" || p.Issuer != "https://authentik.lan/application/o/homestead-immich/" {
		t.Fatalf("names: %+v", p)
	}
	want := "https://immich.lan/auth/login,https://immich.lan/user-settings,https://immich.local/auth/login,https://immich.local/user-settings,app.immich:///oauth-callback"
	if strings.Join(p.RedirectURIs, ",") != want {
		t.Fatalf("redirects: %v", p.RedirectURIs)
	}
	if len(p.AppSettings) < 2 || !strings.Contains(strings.Join(p.AppSettings, " "), "authentik.lan") || p.Digest == "" {
		t.Fatalf("settings: %+v", p)
	}
	spec := p.Spec(dashboard)
	if spec.AppSlug != "homestead-immich" || strings.Join(spec.Origins, ",") != "https://immich.lan,https://immich.local" || spec.SubMode != "hashed_user_id" {
		t.Fatalf("spec: %+v", spec)
	}
}

func TestPlanRefusesWithoutThePreconditions(t *testing.T) {
	for _, tc := range []struct {
		edit func(*Inputs)
		want string
	}{
		{func(in *Inputs) { in.AuthentikInstalled = false }, "Install Authentik"},
		{func(in *Inputs) { in.DashboardOK = false }, "dashboard sign-in"},
		{func(in *Inputs) { in.Routes = nil }, "no address"},
		{func(in *Inputs) { in.Installed = false }, "not installed"},
	} {
		in := inputs(t, "nextcloud")
		tc.edit(&in)
		if _, err := BuildPlan(in, true); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.want, err)
		}
	}
	other := inputs(t, "immich")
	other.Manifest = manifest(t, "wordcraft")
	if _, err := BuildPlan(other, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no adapter: %v", err)
	}
}

func TestANewNameReplansTheRedirects(t *testing.T) {
	in := inputs(t, "jellyfin")
	p, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "jellyfin", Issuer: p.Issuer, RedirectURIs: p.RedirectURIs, PluginVersion: pluginState(p.Plugin), Access: p.Access}
	if st := BuildStatus(in, false); st.Replan || !st.Enabled || st.SignInURL != "https://jellyfin.lan/apps/jellyfin/sso/OID/start/authentik" {
		t.Fatalf("status: %+v", st)
	}
	same, _ := BuildPlan(in, true)
	if !strings.Contains(same.Changes[0], "Nothing changes") {
		t.Fatalf("same: %v", same.Changes)
	}
	// The app gains a public name through the tunnel.
	in.Routes = []docker.Route{route("jellyfin.lan", "jellyfin", "media.example.com")}
	if st := BuildStatus(in, false); !st.Replan {
		t.Fatalf("no replan: %+v", st)
	}
	re, _ := BuildPlan(in, true)
	if !strings.Contains(strings.Join(re.Changes, "\n"), "Add redirect URI https://media.example.com/sso/OID/redirect/authentik") || re.Digest == same.Digest {
		t.Fatalf("replan: %v", re.Changes)
	}
}

func TestPlanOff(t *testing.T) {
	in := inputs(t, "immich")
	p, _ := BuildPlan(in, false)
	if !strings.Contains(p.Changes[0], "already off") {
		t.Fatalf("off when off: %v", p.Changes)
	}
	in.Applied = &Record{Package: "immich", Issuer: "https://authentik.lan/application/o/homestead-immich/", RedirectURIs: []string{"https://immich.lan/auth/login"}}
	// Turning off works even when the preconditions no longer hold.
	in.AuthentikInstalled = false
	p, err := BuildPlan(in, false)
	if err != nil || p.Enabled || !strings.Contains(strings.Join(p.Changes, " "), "Delete the application") {
		t.Fatalf("off: %+v %v", p, err)
	}
}

// ---- adapters ----

type call struct {
	container, user, stdin string
	args                   []string
}

type fakeDocker struct {
	mu       sync.Mutex
	calls    []call
	restarts []string
	copies   []string
	fail     func(call) (string, error)
}

func stubDocker(t *testing.T) *fakeDocker {
	f := &fakeDocker{}
	oldExec, oldRestart, oldCopy, oldUp, oldTries := execIn, restart, copyInto, up, readyTries
	execIn = func(_ context.Context, c, user, stdin string, args ...string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		cl := call{c, user, stdin, args}
		f.calls = append(f.calls, cl)
		if f.fail != nil {
			return f.fail(cl)
		}
		return "", nil
	}
	restart = func(_ context.Context, c string) error { f.restarts = append(f.restarts, c); return nil }
	copyInto = func(_ context.Context, src, c, dst string) error {
		entries, _ := os.ReadDir(src)
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		f.copies = append(f.copies, filepath.Base(src)+"->"+c+":"+dst+" ["+strings.Join(names, ",")+"]")
		return nil
	}
	up = func(context.Context, string, string) error { return nil }
	readyTries = 1
	t.Cleanup(func() { execIn, restart, copyInto, up, readyTries = oldExec, oldRestart, oldCopy, oldUp, oldTries })
	return f
}

const clientSecret = "client-secret-never-in-argv"

func (f *fakeDocker) argvHolds(s string) bool {
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.args, " "), s) {
			return true
		}
	}
	return false
}

func TestImmichAdapterWritesOAuthThroughStdin(t *testing.T) {
	f := stubDocker(t)
	m := manifest(t, "immich")
	a, _ := AdapterFor(*m.SSO.OIDC)
	target := Target{Manifest: m, Container: "immich-server"}
	if err := a.Enable(context.Background(), target, Settings{Issuer: "https://authentik.lan/application/o/homestead-immich/", ClientID: "homestead-immich", ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	if c.container != "immich-database" || c.args[0] != "psql" || !strings.Contains(c.stdin, `"clientSecret":"`+clientSecret+`"`) ||
		!strings.Contains(c.stdin, `"enabled":true`) || !strings.Contains(c.stdin, "system_metadata") {
		t.Fatalf("immich call: %+v", c)
	}
	if f.argvHolds(clientSecret) || strings.Join(f.restarts, ",") != "immich-server" {
		t.Fatalf("argv or restart: %+v %v", f.calls, f.restarts)
	}
	if err := a.Disable(context.Background(), target); err != nil || !strings.Contains(f.calls[1].stdin, "'{oauth,enabled}', 'false'") {
		t.Fatalf("disable: %v %+v", err, f.calls)
	}
	if a.TrustEnv()["NODE_EXTRA_CA_CERTS"] != CAFile {
		t.Fatalf("trust: %v", a.TrustEnv())
	}
}

func TestNextcloudAdapterUsesOccAndStdinForTheSecret(t *testing.T) {
	f := stubDocker(t)
	m := manifest(t, "nextcloud")
	a, _ := AdapterFor(*m.SSO.OIDC)
	// user_oidc is not installed yet: enable fails, install runs.
	f.fail = func(c call) (string, error) {
		if len(c.args) > 4 && c.args[3] == "app:enable" && c.args[4] == "user_oidc" {
			return "user_oidc not found", errors.New("exit 1")
		}
		return "", nil
	}
	target := Target{Manifest: m, Container: "nextcloud-nextcloud"}
	if err := a.Enable(context.Background(), target, Settings{Issuer: "https://authentik.lan/application/o/homestead-nextcloud/", ClientID: "homestead-nextcloud", ClientSecret: clientSecret}); err != nil {
		t.Fatal(err)
	}
	var verbs []string
	for _, c := range f.calls {
		if c.user != "www-data" || c.container != "nextcloud-nextcloud" {
			t.Fatalf("call: %+v", c)
		}
		if c.args[0] == "php" {
			verbs = append(verbs, c.args[3])
		} else if strings.HasPrefix(c.args[len(c.args)-1], nextcloudAppDir+"/") {
			verbs = append(verbs, "write")
		} else {
			verbs = append(verbs, "provider")
			if c.stdin != clientSecret+"\n" || !strings.Contains(strings.Join(c.args, " "), "https://authentik.lan/application/o/homestead-nextcloud/.well-known/openid-configuration") {
				t.Fatalf("provider: %+v", c)
			}
		}
	}
	if strings.Join(verbs, ",") != "status,app:enable,app:install,write,write,write,app:enable,security:certificates:import,config:app:set,config:system:set,config:system:set,app:disable,provider" {
		t.Fatalf("verbs: %v", verbs)
	}
	if f.argvHolds(clientSecret) {
		t.Fatal("secret in argv")
	}
	// S500: the admin app is written whole and enabled; its listener keeps
	// the real admin group in step with the claim's admin alias.
	var written string
	for _, c := range f.calls {
		written += c.stdin
	}
	for _, want := range []string{"<id>homestead_sso</id>", "UserLoggedInEvent", "getBackendClassName() !== 'user_oidc'", "adminGroupIds", "removeUser"} {
		if !strings.Contains(written, want) {
			t.Fatalf("admin app lacks %q", want)
		}
	}
	if !strings.Contains(fmt.Sprint(f.calls), "app:enable homestead_sso") {
		t.Fatalf("admin app not enabled: %+v", f.calls)
	}
	if !strings.Contains(fmt.Sprint(f.calls), "app:disable firstrunwizard") {
		t.Fatalf("welcome wizard stays on: %+v", f.calls)
	}
	if !strings.Contains(fmt.Sprint(f.calls), "overwriteprotocol --value=https") {
		t.Fatalf("overwriteprotocol: %+v", f.calls)
	}
	// A Nextcloud that has not finished first-run setup is refused before
	// any app command runs.
	f.calls = nil
	f.fail = func(c call) (string, error) {
		if len(c.args) > 3 && c.args[3] == "status" {
			return `{"installed":false,"version":"35.0.1.1"}`, nil
		}
		return "", nil
	}
	if err := a.Enable(context.Background(), target, Settings{ClientSecret: clientSecret}); !errors.Is(err, ErrNextcloudNotSetUp) || len(f.calls) != 1 {
		t.Fatalf("not set up: %v %+v", err, f.calls)
	}
	f.calls, f.fail = nil, nil
	if err := a.Disable(context.Background(), target); err != nil || f.calls[0].args[3] != "user_oidc:provider:delete" || f.calls[1].args[3] != "app:disable" {
		t.Fatalf("disable: %v %+v", err, f.calls)
	}
}

func pluginZip(t *testing.T, files map[string]string) ([]byte, string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestJellyfinAdapterInstallsOnlyThePinnedBuild(t *testing.T) {
	f := stubDocker(t)
	data, sum := pluginZip(t, map[string]string{"meta.json": "{}", "SSO-Auth.dll": "x"})
	oldDownload := download
	t.Cleanup(func() { download = oldDownload })
	download = func(context.Context, string) ([]byte, error) { return data, nil }
	m := manifest(t, "jellyfin")
	pin := *m.SSO.OIDC.Plugin
	target := Target{Manifest: m, Container: "jellyfin-jellyfin"}
	s := Settings{Issuer: "https://authentik.lan/application/o/homestead-jellyfin/", ClientID: "homestead-jellyfin", ClientSecret: clientSecret}

	// The real pin does not match this archive: refused before anything runs.
	a := jellyfin{plugin: pin}
	if err := a.Enable(context.Background(), target, s); !errors.Is(err, ErrChecksum) || len(f.calls) != 0 || len(f.copies) != 0 {
		t.Fatalf("other build: %v %+v", err, f.calls)
	}
	pin.SHA256 = sum
	a = jellyfin{plugin: pin}
	if err := a.Enable(context.Background(), target, s); err != nil {
		t.Fatal(err)
	}
	if len(f.copies) != 1 || f.copies[0] != "SSO-Authentication_4.0.0.4->jellyfin-jellyfin:/config/plugins/ [SSO-Auth.dll,meta.json]" {
		t.Fatalf("copies: %v", f.copies)
	}
	conf := f.calls[len(f.calls)-1]
	if !strings.Contains(conf.stdin, "<OidSecret>"+clientSecret+"</OidSecret>") || !strings.Contains(conf.stdin, "<string>authentik</string>") ||
		!strings.Contains(conf.stdin, "<OidEndpoint>https://authentik.lan/application/o/homestead-jellyfin/</OidEndpoint>") ||
		!strings.Contains(strings.Join(strings.Fields(conf.stdin), ""), "<value><PluginConfiguration><OidEndpoint>") || strings.Contains(conf.stdin, "<OidConfig>") ||
		conf.args[len(conf.args)-1] != jellyfinPluginConf || f.argvHolds(clientSecret) {
		t.Fatalf("config: %+v", conf)
	}
	if err := a.Disable(context.Background(), target); err != nil || strings.Contains(f.calls[len(f.calls)-1].stdin, "OidSecret") {
		t.Fatalf("disable: %v", err)
	}
	// An archive with a path in it is refused even when the sum matches.
	bad, badSum := pluginZip(t, map[string]string{"../evil.dll": "x"})
	download = func(context.Context, string) ([]byte, error) { return bad, nil }
	pin.SHA256 = badSum
	if err := (jellyfin{plugin: pin}).Enable(context.Background(), target, s); err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("zip slip: %v", err)
	}
}

// ---- apply ----

type memSecrets struct{ m map[string]string }

func (s *memSecrets) SetSystem(n, v string) error { s.m[n] = v; return nil }
func (s *memSecrets) Get(n string) (string, error) {
	v, ok := s.m[n]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (s *memSecrets) Delete(n string) error { delete(s.m, n); return nil }

type fakeAuthentik struct {
	specs       []sso.Spec
	removedSpec sso.Spec
	secret      string
	removed     []string
}

func (f *fakeAuthentik) Version(context.Context) (string, error) { return "2026.8.3", nil }
func (f *fakeAuthentik) Reconcile(_ context.Context, spec sso.Spec, secret string, step func(string)) error {
	f.specs, f.secret = append(f.specs, spec), secret
	step("created the provider")
	return nil
}
func (f *fakeAuthentik) Remove(_ context.Context, spec sso.Spec, step func(string)) error {
	f.removed, f.removedSpec = append(f.removed, spec.ProviderName), spec
	step("deleted the provider")
	return nil
}

func TestApplyOnThenOff(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, catalog.OverrideFileName), []byte("services:\n  server:\n    environment:\n      TZ: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := &memSecrets{m: map[string]string{}}
	var audits []string
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: secrets, StackDir: func(string) string { return dir },
		RootPEM: func() ([]byte, error) {
			return []byte("-----BEGIN CERTIFICATE-----\nroot\n-----END CERTIFICATE-----\n"), nil
		},
		Audit: func(actor, action, outcome, detail string) { audits = append(audits, action+" "+outcome+" "+detail) }}
	in := inputs(t, "immich")
	plan, _ := BuildPlan(in, true)
	ak := &fakeAuthentik{}
	def, err := Definition(rt, plan, in.Manifest, dashboard, ak, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", step.Name, err)
		}
	}
	sealed := secrets.m[SecretName("immich")]
	if sealed == "" || ak.secret != sealed || ak.specs[0].ClientID != "homestead-immich" || SecretName("immich") != "sys_sso_app_immich_secret" {
		t.Fatalf("secret/spec: %q %+v", sealed, ak.specs)
	}
	override, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	for _, want := range []string{"TZ: UTC", "authentik.lan:host-gateway", "./homestead-ca:/etc/homestead/ca:ro", "NODE_EXTRA_CA_CERTS: /etc/homestead/ca/ca.crt"} {
		if !strings.Contains(string(override), want) {
			t.Fatalf("override lacks %q:\n%s", want, override)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, CADir, "ca.crt")); err != nil {
		t.Fatal(err)
	}
	rec, ok, _ := Records{DB: st.DB()}.Get("immich")
	if !ok || rec.ClientID != "homestead-immich" || len(rec.RedirectURIs) != 5 || rec.Actor != "owner" {
		t.Fatalf("record: %+v", rec)
	}
	// Applying again keeps the same secret.
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("again %s: %v", step.Name, err)
		}
	}
	if secrets.m[SecretName("immich")] != sealed {
		t.Fatal("secret churned")
	}
	override2, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if string(override2) != string(override) {
		t.Fatalf("wiring duplicated:\n%s", override2)
	}

	// Publishing moves the app to Authentik's public address (ADR-047):
	// the host entry follows the issuer, the old one goes.
	in.Applied = &rec
	// An unpublished app with a name of its own keeps the local address
	// even when the public one is answered (only path apps move, S408).
	unpub := in
	unpub.PublicAuthentikURL = "https://homestead.example.com/apps/auth"
	if p, _ := BuildPlan(unpub, true); strings.Contains(p.Issuer, "example.com") {
		t.Fatalf("unpublished host app issuer = %s", p.Issuer)
	}
	pub := in
	pub.Routes = append([]docker.Route(nil), in.Routes...)
	pub.Routes[0].Tunnel = []docker.Hostname{"immich.example.com"}
	pub.PublicAuthentikURL = "https://homestead.example.com/apps/auth"
	moved, _ := BuildPlan(pub, true)
	def, _ = Definition(rt, moved, in.Manifest, dashboard, ak, "owner")
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("publish %s: %v", step.Name, err)
		}
	}
	overridePub, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if !strings.Contains(string(overridePub), "homestead.example.com:host-gateway") || strings.Contains(string(overridePub), "authentik.lan:host-gateway") ||
		strings.Count(string(overridePub), "NODE_EXTRA_CA_CERTS") != 1 {
		t.Fatalf("published override:\n%s", overridePub)
	}
	rec, _, _ = Records{DB: st.DB()}.Get("immich")
	if rec.Issuer != "https://homestead.example.com/apps/auth/application/o/homestead-immich/" {
		t.Fatalf("published record issuer = %s", rec.Issuer)
	}
	in.Applied = &rec
	off, _ := BuildPlan(in, false)
	def, _ = Definition(rt, off, in.Manifest, dashboard, ak, "owner")
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("off %s: %v", step.Name, err)
		}
	}
	if _, ok, _ := (Records{DB: st.DB()}).Get("immich"); ok || len(ak.removed) != 1 || secrets.m[SecretName("immich")] != "" {
		t.Fatalf("after off: %v %v", ok, ak.removed)
	}
	override3, _ := os.ReadFile(filepath.Join(dir, catalog.OverrideFileName))
	if strings.Contains(string(override3), "host-gateway") || strings.Contains(string(override3), "NODE_EXTRA") || !strings.Contains(string(override3), "TZ: UTC") {
		t.Fatalf("off override:\n%s", override3)
	}
	if _, err := os.Stat(filepath.Join(dir, CADir)); !os.IsNotExist(err) {
		t.Fatalf("ca dir left: %v", err)
	}
	for _, a := range audits {
		if strings.Contains(a, sealed) {
			t.Fatalf("audit holds the secret: %s", a)
		}
	}
	if !strings.Contains(strings.Join(audits, "\n"), "app.sign-in.on ok") || !strings.Contains(strings.Join(audits, "\n"), "app.sign-in.off ok") {
		t.Fatalf("audits: %v", audits)
	}
}

// S312: a Nextcloud installing itself is waited for, bounded, before
// sign-in changes anything; one its owner must set up is refused at once
// with guidance that names its address.
func TestNextcloudWaitsForItsOwnInstall(t *testing.T) {
	f := stubDocker(t)
	oldPause, oldInstall := readyPause, installTries
	readyPause, installTries = time.Millisecond, 5
	t.Cleanup(func() { readyPause, installTries = oldPause, oldInstall })
	m := manifest(t, "nextcloud")
	a, _ := AdapterFor(*m.SSO.OIDC)
	waiter, ok := a.(SetUpWaiter)
	if !ok {
		t.Fatal("the Nextcloud adapter does not wait for set-up")
	}
	target := Target{Manifest: m, Container: "nextcloud-nextcloud", Address: "https://nextcloud.lan"}
	statuses := 0
	installedAfter := 3
	auto := true
	f.fail = func(c call) (string, error) {
		switch {
		case c.args[0] == "printenv":
			if auto {
				return "admin\n", nil
			}
			return "", errors.New("exit 1")
		case len(c.args) > 3 && c.args[3] == "status":
			statuses++
			if statuses >= installedAfter {
				return `{"installed":true}`, nil
			}
			return `{"installed":false}`, nil
		}
		return "", nil
	}
	// Installing itself: waited for until it reports installed.
	if err := waiter.WaitSetUp(context.Background(), target); err != nil || statuses != 3 {
		t.Fatalf("wait: %v after %d status calls", err, statuses)
	}
	// Still installing at the bound: guided failure, not a bare error.
	statuses, installedAfter = 0, 100
	err := waiter.WaitSetUp(context.Background(), target)
	var notSetUp *NotSetUpError
	if !errors.As(err, &notSetUp) || !notSetUp.Waited || !errors.Is(err, ErrNextcloudNotSetUp) || statuses != 5 {
		t.Fatalf("bound: %v after %d", err, statuses)
	}
	for _, want := range []string{"Nextcloud " + NotSetUpPhrase, "https://nextcloud.lan", "Apply sign-in again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("bound text lacks %q: %v", want, err)
		}
	}
	// A manual install (no NEXTCLOUD_ADMIN_USER) is refused at once.
	statuses, auto = 0, false
	err = waiter.WaitSetUp(context.Background(), target)
	if !errors.As(err, &notSetUp) || notSetUp.Waited || statuses != 1 ||
		!strings.Contains(err.Error(), "create its admin account") || !strings.Contains(err.Error(), "https://nextcloud.lan") {
		t.Fatalf("manual: %v after %d", err, statuses)
	}
}

// S312: the sign-in operation checks set-up before it changes anything,
// and fails with the guided text.
func TestApplyStopsBeforeChangesWhenNotSetUp(t *testing.T) {
	f := stubDocker(t)
	f.fail = func(c call) (string, error) {
		if c.args[0] == "printenv" {
			return "", errors.New("exit 1")
		}
		if len(c.args) > 3 && c.args[3] == "status" {
			return `{"installed":false}`, nil
		}
		return "", nil
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	secrets := &memSecrets{m: map[string]string{}}
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: secrets, StackDir: func(string) string { return t.TempDir() },
		RootPEM: func() ([]byte, error) { return []byte("root"), nil }}
	in := inputs(t, "nextcloud")
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	ak := &fakeAuthentik{}
	def, err := Definition(rt, plan, in.Manifest, dashboard, ak, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Steps) < 2 || def.Steps[1].Name != "app-set-up" {
		t.Fatalf("steps: %+v", def.Steps)
	}
	var runErr error
	for _, step := range def.Steps {
		if runErr = step.Run(context.Background()); runErr != nil {
			break
		}
	}
	if runErr == nil || !strings.Contains(runErr.Error(), NotSetUpPhrase) || !strings.Contains(runErr.Error(), "https://") ||
		len(ak.specs) != 0 || len(secrets.m) != 0 {
		t.Fatalf("run: %v; authentik %+v; secrets %v", runErr, ak.specs, secrets.m)
	}
}

// S318: an app Homestead sets up itself is set up before sign-in
// changes anything, and a failure stops the operation there.
func TestFirstRunBeforeSignIn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	secrets := &memSecrets{m: map[string]string{}}
	var ran []string
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: secrets, StackDir: func(string) string { return t.TempDir() },
		RootPEM: func() ([]byte, error) { return []byte("root"), nil },
		FirstRun: func(_ context.Context, m catalog.Manifest) error {
			ran = append(ran, m.Name)
			return errors.New("Immich did not answer in time")
		}}
	in := inputs(t, "immich")
	plan, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	ak := &fakeAuthentik{}
	def, err := Definition(rt, plan, in.Manifest, dashboard, ak, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Steps) < 2 || def.Steps[1].Name != "app-first-run" {
		t.Fatalf("steps: %+v", def.Steps)
	}
	var runErr error
	for _, step := range def.Steps {
		if runErr = step.Run(context.Background()); runErr != nil {
			break
		}
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "did not answer") || len(ran) != 1 || len(ak.specs) != 0 || len(secrets.m) != 0 {
		t.Fatalf("run: %v; ran %v; authentik %+v; secrets %v", runErr, ran, ak.specs, secrets.m)
	}
}

// S316: the Doctor asks whether an app finished its own first run,
// read-only, and only Nextcloud's can be detected.
func TestSetUpState(t *testing.T) {
	f := stubDocker(t)
	nc := manifest(t, "nextcloud")
	f.fail = func(c call) (string, error) { return `{"installed":true,"version":"30"}`, nil }
	if setUp, known, err := SetUpState(context.Background(), nc); err != nil || !known || !setUp {
		t.Fatalf("installed: %v %v %v", setUp, known, err)
	}
	f.fail = func(c call) (string, error) { return `{"installed":false}`, nil }
	if setUp, known, err := SetUpState(context.Background(), nc); err != nil || !known || setUp {
		t.Fatalf("not installed: %v %v %v", setUp, known, err)
	}
	f.fail = func(c call) (string, error) { return "", errors.New("container is starting") }
	if _, known, err := SetUpState(context.Background(), nc); err == nil || known {
		t.Fatalf("starting: known=%v err=%v", known, err)
	}
	if _, known, err := SetUpState(context.Background(), manifest(t, "immich")); known || err != nil {
		t.Fatalf("immich cannot be detected: known=%v err=%v", known, err)
	}
	for _, c := range f.calls {
		if strings.Join(c.args, " ") != "php occ --no-interaction status --output=json" {
			t.Fatalf("not read-only: %v", c.args)
		}
	}
}

func TestJellyfinAsksEachScopeOnce(t *testing.T) {
	doc, err := jellyfinConfig(&Settings{Issuer: "https://a/", ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Scopes []string `xml:"OidConfigs>item>value>PluginConfiguration>OidScopes>string"`
	}
	if err := xml.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatal(err)
	}
	// The plugin itself requests "openid profile".
	if strings.Join(got.Scopes, " ") != "email groups" {
		t.Fatalf("scopes %q:\n%s", got.Scopes, doc)
	}
}

func TestSettingsFromBeforeReplan(t *testing.T) {
	in := inputs(t, "jellyfin")
	p, _ := BuildPlan(in, true)
	in.Applied = &Record{Package: "jellyfin", Issuer: p.Issuer, RedirectURIs: p.RedirectURIs, PluginVersion: p.Plugin.Version, Access: p.Access}
	if st := BuildStatus(in, false); !st.Replan {
		t.Fatalf("an old record should replan: %+v", st)
	}
	again, _ := BuildPlan(in, true)
	if !strings.Contains(strings.Join(again.Changes, "\n"), "Sign in with Authentik button are rewritten") || strings.Contains(strings.Join(again.Changes, "\n"), "replaces") {
		t.Fatalf("changes: %v", again.Changes)
	}
}

// S400: Jellyfin serves under its app path (BaseUrl) on a name of its own
// and on the appliance's names alike; the sign-in URL carries the path
// once, never twice.
func TestJellyfinSignInURLCarriesItsPath(t *testing.T) {
	m := manifest(t, "jellyfin")
	a, err := AdapterFor(*m.SSO.OIDC)
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]string{
		"https://jellyfin.lan":                "https://jellyfin.lan/apps/jellyfin/sso/OID/start/authentik",
		"https://homestead.lan/apps/jellyfin": "https://homestead.lan/apps/jellyfin/sso/OID/start/authentik",
	} {
		if got := signInURL(a, m, origin); got != want {
			t.Errorf("%s: %s", origin, got)
		}
	}
	n := manifest(t, "nextcloud")
	na, _ := AdapterFor(*n.SSO.OIDC)
	if got := signInURL(na, n, "https://nextcloud.lan"); got != "https://nextcloud.lan/login" {
		t.Errorf("nextcloud: %s", got)
	}
}

// S456: applying sign-in puts people's recorded grants back into the app's
// Authentik groups; turning it off does not.
func TestApplyRestoresPeoplesAccess(t *testing.T) {
	stubDocker(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, catalog.OverrideFileName), []byte("services:\n  server:\n    environment:\n      TZ: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var restored []string
	var order []string
	failRestore := false
	rt := Runtime{Records: Records{DB: st.DB()}, Secrets: &memSecrets{m: map[string]string{}}, StackDir: func(string) string { return dir },
		RootPEM: func() ([]byte, error) {
			return []byte("-----BEGIN CERTIFICATE-----\nroot\n-----END CERTIFICATE-----\n"), nil
		},
		RestoreAccess: func(_ context.Context, app string) (int, error) {
			order = append(order, "restore")
			if failRestore {
				return 0, errors.New("authentik refused")
			}
			restored = append(restored, app)
			return 2, nil
		}}
	in := inputs(t, "immich")
	run := func(on bool) error {
		plan, _ := BuildPlan(in, on)
		def, err := Definition(rt, plan, in.Manifest, dashboard, &fakeAuthentik{}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range def.Steps {
			if err := step.Run(context.Background()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := run(true); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0] != "immich" {
		t.Fatalf("restored = %v", restored)
	}
	rec, _, _ := Records{DB: st.DB()}.Get("immich")
	in.Applied = &rec
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 {
		t.Fatalf("turning off restored people: %v", order)
	}
	failRestore = true
	in.Applied = nil
	if err := run(true); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a failed restore must fail the apply: %v", err)
	}
}

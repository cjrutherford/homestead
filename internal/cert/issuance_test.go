package cert

import (
	"context"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

const issuanceDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

// issuanceRig is a full first-issuance setup: temp store and stack, a
// scripted fake CA (counting requests), a fake DNS provider, saved TLS
// strategy and domain setup, and one edge route using automatic TLS.
type issuanceRig struct {
	t         *testing.T
	issuing   *Issuing
	store     *store.Store
	secrets   *secret.Store
	wizards   *wizard.Service
	provider  *scriptDNSProvider
	double    *acmeDouble
	caCalls   *atomic.Int64
	stackDir  string
	reloads   int
	reloadErr error
	routes    []docker.Route
}

func newIssuanceRig(t *testing.T, directory string) *issuanceRig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := secret.New(st, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	double := &acmeDouble{t: t, usedNonce: map[string]bool{}}
	calls := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		double.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	double.base = srv.URL
	provider := &scriptDNSProvider{}
	registry := dns.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rig := &issuanceRig{
		t: t, store: st, secrets: secrets, wizards: wizard.New(st), provider: provider,
		double: double, caCalls: calls, stackDir: filepath.Join(dir, "caddy"),
		routes: []docker.Route{{
			Host: "www.example.com", Target: docker.Target{Host: "www", Port: 80},
			Exposure: docker.ExposureLocal, TLS: docker.TLSAuto,
		}},
	}
	rig.issuing = &Issuing{
		Store: st, Wizards: rig.wizards, Secrets: secrets, Providers: registry,
		StackDir: rig.stackDir,
		NewClient: func(string) (*Client, error) {
			return NewClient(srv.URL + "/directory")
		},
		Probe: func(_ context.Context, name string, _ dns.RecordType) ([]string, error) {
			for _, r := range provider.records {
				full := r.Name + ".example.com"
				if full == name && r.Type == dns.RecordTXT {
					return []string{r.Value}, nil
				}
			}
			return nil, fmt.Errorf("no such host %s", name)
		},
		Interval: time.Millisecond,
		Routes:   func() ([]docker.Route, error) { return rig.routes, nil },
		Reload: func(context.Context) error {
			rig.reloads++
			return rig.reloadErr
		},
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	rig.saveStrategy(directory, []string{"www.example.com"})
	rig.saveDomain("script", "example.com")
	// The edge has applied the route: its snippet exists.
	rig.writeSnippet(rig.routes[0])
	return rig
}

func (r *issuanceRig) saveStrategy(directory string, names []string) {
	r.t.Helper()
	set := Set{Certificates: []Certificate{{
		Name: "www", DNSNames: names, State: StatePending, Renewal: Renewal{Auto: true},
		Issuer: Issuer{Name: "le", Type: IssuerACME, Directory: directory,
			Challenge: ChallengeDNS01, DNSProvider: "script"},
	}}}
	raw, _ := json.Marshal(map[string]any{"certificates": set.Certificates})
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if _, err := r.wizards.Put(TLSWizardID, "strategy", payload); err != nil {
		r.t.Fatalf("save strategy: %v", err)
	}
}

func (r *issuanceRig) saveDomain(provider, zone string) {
	r.t.Helper()
	if _, err := r.wizards.Put("domain", "zone", map[string]any{"provider": provider, "zone": zone}); err != nil {
		r.t.Fatalf("save domain: %v", err)
	}
}

func (r *issuanceRig) writeSnippet(route docker.Route) {
	r.t.Helper()
	content, err := docker.RenderRouteIn(r.stackDir, route)
	if err != nil {
		r.t.Fatalf("render: %v", err)
	}
	dir := filepath.Join(r.stackDir, "routes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, docker.RouteFilename(route.Host)), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *issuanceRig) approve() IssuancePlan {
	r.t.Helper()
	plan, err := r.issuing.Plan("www")
	if err != nil {
		r.t.Fatalf("Plan: %v", err)
	}
	if _, err := r.issuing.Approve("www", plan.Digest, "owner", true, false); err != nil {
		r.t.Fatalf("Approve: %v", err)
	}
	return plan
}

// run executes the definition's steps in order like the runner does.
func (r *issuanceRig) run(def operation.Definition) error {
	r.t.Helper()
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
	}
	return nil
}

func TestPlanContentsAndNoCAContact(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	plan, err := rig.issuing.Plan("www")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Ready || len(plan.Blockers) != 0 {
		t.Fatalf("plan not ready: %+v", plan.Blockers)
	}
	if plan.Certificate != "www" || len(plan.Names) != 1 || plan.Names[0] != "www.example.com" ||
		plan.Provider != "script" || plan.Challenge != "dns-01" || plan.Zone != "example.com" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Directory != issuanceDirectory || plan.DirectoryClass != DirectoryStaging {
		t.Fatalf("directory = %q class %q", plan.Directory, plan.DirectoryClass)
	}
	if plan.Account.State != "new" || !strings.HasPrefix(plan.Account.Secret, "sys_acme_account_") {
		t.Fatalf("account = %+v", plan.Account)
	}
	if plan.Placement.KeyMode != "0600" || !strings.HasSuffix(plan.Placement.KeyFile, "routes/certs/www/privkey.pem") {
		t.Fatalf("placement = %+v", plan.Placement)
	}
	if len(plan.ServedRoutes) != 1 || plan.ServedRoutes[0] != "www.example.com" {
		t.Fatalf("served routes = %v", plan.ServedRoutes)
	}
	if plan.Terms == "" || plan.Digest == "" || plan.Approved {
		t.Fatalf("terms/digest/approved = %q %q %v", plan.Terms, plan.Digest, plan.Approved)
	}
	// Planning, approving, and refused applies never touch the CA, and
	// planning generates no account key.
	if _, err := rig.issuing.Definition("www"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("apply before approval = %v, want ErrNotApproved", err)
	}
	rig.approve()
	if rig.caCalls.Load() != 0 {
		t.Fatalf("CA was contacted %d time(s) before the run", rig.caCalls.Load())
	}
	if rig.issuing.accountExists(issuanceDirectory) {
		t.Fatal("account key generated before approval ran")
	}
	// The plan is deterministic, and approval is visible on it.
	again, _ := rig.issuing.Plan("www")
	if again.Digest != plan.Digest || !again.Approved {
		t.Fatalf("digest %q vs %q approved=%v", again.Digest, plan.Digest, again.Approved)
	}
}

func TestPlanBlockersAndEligibility(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.saveDomain("script", "other.test")
	plan, _ := rig.issuing.Plan("www")
	if plan.Ready || !strings.Contains(strings.Join(plan.Blockers, ";"), "outside the zone") {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
	rig.saveDomain("cloudflare", "example.com")
	plan, _ = rig.issuing.Plan("www")
	if plan.Ready || !strings.Contains(strings.Join(plan.Blockers, ";"), `uses provider "cloudflare"`) {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
	rig.saveDomain("script", "example.com")
	rig.wizards.Delete("domain")
	plan, _ = rig.issuing.Plan("www")
	if plan.Ready || !strings.Contains(strings.Join(plan.Blockers, ";"), "no domain setup") {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, false); err == nil ||
		!strings.Contains(err.Error(), "not ready") {
		t.Fatalf("approved an unready plan: %v", err)
	}
	if _, err := rig.issuing.Plan("nope"); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("unknown certificate = %v", err)
	}
	// A missing DNS credential blocks.
	rig.saveDomain("script", "example.com")
	rig.provider.fields = []dns.CredentialField{{Name: "api_token", Label: "Token", Secret: true}}
	plan, _ = rig.issuing.Plan("www")
	if plan.Ready || !strings.Contains(strings.Join(plan.Blockers, ";"), `missing DNS credential "api_token"`) {
		t.Fatalf("blockers = %v", plan.Blockers)
	}
}

func TestApproveRefusals(t *testing.T) {
	rig := newIssuanceRig(t, "https://acme-v02.api.letsencrypt.org/directory")
	plan, _ := rig.issuing.Plan("www")
	if plan.DirectoryClass != DirectoryProduction {
		t.Fatalf("class = %q", plan.DirectoryClass)
	}
	for name, call := range map[string]func() error{
		"no actor": func() error { _, err := rig.issuing.Approve("www", plan.Digest, "", true, true); return err },
		"stale digest": func() error {
			_, err := rig.issuing.Approve("www", "deadbeef", "owner", true, true)
			if !errors.Is(err, ErrPlanChanged) {
				t.Errorf("stale digest = %v, want ErrPlanChanged", err)
			}
			return err
		},
		"no terms": func() error { _, err := rig.issuing.Approve("www", plan.Digest, "owner", false, true); return err },
		"production unconfirmed": func() error {
			_, err := rig.issuing.Approve("www", plan.Digest, "owner", true, false)
			if err == nil || !strings.Contains(err.Error(), "production") {
				t.Errorf("production = %v", err)
			}
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: approved", name)
		}
	}
	if _, err := rig.issuing.Definition("www"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("Definition = %v", err)
	}
	if _, err := rig.issuing.Approve("www", plan.Digest, "owner", true, true); err != nil {
		t.Fatalf("confirmed production approval: %v", err)
	}
	approvals, _ := rig.issuing.Approvals()
	if len(approvals) != 1 || approvals[0].Actor != "owner" || !approvals[0].ConfirmedProduction {
		t.Fatalf("approvals = %+v", approvals)
	}
}

func TestClassifyDirectory(t *testing.T) {
	for directory, want := range map[string]string{
		LetsEncryptStagingDirectory:                      DirectoryStaging,
		"https://acme-v02.api.letsencrypt.org/directory": DirectoryProduction,
		"https://acme.zerossl.com/v2/DV90":               DirectoryProduction,
		"https://localhost:14000/dir":                    DirectoryTest,
		"https://192.168.122.10:14000/dir":               DirectoryTest,
		"https://pebble.test:14000/dir":                  DirectoryTest,
		"not a url":                                      DirectoryProduction,
	} {
		if got := ClassifyDirectory(directory); got != want {
			t.Errorf("ClassifyDirectory(%q) = %q, want %q", directory, got, want)
		}
	}
}

func TestIssueEndToEnd(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.double.terms = "https://ca.example/terms"
	plan := rig.approve()
	def, err := rig.issuing.Definition("www")
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if def.Type != "cert.issue" || len(def.Steps) != 3 {
		t.Fatalf("definition = %+v", def)
	}
	// Nothing happened at definition time.
	if rig.caCalls.Load() != 0 {
		t.Fatalf("CA contacted while defining: %d", rig.caCalls.Load())
	}
	if err := rig.run(def); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Chain and key placed; key 0600, P-256.
	chain, key := docker.CertPaths(rig.stackDir, "www")
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key stat = %v err %v", info, err)
	}
	keyPEM, _ := os.ReadFile(key)
	parsed, err := ParseAccountKey(keyPEM)
	if err != nil || parsed.Curve != elliptic.P256() {
		t.Fatalf("placed key unreadable: %v", err)
	}
	if _, err := InspectChain(mustRead(t, chain), time.Now()); err != nil {
		t.Fatalf("placed chain: %v", err)
	}
	placed := docker.PlacedCerts(rig.stackDir)
	if len(placed) != 1 || placed[0].Name != "www" || placed[0].Names[0] != "www.example.com" {
		t.Fatalf("placed = %+v", placed)
	}
	// The S263 fingerprint and public chain were recorded.
	issuances, err := NewIssuances(rig.store).List()
	if err != nil || len(issuances) != 1 {
		t.Fatalf("issuances = %+v err %v", issuances, err)
	}
	want := NewFingerprint(plan.Names, "script", "dns-01")
	if !issuances[0].Fingerprint.Equal(want) || len(issuances[0].ChainPEM) == 0 {
		t.Fatalf("recorded fingerprint = %+v", issuances[0].Fingerprint)
	}
	// Account key custody: held under a sys_ name, with its location.
	name := AccountSecretName(issuanceDirectory)
	raw, err := rig.secrets.Get(name)
	if err != nil || !secret.IsSystemSecret(name) {
		t.Fatalf("account secret %q: %v", name, err)
	}
	var stored storedAccount
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.KID == "" || stored.KeyPEM == "" {
		t.Fatalf("stored account = %+v err %v", stored, err)
	}
	if listed, _ := rig.secrets.List(); func() bool {
		for _, info := range listed {
			if strings.Contains(fmt.Sprint(info), stored.KeyPEM) {
				return true
			}
		}
		return false
	}() {
		t.Fatal("secret listing leaked the key")
	}
	// The route snippet serves the placed certificate; Caddy reloaded.
	snippet := mustRead(t, filepath.Join(rig.stackDir, "routes", "www.example.com.caddy"))
	if !strings.Contains(string(snippet), "tls /etc/caddy/routes/certs/www/fullchain.pem /etc/caddy/routes/certs/www/privkey.pem") {
		t.Fatalf("snippet does not serve the certificate:\n%s", snippet)
	}
	if rig.reloads != 1 {
		t.Fatalf("reloads = %d", rig.reloads)
	}
	// DNS-01 values were cleaned up.
	if len(rig.provider.records) != 0 {
		t.Fatalf("leftover TXT: %+v", rig.provider.records)
	}
	// No key or account material in anything the API serves.
	approvals, _ := rig.issuing.Approvals()
	served, _ := json.Marshal(approvals)
	plans, _ := rig.issuing.Plans()
	servedPlans, _ := json.Marshal(plans)
	for _, secretText := range []string{stored.KeyPEM, string(keyPEM), "PRIVATE KEY"} {
		if strings.Contains(string(served), secretText) || strings.Contains(string(servedPlans), secretText) {
			t.Fatalf("API output carries key material")
		}
	}
	// The account is reused on the next plan.
	if after, _ := rig.issuing.Plan("www"); after.Account.State != "existing" {
		t.Fatalf("account state after issuance = %q", after.Account.State)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func TestApplyRefusesChangedPlan(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.approve()
	// Strategy edits after approval change the digest: nothing issues.
	rig.saveStrategy(issuanceDirectory, []string{"www.example.com", "api.example.com"})
	if _, err := rig.issuing.Definition("www"); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("changed names = %v, want ErrPlanChanged", err)
	}
	rig.saveStrategy("https://acme.zerossl.com/v2/DV90", []string{"www.example.com"})
	if _, err := rig.issuing.Definition("www"); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("changed directory = %v, want ErrPlanChanged", err)
	}
	if rig.caCalls.Load() != 0 {
		t.Fatalf("CA contacted: %d", rig.caCalls.Load())
	}
}

func TestIssueReloadRefusedRollsBackSnippets(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.approve()
	before := mustRead(t, filepath.Join(rig.stackDir, "routes", "www.example.com.caddy"))
	rig.reloadErr = errors.New("caddy refused")
	def, _ := rig.issuing.Definition("www")
	err := rig.run(def)
	if err == nil || !strings.Contains(err.Error(), "edge reload was refused") {
		t.Fatalf("run = %v", err)
	}
	after := mustRead(t, filepath.Join(rig.stackDir, "routes", "www.example.com.caddy"))
	if string(before) != string(after) {
		t.Fatalf("snippet not rolled back:\n%s", after)
	}
	// The issued files stay, and the issuance is recorded: a retry of the
	// reload (or the next apply) picks them up.
	if len(docker.PlacedCerts(rig.stackDir)) != 1 {
		t.Fatal("placed certificate was removed")
	}
}

func TestIssueFailureIsNotRecorded(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.double.failFinal = true
	rig.approve()
	def, _ := rig.issuing.Definition("www")
	err := rig.run(def)
	if err == nil || !strings.Contains(err.Error(), "issue-and-place") {
		t.Fatalf("run = %v", err)
	}
	if issuances, _ := NewIssuances(rig.store).List(); len(issuances) != 0 {
		t.Fatalf("recorded a failed issuance: %+v", issuances)
	}
	if len(docker.PlacedCerts(rig.stackDir)) != 0 || rig.reloads != 0 {
		t.Fatal("placed or reloaded after a failed issuance")
	}
	if len(rig.provider.records) != 0 {
		t.Fatalf("leftover TXT: %+v", rig.provider.records)
	}
	// The registered account survives the failed order.
	if !rig.issuing.accountExists(issuanceDirectory) {
		t.Fatal("account lost after failed issuance")
	}
}

func TestPlacementFailureIsNotRecorded(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.approve()
	// A file where the certs directory belongs makes placement fail.
	if err := os.WriteFile(filepath.Join(rig.stackDir, "routes", "certs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	def, _ := rig.issuing.Definition("www")
	if err := rig.run(def); err == nil || !strings.Contains(err.Error(), "delivery failed") {
		t.Fatalf("run = %v", err)
	}
	if issuances, _ := NewIssuances(rig.store).List(); len(issuances) != 0 {
		t.Fatalf("recorded an undelivered certificate: %+v", issuances)
	}
	if rig.reloads != 0 {
		t.Fatal("reloaded after placement failure")
	}
}

func TestProviderEnvOverrides(t *testing.T) {
	env := map[string]string{EnvCloudflareAPI: "http://127.0.0.1:9999/client/v4"}
	registry, err := ProvidersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("ProvidersFromEnv: %v", err)
	}
	for _, name := range []string{"cloudflare", "route53", "digitalocean"} {
		if _, err := registry.Get(name); err != nil {
			t.Fatalf("provider %s: %v", name, err)
		}
	}
	env[EnvCloudflareAPI] = "ftp://x"
	if _, err := ProvidersFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("accepted a non-http origin")
	}
	if ProbeFromEnv(func(string) string { return "" }) == nil {
		t.Fatal("no default probe")
	}
}

func TestStoreCredentialsAndPlanPresence(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	rig.provider.fields = []dns.CredentialField{{Name: "api_token", Label: "API token", Secret: true}}
	plan, _ := rig.issuing.Plan("www")
	if plan.Ready || len(plan.Credentials) != 1 || plan.Credentials[0].Present || plan.Credentials[0].Label != "API token" {
		t.Fatalf("before = %+v blockers %v", plan.Credentials, plan.Blockers)
	}
	before := plan.Digest
	for name, values := range map[string]map[string]string{
		"missing": {},
		"empty":   {"api_token": ""},
		"extra":   {"api_token": "v", "other": "v"},
	} {
		if _, err := rig.issuing.StoreCredentials("script", values); err == nil {
			t.Errorf("%s: stored", name)
		}
	}
	if _, err := rig.issuing.StoreCredentials("nope", map[string]string{"api_token": "v"}); err == nil {
		t.Error("stored for an unknown provider")
	}
	fields, err := rig.issuing.StoreCredentials("script", map[string]string{"api_token": "hunter2-value"})
	if err != nil || len(fields) != 1 || fields[0] != "api_token" {
		t.Fatalf("StoreCredentials = %v %v", fields, err)
	}
	plan, _ = rig.issuing.Plan("www")
	if !plan.Ready || !plan.Credentials[0].Present {
		t.Fatalf("after = %+v blockers %v", plan.Credentials, plan.Blockers)
	}
	// Presence never changes the digest the owner approved.
	if plan.Digest != before {
		t.Fatalf("digest changed with credential presence")
	}
	out, _ := json.Marshal(plan)
	if strings.Contains(string(out), "hunter2-value") {
		t.Fatal("plan carries the credential value")
	}
	if got, err := rig.secrets.Get("dns_script_api_token"); err != nil || got != "hunter2-value" {
		t.Fatalf("stored under the conventional name: %q %v", got, err)
	}
}

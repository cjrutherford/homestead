package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/cert"
	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

type issuanceFakeDNS struct{}

func (issuanceFakeDNS) Name() string { return "fakedns" }
func (issuanceFakeDNS) Capabilities() []dns.Capability {
	return []dns.Capability{dns.CapabilityDNS01}
}
func (issuanceFakeDNS) RequiredCredentials() []dns.CredentialField { return nil }
func (issuanceFakeDNS) ListRecords(context.Context, string, dns.Credentials) ([]dns.Record, error) {
	return nil, nil
}
func (issuanceFakeDNS) ApplyChanges(context.Context, string, dns.Credentials, []dns.Change) error {
	return nil
}

func issuanceServer(t *testing.T) (*Server, *store.Store, *cert.Issuing) {
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
	registry := dns.NewRegistry()
	if err := registry.Register(issuanceFakeDNS{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	issuing := &cert.Issuing{
		Store: st, Wizards: wizard.New(st), Secrets: secrets, Providers: registry,
		StackDir: filepath.Join(dir, "caddy"),
		// The CA never answers: apply must fail without any real network.
		NewClient: func(string) (*cert.Client, error) { return cert.NewClient("http://127.0.0.1:1/directory") },
		Interval:  time.Millisecond,
		Routes:    func() ([]docker.Route, error) { return servedCandidateRoutes(st) },
		Reload:    func(context.Context) error { return nil },
	}
	srv := New("127.0.0.1:0")
	srv.MountTLSIssuance(st, issuing)
	return srv, st, issuing
}

func saveIssuanceInputs(t *testing.T, st *store.Store, directory string) {
	t.Helper()
	svc := wizard.New(st)
	strategy := map[string]any{"certificates": []any{map[string]any{
		"Name": "www", "DNSNames": []string{"www.example.com"}, "State": "pending",
		"Renewal": map[string]any{"Auto": true},
		"Issuer": map[string]any{"Name": "le", "Type": "acme", "Directory": directory,
			"Challenge": "dns-01", "DNSProvider": "fakedns"},
	}}}
	if _, err := svc.Put(cert.TLSWizardID, "strategy", strategy); err != nil {
		t.Fatalf("strategy: %v", err)
	}
	if _, err := svc.Put("domain", "zone", map[string]any{"provider": "fakedns", "zone": "example.com"}); err != nil {
		t.Fatalf("domain: %v", err)
	}
}

func TestTLSIssuancePlanApproveApplyFlow(t *testing.T) {
	srv, st, _ := issuanceServer(t)
	// No strategy yet.
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/issuance/plans", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no strategy code = %d", rec.Code)
	}
	saveIssuanceInputs(t, st, cert.LetsEncryptStagingDirectory)
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/issuance/plans", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plans = %d (%s)", rec.Code, rec.Body.String())
	}
	var plans struct {
		Plans []cert.IssuancePlan `json:"plans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plans); err != nil || len(plans.Plans) != 1 {
		t.Fatalf("plans body = %s err %v", rec.Body.String(), err)
	}
	plan := plans.Plans[0]
	if !plan.Ready || plan.Digest == "" {
		t.Fatalf("plan = %+v", plan)
	}
	// Apply before approval conflicts and starts nothing.
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/apply", map[string]any{"certificate": "www"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("apply before approval = %d (%s)", rec.Code, rec.Body.String())
	}
	// Approval needs the digest and the terms.
	for name, body := range map[string]map[string]any{
		"stale digest": {"certificate": "www", "digest": "x", "agree_terms": true},
		"no terms":     {"certificate": "www", "digest": plan.Digest},
	} {
		if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/approvals", body); rec.Code < 400 {
			t.Fatalf("%s accepted: %d", name, rec.Code)
		}
	}
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/approvals",
		map[string]any{"certificate": "www", "digest": plan.Digest, "agree_terms": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("approval response carries key material")
	}
	rec = serveRequest(t, srv, http.MethodGet, "/api/v1/tls/issuance/approvals", nil)
	if !strings.Contains(rec.Body.String(), `"actor":"local"`) {
		t.Fatalf("approvals = %s", rec.Body.String())
	}
	// Apply starts an operation; the unreachable CA fails it, audited.
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/apply", map[string]any{"certificate": "www"})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d (%s)", rec.Code, rec.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done, err := operation.NewRunner(st).Wait(ctx, started.ID)
	if err != nil || done.Status != operation.StatusFailed || done.Type != "cert.issue" {
		t.Fatalf("operation = %+v err %v", done, err)
	}
	if strings.Contains(done.Error+done.Payload+done.Result, "PRIVATE KEY") {
		t.Fatal("operation carries key material")
	}
	deadline := time.Now().Add(5 * time.Second)
	var actions []string
	for time.Now().Before(deadline) {
		entries, _ := audit.New(st).List(100)
		actions = actions[:0]
		for _, e := range entries {
			actions = append(actions, e.Action+":"+e.Outcome)
			if strings.Contains(e.Detail+e.Plan, "PRIVATE KEY") {
				t.Fatal("audit carries key material")
			}
		}
		if strings.Contains(strings.Join(actions, ","), "cert.issuance:error") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := strings.Join(actions, ",")
	for _, want := range []string{"cert.issuance.approve:ok", "cert.issuance.start:ok", "cert.issuance:error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("audit actions %q missing %q", got, want)
		}
	}
}

func TestTLSIssuanceProductionNeedsConfirmation(t *testing.T) {
	srv, st, _ := issuanceServer(t)
	saveIssuanceInputs(t, st, "https://acme-v02.api.letsencrypt.org/directory")
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/issuance/plans", nil)
	var plans struct {
		Plans []cert.IssuancePlan `json:"plans"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &plans)
	body := map[string]any{"certificate": "www", "digest": plans.Plans[0].Digest, "agree_terms": true}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/approvals", body); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "production") {
		t.Fatalf("unconfirmed production = %d (%s)", rec.Code, rec.Body.String())
	}
	body["confirm_production"] = true
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/approvals", body); rec.Code != http.StatusOK {
		t.Fatalf("confirmed production = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestServedCandidateRoutesIncludesEdgeTable(t *testing.T) {
	_, st, _ := issuanceServer(t)
	route := docker.Route{Host: "app.example.com", Target: docker.Target{Host: "a", Port: 1},
		Exposure: docker.ExposureLocal, TLS: docker.TLSAuto}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	routes, err := servedCandidateRoutes(st)
	if err != nil || len(routes) != 1 || routes[0].Host != "app.example.com" {
		t.Fatalf("routes = %+v err %v", routes, err)
	}
}

func TestTLSIssuanceCredentialsAreWriteOnly(t *testing.T) {
	srv, st, issuing := issuanceServer(t)
	// A provider with one credential field.
	registry := dns.NewRegistry()
	_ = registry.Register(issuanceTokenDNS{})
	issuing.Providers = registry
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/credentials",
		map[string]any{"provider": "tokendns", "values": map[string]string{"api_token": "topsecret-value"}})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "topsecret-value") {
		t.Fatalf("code = %d body %q", rec.Code, rec.Body.String())
	}
	entries, _ := audit.New(st).List(100)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Detail+e.Plan+e.Action, "topsecret-value") {
			t.Fatal("audit carries the credential value")
		}
		if e.Action == "cert.issuance.credentials" && e.Outcome == audit.OutcomeOK && strings.Contains(e.Detail, "api_token") {
			found = true
		}
	}
	if !found {
		t.Fatal("credential storage was not audited")
	}
	for name, body := range map[string]any{
		"unknown provider": map[string]any{"provider": "nope", "values": map[string]string{"api_token": "x"}},
		"missing field":    map[string]any{"provider": "tokendns", "values": map[string]string{}},
	} {
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/tls/issuance/credentials", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	// There is no read route for stored values.
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/tls/issuance/credentials", nil); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("GET credentials = %d", rec.Code)
	}
}

type issuanceTokenDNS struct{ issuanceFakeDNS }

func (issuanceTokenDNS) Name() string { return "tokendns" }
func (issuanceTokenDNS) RequiredCredentials() []dns.CredentialField {
	return []dns.CredentialField{{Name: "api_token", Label: "API token", Secret: true}}
}

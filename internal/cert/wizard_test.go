package cert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/dns"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openTLSWizard(t *testing.T, authority *Authority) (*Wizard, *secret.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	registry := dns.NewRegistry()
	if err := registry.Register(&scriptDNSProvider{fields: []dns.CredentialField{
		{Name: "api_token", Label: "Token", Secret: true},
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return NewWizard(wizard.New(s), secrets, registry, authority), secrets
}

func strategyPayload(t *testing.T, certificates []Certificate) map[string]any {
	t.Helper()
	raw, err := json.Marshal(certificates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return map[string]any{"certificates": decoded}
}

func lanIssuer() Issuer {
	return Issuer{Name: "homestead-internal", Type: IssuerInternal}
}

func TestTLSSteps(t *testing.T) {
	if len(TLSSteps) != 4 || TLSSteps[0] != "strategy" || TLSSteps[3] != "done" {
		t.Fatalf("steps = %v", TLSSteps)
	}
	if tlsStepIndex("bogus") != -1 {
		t.Fatal("indexed unknown step")
	}
}

func TestWizardSaveFlow(t *testing.T) {
	ca, err := GenerateCA("Test CA", DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	w, _ := openTLSWizard(t, &ca)
	strategy := []Certificate{{
		Name: "lan", DNSNames: []string{"app.lan", "*.apps.lan"},
		Issuer: lanIssuer(), State: StatePending, Renewal: Renewal{Auto: true},
	}}
	for _, step := range TLSSteps {
		if err := w.Save(step, strategyPayload(t, strategy)); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	if err := w.Save("bogus", strategyPayload(t, strategy)); err == nil {
		t.Fatal("saved unknown step")
	}
	// Private names refuse public ACME.
	bad := strategy
	bad[0].Issuer = Issuer{Name: "le", Type: IssuerACME,
		Directory: "https://x/", Challenge: ChallengeDNS01, DNSProvider: "script"}
	if err := w.Save("strategy", strategyPayload(t, bad)); err == nil ||
		!strings.Contains(err.Error(), "needs internal or manual") {
		t.Fatalf("err = %v", err)
	}
	// ...but accept manual by owner hand.
	bad[0].Issuer = Issuer{Name: "owner", Type: IssuerManual}
	bad[0].Renewal = Renewal{}
	if err := w.Save("strategy", strategyPayload(t, bad)); err != nil {
		t.Fatalf("manual: %v", err)
	}
	// Shape violations refuse.
	empty := []Certificate{}
	if err := w.Save("strategy", strategyPayload(t, empty)); err == nil {
		t.Fatal("saved empty strategy")
	}
}

func serveDirectory(t *testing.T) (string, func(string) (*Client, error)) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"newNonce":"https://x/","newAccount":"https://x/","newOrder":"https://x/"}`))
	}))
	t.Cleanup(srv.Close)
	factory := func(directory string) (*Client, error) {
		client, err := NewClient(directory)
		if err != nil {
			return nil, err
		}
		client.HTTP = srv.Client()
		return client, nil
	}
	return srv.URL, factory
}

func TestPrerequisites(t *testing.T) {
	ca, err := GenerateCA("Test CA", DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	ctx := context.Background()
	directory, factory := serveDirectory(t)
	dnsIssuer := func() Issuer {
		return Issuer{Name: "acme", Type: IssuerACME,
			Directory: directory, Challenge: ChallengeDNS01, DNSProvider: "script"}
	}
	strategy := []Certificate{
		{Name: "lan", DNSNames: []string{"app.lan"}, Issuer: lanIssuer(), State: StatePending},
		{Name: "web", DNSNames: []string{"example.com"}, Issuer: dnsIssuer(), State: StatePending},
		{Name: "legacy", DNSNames: []string{"old.example.com"},
			Issuer: Issuer{Name: "owner", Type: IssuerManual}, State: StatePending},
		{Name: "direct", DNSNames: []string{"cdn.example.com"},
			Issuer: Issuer{Name: "le", Type: IssuerACME, Directory: directory, Challenge: ChallengeHTTP01},
			State:  StatePending},
	}
	w, secrets := openTLSWizard(t, &ca)
	w.newClient = factory
	if err := w.Save("strategy", strategyPayload(t, strategy)); err != nil {
		t.Fatalf("save: %v", err)
	}
	prereqs, err := w.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	byName := map[string]Prereq{}
	for _, prereq := range prereqs {
		byName[prereq.Certificate] = prereq
	}
	if !byName["lan"].Ready || !byName["legacy"].Ready || !byName["direct"].Ready {
		t.Fatalf("prereqs = %+v", prereqs)
	}
	if byName["web"].Ready || !strings.Contains(byName["web"].Detail, "api_token") {
		t.Fatalf("web = %+v", byName["web"])
	}
	// Credentials present flips dns-01 ready (directory serves locally).
	if err := secrets.Set("dns_script_api_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	prereqs, err = w.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	for _, prereq := range prereqs {
		if !prereq.Ready {
			t.Fatalf("prereq = %+v", prereq)
		}
	}
	plan, err := w.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Certificates) != 4 || len(plan.Prereqs) != 4 {
		t.Fatalf("plan = %+v", plan)
	}
	// Without an authority, internal prerequisites report unconfigured.
	bare, bareSecrets := openTLSWizard(t, nil)
	if err := bare.Save("strategy", strategyPayload(t, strategy[:1])); err != nil {
		t.Fatalf("save: %v", err)
	}
	prereqs, err = bare.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if prereqs[0].Ready || !strings.Contains(prereqs[0].Detail, "not configured") {
		t.Fatalf("prereq = %+v", prereqs[0])
	}
	// Unreachable directories fail readiness, not the call.
	down := strategy[1]
	down.Issuer.Directory = "https://127.0.0.1:1/"
	if err := bare.Save("strategy", strategyPayload(t, []Certificate{down})); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := bareSecrets.Set("dns_script_api_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	prereqs, err = bare.Prerequisites(ctx)
	if err != nil {
		t.Fatalf("Prerequisites: %v", err)
	}
	if prereqs[0].Ready || !strings.Contains(prereqs[0].Detail, "unreachable") {
		t.Fatalf("prereq = %+v", prereqs[0])
	}
}

func TestValidateIssued(t *testing.T) {
	ca, err := GenerateCA("Test CA", DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	w, _ := openTLSWizard(t, &ca)
	strategy := []Certificate{{
		Name: "lan", DNSNames: []string{"app.lan"},
		Issuer: lanIssuer(), State: StateIssued, Renewal: Renewal{Auto: true},
	}}
	if err := w.Save("validation", strategyPayload(t, strategy)); err != nil {
		t.Fatalf("save: %v", err)
	}
	chain, _, err := ca.IssueServerCert([]string{"app.lan"}, 300*24*time.Hour, testTime())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	statuses, err := w.ValidateIssued(map[string][]byte{"lan": chain}, testTime())
	if err != nil {
		t.Fatalf("ValidateIssued: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != HealthHealthy {
		t.Fatalf("statuses = %+v", statuses)
	}
	statuses, err = w.ValidateIssued(nil, testTime())
	if err != nil {
		t.Fatalf("ValidateIssued: %v", err)
	}
	if statuses[0].Health != HealthMissing {
		t.Fatalf("statuses = %+v", statuses)
	}
	empty, _ := openTLSWizard(t, &ca)
	if _, err := empty.ValidateIssued(nil, testTime()); err == nil {
		t.Fatal("validated without strategy")
	}
}

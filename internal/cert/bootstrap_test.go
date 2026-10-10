package cert

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/store"
)

// stubProvider carries capabilities without behavior: decisions make
// no provider calls.
type stubProvider struct {
	name string
	caps []dns.Capability
}

func (s stubProvider) Name() string                               { return s.name }
func (s stubProvider) Capabilities() []dns.Capability             { return s.caps }
func (s stubProvider) RequiredCredentials() []dns.CredentialField { return nil }
func (s stubProvider) ListRecords(_ context.Context, _ string, _ dns.Credentials) ([]dns.Record, error) {
	return nil, dns.ErrNoAPI
}
func (s stubProvider) ApplyChanges(_ context.Context, _ string, _ dns.Credentials, _ []dns.Change) error {
	return dns.ErrNoAPI
}

func readinessRegistry(t *testing.T) *dns.Registry {
	t.Helper()
	registry := dns.NewRegistry()
	for _, provider := range []dns.Provider{
		stubProvider{name: "api", caps: []dns.Capability{dns.CapabilityRecords, dns.CapabilityDNS01}},
		stubProvider{name: "plain", caps: []dns.Capability{dns.CapabilityRecords}},
		stubProvider{name: "manual", caps: nil},
	} {
		if err := registry.Register(provider); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	return registry
}

func openReadinessStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDecideReadiness(t *testing.T) {
	registry := readinessRegistry(t)
	for _, name := range []string{"homestead", "homestead.lan", "192.168.1.10"} {
		ready, err := DecideReadiness(name, nil, "", Ingress{})
		if err != nil {
			t.Fatalf("Decide %s: %v", name, err)
		}
		if ready.Strategy != IssuerInternal || ready.NeedsIngress {
			t.Fatalf("%s = %+v", name, ready)
		}
	}
	// DNS-01 decides with no ingress evidence at all.
	ready, err := DecideReadiness("home.example.com", registry, "api", Ingress{})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Strategy != IssuerACME || ready.Challenge != ChallengeDNS01 ||
		ready.DNSProvider != "api" || ready.NeedsIngress {
		t.Fatalf("ready = %+v", ready)
	}
	// Direct challenges decide on verified ingress only.
	ready, err = DecideReadiness("home.example.com", registry, "plain", Ingress{HTTP: true})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Challenge != ChallengeHTTP01 || !ready.NeedsIngress {
		t.Fatalf("ready = %+v", ready)
	}
	ready, err = DecideReadiness("home.example.com", registry, "plain", Ingress{HTTPS: true})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Challenge != ChallengeTLSALPN01 || !ready.NeedsIngress {
		t.Fatalf("ready = %+v", ready)
	}
	// Nothing capable and nothing verified: the owner path.
	ready, err = DecideReadiness("home.example.com", registry, "plain", Ingress{})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Strategy != IssuerManual {
		t.Fatalf("ready = %+v", ready)
	}
	ready, err = DecideReadiness("home.example.com", registry, "manual", Ingress{HTTP: true, HTTPS: true})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// Direct challenges need no DNS provider: verified ingress
	// automates even for manual zones.
	if ready.Strategy != IssuerACME || ready.Challenge != ChallengeHTTP01 {
		t.Fatalf("ready = %+v", ready)
	}
	// Wildcards skip direct challenges even with ingress verified.
	ready, err = DecideReadiness("*.example.com", registry, "plain", Ingress{HTTP: true})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Strategy != IssuerManual {
		t.Fatalf("ready = %+v", ready)
	}
	ready, err = DecideReadiness("*.example.com", registry, "api", Ingress{})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ready.Strategy != IssuerACME || ready.Challenge != ChallengeDNS01 {
		t.Fatalf("ready = %+v", ready)
	}
}

func TestDecideReadinessRefusals(t *testing.T) {
	registry := readinessRegistry(t)
	for name, args := range map[string]struct {
		fqdn     string
		provider string
		ingress  Ingress
		nilReg   bool
	}{
		"malformed":    {"Bad_Name!", "api", Ingress{}, false},
		"unknown":      {"home.example.com", "ghost", Ingress{}, false},
		"nil registry": {"home.example.com", "api", Ingress{}, true},
	} {
		var ready Readiness
		var err error
		if args.nilReg {
			ready, err = DecideReadiness(args.fqdn, nil, args.provider, args.ingress)
		} else {
			ready, err = DecideReadiness(args.fqdn, registry, args.provider, args.ingress)
		}
		if err == nil {
			t.Fatalf("%s accepted: %+v", name, ready)
		}
	}
}

func TestEstablishReadinessRecords(t *testing.T) {
	registry := readinessRegistry(t)
	st := openReadinessStore(t)
	ready, err := EstablishReadiness(st, "home.example.com", registry, "api", Ingress{})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if ready.FQDN != "home.example.com" || ready.Detail == "" {
		t.Fatalf("ready = %+v", ready)
	}
	current, err := CurrentReadiness(st)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.FQDN != ready.FQDN || current.Strategy != ready.Strategy ||
		current.Challenge != ready.Challenge || current.DNSProvider != ready.DNSProvider ||
		current.NeedsIngress != ready.NeedsIngress {
		t.Fatalf("current = %+v, want %+v", current, ready)
	}
	// Re-deciding replaces the record.
	replaced, err := EstablishReadiness(st, "home.example.com", registry, "plain", Ingress{HTTP: true})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if replaced.Challenge != ChallengeHTTP01 {
		t.Fatalf("replaced = %+v", replaced)
	}
	current, err = CurrentReadiness(st)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Challenge != ChallengeHTTP01 || !current.NeedsIngress {
		t.Fatalf("current = %+v", current)
	}
	if _, err := EstablishReadiness(nil, "home.example.com", registry, "api", Ingress{}); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := CurrentReadiness(openReadinessStore(t)); err != ErrNoTLSReadiness {
		t.Fatalf("Current = %v", err)
	}
	if err := RecordReadiness(st, Readiness{}); err == nil {
		t.Fatal("empty readiness recorded")
	}
	// Details survive the round trip for owner display.
	if !strings.Contains(ready.Detail, "DNS-01") {
		t.Fatalf("detail = %q", ready.Detail)
	}
}

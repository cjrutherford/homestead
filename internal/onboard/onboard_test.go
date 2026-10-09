package onboard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/dns"
	"homestead/internal/domain"
	"homestead/internal/firewall"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// stubProvider is a scriptable dns.Provider: records or failure.
type stubProvider struct {
	name    string
	fields  []dns.CredentialField
	records []dns.Record
	err     error
}

func (s stubProvider) Name() string                               { return s.name }
func (s stubProvider) Capabilities() []dns.Capability             { return []dns.Capability{dns.CapabilityRecords} }
func (s stubProvider) RequiredCredentials() []dns.CredentialField { return s.fields }
func (s stubProvider) ListRecords(_ context.Context, _ string, _ dns.Credentials) ([]dns.Record, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.records, nil
}
func (s stubProvider) ApplyChanges(_ context.Context, _ string, _ dns.Credentials, _ []dns.Change) error {
	return dns.ErrNoAPI
}

func apiProvider() stubProvider {
	return stubProvider{
		name:    "api",
		fields:  []dns.CredentialField{{Name: "api_token", Label: "Token", Secret: true}},
		records: []dns.Record{{Name: "@", Type: dns.RecordA, Value: "203.0.113.7"}},
	}
}

// openOnboarding builds a Service and its domain flow over a scratch
// store with one stub provider registered: claimed and administered
// when asked, firewall applied when asked.
func openOnboarding(t *testing.T, provider stubProvider, claimed, withAdmin, firewallApplied bool) (*Service, *domain.Service) {
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
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	claimSvc := claim.New(s)
	authSvc := auth.New(s, claimSvc)
	if claimed {
		token, err := claimSvc.MintToken(claim.TokenTTL)
		if err != nil {
			t.Fatalf("MintToken: %v", err)
		}
		if err := claimSvc.Claim(token); err != nil {
			t.Fatalf("Claim: %v", err)
		}
	}
	if withAdmin {
		if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err != nil {
			t.Fatalf("SetupAdmin: %v", err)
		}
	}
	detect := func() firewall.State {
		if !firewallApplied {
			return firewall.State{}
		}
		return firewall.State{NFTAvailable: true, RulesetAccessible: true, HasHomesteadTable: true}
	}
	domains := domain.New(wizard.New(s), secrets, registry)
	return New(domains, claimSvc, authSvc, detect, s), domains
}

// selectZone saves a complete wizard selection and stores its
// credentials through the domain flow.
func selectZone(t *testing.T, domains *domain.Service, zone string) {
	t.Helper()
	if err := domains.Save("done", map[string]any{"provider": "api", "zone": zone, "base": "home"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := domains.StoreCredentials("api", map[string]string{"api_token": "token"}); err != nil {
		t.Fatalf("store: %v", err)
	}
}

func TestOnboardFullFlow(t *testing.T) {
	o, domains := openOnboarding(t, apiProvider(), true, true, true)
	selectZone(t, domains, "example.com")
	onboarded, err := o.Onboard(context.Background(), "owner")
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if onboarded.Provider != "api" || onboarded.Zone != "example.com" || onboarded.Base != "home" ||
		onboarded.FQDN != "home.example.com" || onboarded.Actor != "owner" {
		t.Fatalf("onboarded = %+v", onboarded)
	}
	current, err := o.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current != onboarded {
		t.Fatalf("current = %+v, want %+v", current, onboarded)
	}
	// Re-running converges: new selection replaces the record.
	selectZone(t, domains, "example.net")
	replaced, err := o.Onboard(context.Background(), "owner")
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if replaced.FQDN != "home.example.net" {
		t.Fatalf("replaced = %+v", replaced)
	}
	current, err = o.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.FQDN != "home.example.net" {
		t.Fatalf("current = %+v", current)
	}
}

func TestOnboardGatesBaseline(t *testing.T) {
	ctx := context.Background()
	unclaimed, domains := openOnboarding(t, apiProvider(), false, false, false)
	selectZone(t, domains, "example.com")
	if _, err := unclaimed.Onboard(ctx, "owner"); err == nil || !strings.Contains(err.Error(), "claim") {
		t.Fatalf("unclaimed onboard = %v", err)
	}
	noAdmin, domains := openOnboarding(t, apiProvider(), true, false, true)
	selectZone(t, domains, "example.com")
	if _, err := noAdmin.Onboard(ctx, "owner"); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatalf("adminless onboard = %v", err)
	}
	noFirewall, _ := openOnboarding(t, apiProvider(), true, true, false)
	if _, err := noFirewall.Onboard(ctx, "owner"); err == nil || !strings.Contains(err.Error(), "firewall") {
		t.Fatalf("firewall-less onboard = %v", err)
	}
	// Nothing recorded on gated attempts.
	if _, err := noFirewall.Current(); err != ErrNotOnboarded {
		t.Fatalf("Current = %v", err)
	}
}

func TestOnboardProvesOwnership(t *testing.T) {
	broken, domains := openOnboarding(t, stubProvider{name: "api",
		fields: []dns.CredentialField{{Name: "api_token", Label: "T", Secret: true}},
		err:    errors.New("denied")}, true, true, true)
	selectZone(t, domains, "example.com")
	if _, err := broken.Onboard(context.Background(), "owner"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("unproven onboard = %v", err)
	}
	// Failed proofs record nothing.
	if _, err := broken.Current(); err != ErrNotOnboarded {
		t.Fatalf("Current = %v", err)
	}
}

func TestOnboardRequiresActor(t *testing.T) {
	o, domains := openOnboarding(t, apiProvider(), true, true, true)
	selectZone(t, domains, "example.com")
	if _, err := o.Onboard(context.Background(), ""); err == nil {
		t.Fatal("actorless onboard accepted")
	}
}

func TestCurrentEmpty(t *testing.T) {
	o, _ := openOnboarding(t, apiProvider(), true, true, true)
	if _, err := o.Current(); err != ErrNotOnboarded {
		t.Fatalf("Current = %v", err)
	}
}

func TestBaselineReportsGate(t *testing.T) {
	o, _ := openOnboarding(t, apiProvider(), false, false, false)
	report := o.Baseline()
	if report.Ready {
		t.Fatalf("report = %+v, want unready", report)
	}
	if len(report.Checks) != 4 {
		t.Fatalf("checks = %+v", report.Checks)
	}
}

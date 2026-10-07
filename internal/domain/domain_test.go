package domain

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/dns"
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

func openDomain(t *testing.T, providers ...dns.Provider) *Service {
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
	for _, p := range providers {
		if err := registry.Register(p); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	return New(wizard.New(s), secrets, registry)
}

func apiProvider() stubProvider {
	return stubProvider{
		name:    "api",
		fields:  []dns.CredentialField{{Name: "api_token", Label: "Token", Secret: true}},
		records: []dns.Record{{Name: "@", Type: dns.RecordA, Value: "203.0.113.7"}},
	}
}

func TestSteps(t *testing.T) {
	if next, err := Next(""); err != nil || next != "provider" {
		t.Fatalf("start = %q %v", next, err)
	}
	if next, err := Next("provider"); err != nil || next != "zone" {
		t.Fatalf("next = %q %v", next, err)
	}
	if next, err := Next("done"); err != nil || next != "" {
		t.Fatalf("end = %q %v", next, err)
	}
	if _, err := Next("nope"); err == nil {
		t.Fatal("advanced unknown step")
	}
}

func TestSaveFlow(t *testing.T) {
	svc := openDomain(t, apiProvider(), dns.Manual{})
	if names := svc.Providers(); len(names) != 2 || names[0] != "api" || names[1] != "manual" {
		t.Fatalf("providers = %v", names)
	}
	fields, err := svc.RequiredCredentials("api")
	if err != nil || len(fields) != 1 || fields[0].Name != "api_token" {
		t.Fatalf("fields = %+v %v", fields, err)
	}
	if _, err := svc.RequiredCredentials("nope"); err == nil {
		t.Fatal("described unknown provider")
	}
	flow := []struct {
		step    string
		payload map[string]any
	}{
		{"provider", map[string]any{"provider": "api"}},
		{"zone", map[string]any{"provider": "api", "zone": "example.com"}},
		{"credentials", map[string]any{"provider": "api", "zone": "example.com"}},
		{"validate", map[string]any{"provider": "api", "zone": "example.com"}},
		{"subdomain", map[string]any{"provider": "api", "zone": "example.com", "base": "home"}},
		{"done", map[string]any{"provider": "api", "zone": "example.com", "base": "home"}},
	}
	for _, f := range flow {
		if err := svc.Save(f.step, f.payload); err != nil {
			t.Fatalf("save %s: %v", f.step, err)
		}
	}
	plan, err := svc.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != (Plan{Provider: "api", Zone: "example.com", Base: "home", FQDN: "home.example.com"}) {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestSaveRejects(t *testing.T) {
	svc := openDomain(t, apiProvider())
	cases := []struct {
		name    string
		step    string
		payload map[string]any
		want    string
	}{
		{"unknown step", "nope", map[string]any{}, "unknown step"},
		{"unknown provider", "provider", map[string]any{"provider": "nope"}, "unknown provider"},
		{"bad zone", "zone", map[string]any{"provider": "api", "zone": "BAD"}, "lowercase"},
		{"fqdn zone", "zone", map[string]any{"provider": "api", "zone": "example.com."}, "relative origin"},
		{"credential in payload", "credentials",
			map[string]any{"provider": "api", "zone": "example.com", "api_token": "leak"},
			"secret store"},
		{"bad base", "subdomain",
			map[string]any{"provider": "api", "zone": "example.com", "base": "Home!"},
			"lowercase"},
		{"bad base chars", "subdomain",
			map[string]any{"provider": "api", "zone": "example.com", "base": "ho_me"},
			"invalid base"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Save(tc.step, tc.payload)
			if err == nil {
				t.Fatal("saved invalid payload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	svc := openDomain(t, apiProvider())
	if err := svc.StoreCredentials("nope", nil); err == nil {
		t.Fatal("stored for unknown provider")
	}
	if err := svc.StoreCredentials("api", map[string]string{"api_token": "token"}); err != nil {
		t.Fatalf("StoreCredentials: %v", err)
	}
	if err := svc.StoreCredentials("api", map[string]string{}); err == nil {
		t.Fatal("stored empty set")
	}
	if err := svc.StoreCredentials("api", map[string]string{"api_token": "t", "extra": "x"}); err == nil {
		t.Fatal("stored extras")
	}
	// Secrets are retrievable under conventional names only through the
	// service, never echoed in wizard state.
	state, err := svc.wizards.Get(WizardID)
	if err == nil {
		for key := range state.Payload {
			if key == "api_token" {
				t.Fatal("credential value in wizard payload")
			}
		}
	}
}

func TestValidate(t *testing.T) {
	ctx := context.Background()
	svc := openDomain(t, apiProvider())
	if err := svc.Save("zone", map[string]any{"provider": "api", "zone": "example.com"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Missing secrets fail naming the field.
	if err := svc.Validate(ctx); err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Fatalf("err = %v", err)
	}
	if err := svc.StoreCredentials("api", map[string]string{"api_token": "token"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := svc.Validate(ctx); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Provider failures surface wrapped.
	broken := openDomain(t, stubProvider{name: "api",
		fields: []dns.CredentialField{{Name: "api_token", Label: "T", Secret: true}},
		err:    errors.New("denied")})
	if err := broken.Save("zone", map[string]any{"provider": "api", "zone": "example.com"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := broken.StoreCredentials("api", map[string]string{"api_token": "t"}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := broken.Validate(ctx); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
	// Manual passes on syntax with nothing to prove against.
	manual := openDomain(t, dns.Manual{})
	if err := manual.Save("zone", map[string]any{"provider": "manual", "zone": "example.com"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := manual.Validate(ctx); err != nil {
		t.Fatalf("manual Validate: %v", err)
	}
	// No saved selection fails.
	empty := openDomain(t, apiProvider())
	if err := empty.Validate(ctx); err == nil {
		t.Fatal("validated without selection")
	}
	if _, err := empty.Plan(); err == nil {
		t.Fatal("planned without selection")
	}
}

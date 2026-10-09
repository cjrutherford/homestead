package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubProvider proves the contract is implementable and exercises
// validation; later slices carry the real ones.
type stubProvider struct {
	name  string
	caps  []Capability
	creds []CredentialField
	fail  error
}

func (s stubProvider) Name() string                           { return s.name }
func (s stubProvider) Capabilities() []Capability             { return s.caps }
func (s stubProvider) RequiredCredentials() []CredentialField { return s.creds }
func (s stubProvider) CheckConnection(_ context.Context, _ string, _ Credentials) error {
	return s.fail
}

func TestValidateProvider(t *testing.T) {
	var _ Provider = stubProvider{}
	good := stubProvider{
		name:  "stub",
		caps:  []Capability{CapabilityOIDC, CapabilitySAML},
		creds: []CredentialField{{Name: "api_token", Label: "API token", Secret: true}},
	}
	if err := ValidateProvider(good); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	for _, p := range []stubProvider{
		{name: "", caps: good.caps, creds: good.creds},
		{name: "stub", caps: []Capability{"telepathy"}, creds: good.creds},
		{name: "stub", caps: good.caps, creds: []CredentialField{{Name: "Bad", Label: "x"}}},
		{name: "stub", caps: good.caps, creds: []CredentialField{{Name: "", Label: "x"}}},
		{name: "stub", caps: good.caps, creds: []CredentialField{
			{Name: "token", Label: "a", Secret: true},
			{Name: "token", Label: "b", Secret: true},
		}},
	} {
		if err := ValidateProvider(p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}

func TestCapabilitiesValid(t *testing.T) {
	for _, c := range []Capability{CapabilityOIDC, CapabilitySAML, CapabilityLDAP} {
		if !c.Valid() {
			t.Fatalf("capability %q invalid", c)
		}
	}
	if (Capability("telepathy")).Valid() {
		t.Fatal("unknown capability valid")
	}
}

func TestCredentialsGet(t *testing.T) {
	creds := Credentials{"api_token": "secret"}
	if got, err := creds.Get("api_token"); err != nil || got != "secret" {
		t.Fatalf("Get = %q %v", got, err)
	}
	for _, name := range []string{"missing", ""} {
		if _, err := creds.Get(name); err == nil {
			t.Fatalf("found %q", name)
		}
	}
	if _, err := (Credentials{"api_token": ""}).Get("api_token"); err == nil {
		t.Fatal("found empty value")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if len(r.Names()) != 0 {
		t.Fatalf("names = %v", r.Names())
	}
	a := stubProvider{name: "beta"}
	b := stubProvider{name: "alpha"}
	if err := r.Register(b); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if names := r.Names(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("names = %v", names)
	}
	if _, err := r.Get("alpha"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := r.Get("nope"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("err = %v", err)
	}
	if err := r.Register(a); err == nil || !strings.Contains(err.Error(), "duplicate provider") {
		t.Fatalf("err = %v", err)
	}
	if err := r.Register(stubProvider{}); err == nil {
		t.Fatal("registered invalid provider")
	}
}

func TestCheckConnection(t *testing.T) {
	up := stubProvider{name: "up"}
	if err := up.CheckConnection(context.Background(), "https://idp.lan", nil); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	want := errors.New("connection refused")
	down := stubProvider{name: "down", fail: want}
	if err := down.CheckConnection(context.Background(), "https://idp.lan", nil); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

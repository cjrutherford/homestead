// Package identity models optional external identity integrations
// (S202): the provider-neutral contract that later slices adapt to
// concrete providers (Authentik first) and drive from setup flows.
// Local administrator identity stays in internal/auth; this package
// never authenticates anyone itself — it describes what an external
// provider offers, what secrets its calls need, and whether it
// answers, so integrations stay provider-neutral by construction.
package identity

import (
	"context"
	"fmt"
	"regexp"
	"sort"
)

// Capability names what a provider can do for application
// integrations. The v1 set covers the protocols Homestead apps
// speak; new capabilities arrive with the slice that needs them.
type Capability string

const (
	// CapabilityOIDC integrates apps through OAuth2/OIDC.
	CapabilityOIDC Capability = "oidc"
	// CapabilitySAML integrates apps through SAML.
	CapabilitySAML Capability = "saml"
	// CapabilityLDAP serves directory lookups and binds through LDAP.
	CapabilityLDAP Capability = "ldap"
)

// Valid reports a known capability.
func (c Capability) Valid() bool {
	return c == CapabilityOIDC || c == CapabilitySAML || c == CapabilityLDAP
}

var validCredentialName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// CredentialField describes one secret or setting a provider needs,
// without holding values: values live in the secret store (S096) and
// arrive at call time in Credentials.
type CredentialField struct {
	Name   string
	Label  string
	Secret bool
}

// Validate checks the field shape.
func (f CredentialField) Validate() error {
	if !validCredentialName.MatchString(f.Name) {
		return fmt.Errorf("identity: credential name must match [a-z][a-z0-9_]*")
	}
	if f.Label == "" {
		return fmt.Errorf("identity: credential label must not be empty")
	}
	return nil
}

// Credentials carries provider secret values for one call. Values never
// persist here; the secret store (S096) owns storage.
type Credentials map[string]string

// Get returns one value or a missing-credential error naming the field.
func (c Credentials) Get(name string) (string, error) {
	value, ok := c[name]
	if !ok || value == "" {
		return "", fmt.Errorf("identity: missing credential %q", name)
	}
	return value, nil
}

// Provider is the identity integration contract: provider-neutral
// metadata plus one live call. Capabilities advertise the protocols
// a provider speaks for application integrations (S203 describes the
// application side); RequiredCredentials feeds secret forms without
// ever storing values; CheckConnection proves the endpoint answers
// with the given credentials before setup flows trust it.
type Provider interface {
	// Name identifies the provider: authentik, and later providers
	// register their own names.
	Name() string
	// Capabilities lists the protocols the provider speaks.
	Capabilities() []Capability
	// RequiredCredentials describes the secrets calls need.
	RequiredCredentials() []CredentialField
	// CheckConnection proves the provider endpoint answers with the
	// given credentials.
	CheckConnection(ctx context.Context, endpoint string, creds Credentials) error
}

// ValidateProvider checks contract metadata: a name, known
// capabilities, and valid, uniquely named credential fields.
func ValidateProvider(p Provider) error {
	if p.Name() == "" {
		return fmt.Errorf("identity: provider name must not be empty")
	}
	for _, c := range p.Capabilities() {
		if !c.Valid() {
			return fmt.Errorf("identity: unknown capability %q", c)
		}
	}
	seen := map[string]bool{}
	for _, f := range p.RequiredCredentials() {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Name] {
			return fmt.Errorf("identity: duplicate credential %q", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Builtins returns the identity providers this build ships, in name
// order. Setup flows seed a Registry from the same list; validation paths
// that run before a flow exists read it directly, so a manifest can never
// name a provider the appliance cannot drive (S341).
func Builtins() []Provider {
	return []Provider{NewAuthentik()}
}

// Registry maps provider names to implementations for setup flows
// (S204) to enumerate. Providers register themselves; the zero value
// is not usable, use NewRegistry.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider after validating its contract metadata.
// Duplicate names are errors, never silent replacement.
func (r *Registry) Register(p Provider) error {
	if err := ValidateProvider(p); err != nil {
		return err
	}
	if _, exists := r.providers[p.Name()]; exists {
		return fmt.Errorf("identity: duplicate provider %q", p.Name())
	}
	r.providers[p.Name()] = p
	return nil
}

// Get returns the named provider or an unknown-provider error.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("identity: unknown provider %q", name)
	}
	return p, nil
}

// Names returns registered provider names sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

package dns

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// ErrNoAPI reports API operations on providers without one (manual):
// their contract surface is instructions and propagation checks, and
// S092 implements those.
var ErrNoAPI = errors.New("dns: provider has no API")

// Capability names what a provider can do through its API. The v1 set
// stays small; new capabilities arrive with the slice that needs them.
type Capability string

const (
	// CapabilityRecords manages zone records through the provider API.
	CapabilityRecords Capability = "records"
	// CapabilityDNS01 serves ACME DNS-01 challenge values through the
	// provider API.
	CapabilityDNS01 Capability = "acme-dns01"
)

// Valid reports a known capability.
func (c Capability) Valid() bool {
	return c == CapabilityRecords || c == CapabilityDNS01
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
		return fmt.Errorf("dns: credential name must match [a-z][a-z0-9_]*")
	}
	if f.Label == "" {
		return fmt.Errorf("dns: credential label must not be empty")
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
		return "", fmt.Errorf("dns: missing credential %q", name)
	}
	return value, nil
}

// ChangeAction is the reconciliation verb for one record.
type ChangeAction string

const (
	// ChangeCreate adds a desired record absent from observed state.
	ChangeCreate ChangeAction = "create"
	// ChangeDelete removes an observed record absent from desired state.
	ChangeDelete ChangeAction = "delete"
)

// Change is one record-level reconciliation step.
type Change struct {
	Action ChangeAction
	Record Record
}

// DiffRecords compares the desired table against observed provider state
// as pure set math: desired-minus-observed creates, observed-minus-desired
// deletes (a TTL or value change surfaces as a delete/create pair, which
// matches provider change-batch APIs). Matching is exact on the full
// record, so round-robin repeats diff independently. Output sorts creates
// before deletes, then by name, type, and value, for stable previews.
// Diff is table math, not policy: deletes cover every observed record
// outside desired state, and the reconciliation layer (S098) decides what
// may actually be applied — providers must never widen that gate.
func DiffRecords(desired, observed []Record) []Change {
	inObserved := map[Record]bool{}
	for _, r := range observed {
		inObserved[r] = true
	}
	inDesired := map[Record]bool{}
	for _, r := range desired {
		inDesired[r] = true
	}
	var changes []Change
	for _, r := range desired {
		if !inObserved[r] {
			changes = append(changes, Change{Action: ChangeCreate, Record: r})
		}
	}
	for _, r := range observed {
		if !inDesired[r] {
			changes = append(changes, Change{Action: ChangeDelete, Record: r})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Action != changes[j].Action {
			return changes[i].Action == ChangeCreate
		}
		a, b := changes[i].Record, changes[j].Record
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Value < b.Value
	})
	return changes
}

// Provider is the public-DNS reconciliation contract (ADR-009): intent
// stays provider-neutral (S090) and each provider adapts it. Providers
// without an API return ErrNoAPI from the API methods.
type Provider interface {
	// Name identifies the provider: manual, cloudflare, route53,
	// digitalocean.
	Name() string
	// Capabilities lists what the provider can do through its API.
	Capabilities() []Capability
	// RequiredCredentials describes the secrets calls need.
	RequiredCredentials() []CredentialField
	// ListRecords returns the provider's current records for a zone,
	// normalized to intent records.
	ListRecords(ctx context.Context, zone string, creds Credentials) ([]Record, error)
	// ApplyChanges carries out record changes in order.
	ApplyChanges(ctx context.Context, zone string, creds Credentials, changes []Change) error
}

// ValidateProvider checks contract metadata: known capabilities and
// valid, uniquely named credential fields.
func ValidateProvider(p Provider) error {
	if p.Name() == "" {
		return fmt.Errorf("dns: provider name must not be empty")
	}
	for _, c := range p.Capabilities() {
		if !c.Valid() {
			return fmt.Errorf("dns: unknown capability %q", c)
		}
	}
	seen := map[string]bool{}
	for _, f := range p.RequiredCredentials() {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Name] {
			return fmt.Errorf("dns: duplicate credential %q", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Registry maps provider names to implementations for the setup wizard
// (S097) to enumerate. Providers register themselves; the zero value is
// not usable, use NewRegistry.
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
		return fmt.Errorf("dns: duplicate provider %q", p.Name())
	}
	r.providers[p.Name()] = p
	return nil
}

// Get returns the named provider or an unknown-provider error.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("dns: unknown provider %q", name)
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

package router

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// Mapping errors separate three meanings callers must not conflate:
// incapable (the provider cannot do this at all), disabled (automation
// exists but the owner never opted in), and absent (no such mapping).
var (
	ErrUnsupported = errors.New("router: provider does not support this")
	ErrNotEnabled  = errors.New("router: automated mapping not enabled")
	ErrNoMapping   = errors.New("router: no such mapping")
)

// Capability names what a mapping provider automates. Query coverage
// varies — NAT-PMP cannot enumerate — so callers check before asking.
type Capability string

const (
	// CapabilityMapping adds and removes port mappings.
	CapabilityMapping Capability = "mapping"
	// CapabilityQuery reads single mapping entries.
	CapabilityQuery Capability = "query"
)

// Valid reports a known capability.
func (c Capability) Valid() bool {
	return c == CapabilityMapping || c == CapabilityQuery
}

// Mapping is one NAT rule under automation: the public port and
// protocol the internet reaches, the internal client and port behind
// it, a lifetime, and an optional description for protocols carrying
// one. Lifetime 0 means the provider default; anything else bounds to
// a minute through thirty days so mappings refresh instead of
// fossilizing.
type Mapping struct {
	Protocol       string
	ExternalPort   int
	InternalClient netip.Addr
	InternalPort   int
	Lifetime       time.Duration
	Description    string
}

// Validate checks the mapping shape.
func (m Mapping) Validate() error {
	if m.Protocol != ProtocolTCP && m.Protocol != ProtocolUDP {
		return fmt.Errorf("router: protocol must be tcp or udp")
	}
	if m.ExternalPort < 1 || m.ExternalPort > 65535 {
		return fmt.Errorf("router: external port must be 1-65535")
	}
	if m.InternalPort < 1 || m.InternalPort > 65535 {
		return fmt.Errorf("router: internal port must be 1-65535")
	}
	if !m.InternalClient.IsValid() || m.InternalClient.IsUnspecified() || m.InternalClient.IsMulticast() {
		return fmt.Errorf("router: internal client must be usable")
	}
	if m.Lifetime != 0 && (m.Lifetime < time.Minute || m.Lifetime > 30*24*time.Hour) {
		return fmt.Errorf("router: lifetime must be 0 or 1 minute to 30 days")
	}
	if len(m.Description) > 128 {
		return fmt.Errorf("router: description must be at most 128 characters")
	}
	return nil
}

// Key identifies a mapping for deletion: protocol plus external port,
// the address every protocol routes deletes through.
func (m Mapping) Key() string {
	return fmt.Sprintf("%s/%d", m.Protocol, m.ExternalPort)
}

// Provider is the automated-mapping contract: add, remove, and read
// mappings behind an explicit opt-in. Disabled providers refuse
// AddMapping and DeleteMapping with ErrNotEnabled — automation without
// recorded consent never runs, no matter how reachable the gateway is.
type Provider interface {
	// Name identifies the provider: manual, pcp, nat-pmp, upnp.
	Name() string
	// Capabilities lists what the provider automates.
	Capabilities() []Capability
	// Enabled reports recorded owner opt-in for automation.
	Enabled() bool
	// AddMapping creates or refreshes one mapping.
	AddMapping(ctx context.Context, mapping Mapping) error
	// DeleteMapping removes one mapping by key.
	DeleteMapping(ctx context.Context, mapping Mapping) error
	// QueryMapping reads one mapping entry by key.
	QueryMapping(ctx context.Context, protocol string, externalPort int) (Mapping, error)
}

// ValidateProvider checks contract metadata: known capabilities only.
// Names stay free-form here; the registry refuses duplicates.
func ValidateProvider(p Provider) error {
	if p.Name() == "" {
		return fmt.Errorf("router: provider name must not be empty")
	}
	for _, capability := range p.Capabilities() {
		if !capability.Valid() {
			return fmt.Errorf("router: unknown capability %q", capability)
		}
	}
	return nil
}

// Manual is the human-operated mapping provider: instructions (S130)
// and queries (S132) exist, automation never does. Mapping calls fail
// incapable rather than disabled — there is nothing to opt into.
type Manual struct{}

// Name identifies the manual provider.
func (m Manual) Name() string { return "manual" }

// Capabilities reports no automation.
func (m Manual) Capabilities() []Capability { return nil }

// Enabled reports no automation to enable.
func (m Manual) Enabled() bool { return false }

// AddMapping has no automation to run.
func (m Manual) AddMapping(_ context.Context, _ Mapping) error { return ErrUnsupported }

// DeleteMapping has no automation to run.
func (m Manual) DeleteMapping(_ context.Context, _ Mapping) error { return ErrUnsupported }

// QueryMapping has no automation to run.
func (m Manual) QueryMapping(_ context.Context, _ string, _ int) (Mapping, error) {
	return Mapping{}, ErrUnsupported
}

// Registry maps provider names to implementations for setup flows to
// enumerate. Providers register themselves; the zero value is not
// usable, use NewRegistry.
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
		return fmt.Errorf("router: duplicate provider %q", p.Name())
	}
	r.providers[p.Name()] = p
	return nil
}

// Get returns the named provider or an unknown-provider error.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("router: unknown provider %q", name)
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

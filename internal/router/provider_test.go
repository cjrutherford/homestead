package router

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// stubRouterProvider proves the contract is implementable.
type stubRouterProvider struct {
	name         string
	capabilities []Capability
	enabled      bool
	mappings     map[string]Mapping
}

func (s *stubRouterProvider) Name() string               { return s.name }
func (s *stubRouterProvider) Capabilities() []Capability { return s.capabilities }
func (s *stubRouterProvider) Enabled() bool              { return s.enabled }
func (s *stubRouterProvider) AddMapping(_ context.Context, m Mapping) error {
	if !s.enabled {
		return ErrNotEnabled
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if s.mappings == nil {
		s.mappings = map[string]Mapping{}
	}
	s.mappings[m.Key()] = m
	return nil
}
func (s *stubRouterProvider) DeleteMapping(_ context.Context, m Mapping) error {
	if !s.enabled {
		return ErrNotEnabled
	}
	if _, ok := s.mappings[m.Key()]; !ok {
		return ErrNoMapping
	}
	delete(s.mappings, m.Key())
	return nil
}
func (s *stubRouterProvider) QueryMapping(_ context.Context, protocol string, port int) (Mapping, error) {
	for _, m := range s.mappings {
		if m.Protocol == protocol && m.ExternalPort == port {
			return m, nil
		}
	}
	return Mapping{}, ErrNoMapping
}

func validMapping() Mapping {
	return Mapping{
		Protocol: ProtocolTCP, ExternalPort: 80,
		InternalClient: netip.MustParseAddr("192.168.1.10"), InternalPort: 80,
		Lifetime: time.Hour, Description: "web",
	}
}

func TestMappingValidation(t *testing.T) {
	if err := validMapping().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validMapping().Key() != "tcp/80" {
		t.Fatal("key wrong")
	}
	quiet := validMapping()
	quiet.Lifetime = 0
	quiet.Description = ""
	if err := quiet.Validate(); err != nil {
		t.Fatalf("defaults Validate: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Mapping)
		want   string
	}{
		{"bad protocol", func(m *Mapping) { m.Protocol = "sctp" }, "tcp or udp"},
		{"bad ext port", func(m *Mapping) { m.ExternalPort = 0 }, "external port"},
		{"bad int port", func(m *Mapping) { m.InternalPort = 70000 }, "internal port"},
		{"bad client", func(m *Mapping) { m.InternalClient = netip.Addr{} }, "usable"},
		{"bad lifetime", func(m *Mapping) { m.Lifetime = time.Second }, "lifetime"},
		{"long description", func(m *Mapping) { m.Description = strings.Repeat("x", 129) }, "128"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping := validMapping()
			tc.mutate(&mapping)
			err := mapping.Validate()
			if err == nil {
				t.Fatal("accepted invalid mapping")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestProviderContract(t *testing.T) {
	var _ Provider = (*stubRouterProvider)(nil)
	var _ Provider = Manual{}
	stub := &stubRouterProvider{name: "stub", capabilities: []Capability{CapabilityMapping, CapabilityQuery}, enabled: true}
	if err := ValidateProvider(stub); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	if !stub.Enabled() {
		t.Fatal("not enabled")
	}
	ctx := context.Background()
	if err := stub.AddMapping(ctx, validMapping()); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	got, err := stub.QueryMapping(ctx, ProtocolTCP, 80)
	if err != nil || got.InternalPort != 80 {
		t.Fatalf("QueryMapping = %+v %v", got, err)
	}
	if err := stub.DeleteMapping(ctx, validMapping()); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	if _, err := stub.QueryMapping(ctx, ProtocolTCP, 80); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("err = %v", err)
	}
	if err := stub.DeleteMapping(ctx, validMapping()); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("err = %v", err)
	}
	// Disabled automation refuses with consent semantics.
	stub.enabled = false
	if err := stub.AddMapping(ctx, validMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := stub.DeleteMapping(ctx, validMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	// Bad metadata refuses registration.
	if err := ValidateProvider(&stubRouterProvider{}); err == nil {
		t.Fatal("validated empty name")
	}
	if err := ValidateProvider(&stubRouterProvider{name: "x", capabilities: []Capability{"teleport"}}); err == nil {
		t.Fatal("validated bad capability")
	}
}

func TestManualProvider(t *testing.T) {
	manual := Manual{}
	if manual.Name() != "manual" || len(manual.Capabilities()) != 0 || manual.Enabled() {
		t.Fatalf("manual = %+v", manual.Name())
	}
	ctx := context.Background()
	if err := manual.AddMapping(ctx, validMapping()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if err := manual.DeleteMapping(ctx, validMapping()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if _, err := manual.QueryMapping(ctx, ProtocolTCP, 80); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if err := ValidateProvider(manual); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
}

func TestProviderRegistry(t *testing.T) {
	r := NewRegistry()
	if len(r.Names()) != 0 {
		t.Fatalf("names = %v", r.Names())
	}
	if err := r.Register(Manual{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(&stubRouterProvider{name: "auto"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if names := r.Names(); len(names) != 2 || names[0] != "auto" || names[1] != "manual" {
		t.Fatalf("names = %v", names)
	}
	if _, err := r.Get("auto"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := r.Get("nope"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("err = %v", err)
	}
	if err := r.Register(Manual{}); err == nil || !strings.Contains(err.Error(), "duplicate provider") {
		t.Fatalf("err = %v", err)
	}
}

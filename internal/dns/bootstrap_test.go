package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/store"
)

// recordingProvider serves canned public records and records every
// apply call: split-DNS bootstrap must never make one.
type recordingProvider struct {
	records []Record
	err     error
	applies [][]Change
}

func (p *recordingProvider) Name() string { return "api" }
func (p *recordingProvider) Capabilities() []Capability {
	return []Capability{CapabilityRecords}
}
func (p *recordingProvider) RequiredCredentials() []CredentialField { return nil }
func (p *recordingProvider) ListRecords(_ context.Context, _ string, _ Credentials) ([]Record, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.records, nil
}
func (p *recordingProvider) ApplyChanges(_ context.Context, _ string, _ Credentials, changes []Change) error {
	p.applies = append(p.applies, changes)
	return nil
}

// localDouble serves AdGuard rewrites with real add semantics,
// recording every call.
func localDouble(t *testing.T) (*Client, *[]string, *[]map[string]string) {
	t.Helper()
	var calls []string
	var stored []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/control/rewrite/list", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "list")
		_ = json.NewEncoder(w).Encode(stored)
	})
	mux.HandleFunc("/control/rewrite/add", func(w http.ResponseWriter, r *http.Request) {
		var entry map[string]string
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			t.Fatalf("decode: %v", err)
		}
		calls = append(calls, "add "+entry["domain"])
		stored = append(stored, entry)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client, err := NewAdGuard(srv.URL, "", "")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	return client, &calls, &stored
}

func splitTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func splitNetwork(addrs ...discover.Interface) discover.Network {
	return discover.Network{Interfaces: addrs}
}

func lanIface(name string, ips ...string) discover.Interface {
	iface := discover.Interface{Name: name, Up: true}
	for _, ip := range ips {
		iface.Addresses = append(iface.Addresses, discover.Address{IP: ip})
	}
	return iface
}

func TestEstablishSplitDNSFullFlow(t *testing.T) {
	ctx := context.Background()
	local, _, stored := localDouble(t)
	provider := &recordingProvider{}
	s := splitTestStore(t)
	network := splitNetwork(lanIface("eth0", "192.168.1.10"))
	result, err := EstablishSplitDNS(ctx, s, local, provider, nil, "example.com", "home", network)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if result.Zone != "example.com" || result.FQDN != "home.example.com" || result.LAN != "192.168.1.10" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.LocalApplied) != 1 || result.LocalApplied[0].Domain != "home.example.com" ||
		result.LocalApplied[0].Answer != "192.168.1.10" {
		t.Fatalf("applied = %+v", result.LocalApplied)
	}
	if !result.PublicVerified || len(result.PublicHeld) != 0 {
		t.Fatalf("public = %+v", result)
	}
	if len(provider.applies) != 0 {
		t.Fatalf("public writes = %v", provider.applies)
	}
	if len(*stored) != 1 {
		t.Fatalf("rewrites = %v", *stored)
	}
	current, err := CurrentSplitDNS(s)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Zone != "example.com" || current.FQDN != "home.example.com" || current.LAN != "192.168.1.10" {
		t.Fatalf("current = %+v", current)
	}
	// Re-running converges: present answers stay, nothing re-adds.
	before := len(*stored)
	again, err := EstablishSplitDNS(ctx, s, local, provider, nil, "example.com", "home", network)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if len(again.LocalApplied) != 0 || len(*stored) != before {
		t.Fatalf("re-run applied = %+v (stored %v)", again.LocalApplied, *stored)
	}
}

func TestEstablishSplitDNSHoldsPublic(t *testing.T) {
	ctx := context.Background()
	local, _, _ := localDouble(t)
	provider := &recordingProvider{records: []Record{
		{Name: "home", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		{Name: "www", Type: RecordA, Value: "203.0.113.8", TTLSeconds: 300},
	}}
	s := splitTestStore(t)
	network := splitNetwork(lanIface("eth0", "192.168.1.10"))
	result, err := EstablishSplitDNS(ctx, s, local, provider, nil, "example.com", "home", network)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	// Pre-existing public records hold as deletes; nothing applies.
	if len(result.PublicHeld) != 2 || !result.PublicVerified {
		t.Fatalf("public = %+v", result)
	}
	if len(provider.applies) != 0 {
		t.Fatalf("public writes = %v", provider.applies)
	}
	// Local answers still establish under held public state.
	if len(result.LocalApplied) != 1 {
		t.Fatalf("applied = %+v", result.LocalApplied)
	}
}

func TestEstablishSplitDNSManual(t *testing.T) {
	ctx := context.Background()
	local, _, stored := localDouble(t)
	s := splitTestStore(t)
	network := splitNetwork(lanIface("eth0", "192.168.1.10"))
	result, err := EstablishSplitDNS(ctx, s, local, Manual{}, nil, "example.com", "home", network)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if result.PublicVerified {
		t.Fatalf("manual public verified: %+v", result)
	}
	if len(result.LocalApplied) != 1 || len(*stored) != 1 {
		t.Fatalf("local = %+v (stored %v)", result.LocalApplied, *stored)
	}
}

func TestEstablishSplitDNSRefusals(t *testing.T) {
	ctx := context.Background()
	local, _, _ := localDouble(t)
	provider := &recordingProvider{}
	s := splitTestStore(t)
	good := splitNetwork(lanIface("eth0", "192.168.1.10"))
	cases := []struct {
		name     string
		st       *store.Store
		local    *Client
		provider Provider
		zone     string
		base     string
		network  discover.Network
	}{
		{"nil store", nil, local, provider, "example.com", "home", good},
		{"nil local", s, nil, provider, "example.com", "home", good},
		{"nil provider", s, local, nil, "example.com", "home", good},
		{"bad zone", s, local, provider, "Bad_Zone!", "home", good},
		{"bad base", s, local, provider, "example.com", "Bad", good},
		{"no LAN", s, local, provider, "example.com", "home", discover.Network{}},
		{"link-local only", s, local, provider, "example.com", "home",
			splitNetwork(lanIface("eth0", "169.254.5.5"))},
	}
	for _, tc := range cases {
		if _, err := EstablishSplitDNS(ctx, tc.st, tc.local, tc.provider, nil, tc.zone, tc.base, tc.network); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
	// Refusals record nothing.
	if _, err := CurrentSplitDNS(s); err != ErrNoSplitDNS {
		t.Fatalf("Current = %v", err)
	}
}

func TestApplianceLANPrefersDefault(t *testing.T) {
	network := discover.Network{
		DefaultInterface: "eth1",
		Interfaces: []discover.Interface{
			lanIface("eth0", "192.168.1.9"),
			lanIface("eth1", "10.0.0.5"),
		},
	}
	lan, err := applianceLAN(network)
	if err != nil {
		t.Fatalf("applianceLAN: %v", err)
	}
	if lan != "10.0.0.5" {
		t.Fatalf("lan = %q", lan)
	}
	// Without a default, index order wins; loopback never answers.
	network = discover.Network{Interfaces: []discover.Interface{
		{Name: "lo", Up: true, Loopback: true, Addresses: []discover.Address{{IP: "127.0.0.1"}}},
		lanIface("eth0", "192.168.1.9"),
	}}
	lan, err = applianceLAN(network)
	if err != nil || lan != "192.168.1.9" {
		t.Fatalf("lan = %q, err = %v", lan, err)
	}
}

func TestCurrentSplitDNSEmpty(t *testing.T) {
	if _, err := CurrentSplitDNS(splitTestStore(t)); err != ErrNoSplitDNS {
		t.Fatalf("Current = %v", err)
	}
}

func TestEstablishSplitDNSProviderFailure(t *testing.T) {
	ctx := context.Background()
	local, _, _ := localDouble(t)
	provider := &recordingProvider{err: errors.New("denied")}
	s := splitTestStore(t)
	network := splitNetwork(lanIface("eth0", "192.168.1.10"))
	if _, err := EstablishSplitDNS(ctx, s, local, provider, nil, "example.com", "home", network); err == nil ||
		!strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
}

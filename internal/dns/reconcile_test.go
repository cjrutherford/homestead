package dns

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func splitZone() Zone {
	return Zone{
		Name: "example.com",
		Records: []Record{
			{Name: "app", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
			{Name: "docs", Type: RecordCNAME, Value: "example.com"},
			{Name: "v6", Type: RecordAAAA, Value: "2001:db8::1"},
		},
		Views: []View{
			{Name: "lan", Audience: AudienceLocal, Records: []Record{
				{Name: "app", Type: RecordA, Value: "192.168.1.10"},
			}},
			{Name: "internet", Audience: AudiencePublic, Records: []Record{
				{Name: "app", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
			}},
		},
	}
}

func TestEffectiveRecords(t *testing.T) {
	local, err := EffectiveRecords(splitZone(), AudienceLocal)
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	// View replaces the app key; baseline docs and v6 stand.
	if len(local) != 3 {
		t.Fatalf("local = %+v", local)
	}
	for _, r := range local {
		if r.Name == "app" && r.Value != "192.168.1.10" {
			t.Fatalf("app = %+v", r)
		}
		if r.Name == "app" && r.TTLSeconds != 0 {
			t.Fatalf("view TTL leaked: %+v", r)
		}
	}
	// Overlay has no view: baseline serves everywhere.
	overlay, err := EffectiveRecords(splitZone(), AudienceOverlay)
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	if len(overlay) != 3 {
		t.Fatalf("overlay = %+v", overlay)
	}
	if _, err := EffectiveRecords(splitZone(), Audience("dmz")); err == nil {
		t.Fatal("accepted bad audience")
	}
	bad := splitZone()
	bad.Name = "BAD"
	if _, err := EffectiveRecords(bad, AudienceLocal); err == nil {
		t.Fatal("merged invalid zone")
	}
}

func TestSplitChanges(t *testing.T) {
	changes := []Change{
		{Action: ChangeCreate, Record: Record{Name: "n", Type: RecordA, Value: "10.0.0.1"}},
		{Action: ChangeDelete, Record: Record{Name: "o", Type: RecordA, Value: "10.0.0.2"}},
	}
	apply, held := SplitChanges(changes, false)
	if len(apply) != 1 || len(held) != 1 || held[0].Action != ChangeDelete {
		t.Fatalf("gated = %+v %+v", apply, held)
	}
	apply, held = SplitChanges(changes, true)
	if len(apply) != 2 || len(held) != 0 {
		t.Fatalf("open = %+v %+v", apply, held)
	}
}

// scriptProvider records what reconcile applies.
type scriptProvider struct {
	records []Record
	applied []Change
	listErr error
}

func (s *scriptProvider) Name() string                           { return "script" }
func (s *scriptProvider) Capabilities() []Capability             { return []Capability{CapabilityRecords} }
func (s *scriptProvider) RequiredCredentials() []CredentialField { return nil }
func (s *scriptProvider) ListRecords(_ context.Context, _ string, _ Credentials) ([]Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.records, nil
}
func (s *scriptProvider) ApplyChanges(_ context.Context, _ string, _ Credentials, changes []Change) error {
	s.applied = append(s.applied, changes...)
	return nil
}

func TestReconcileGated(t *testing.T) {
	provider := &scriptProvider{records: []Record{
		{Name: "app", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		{Name: "stale", Type: RecordA, Value: "203.0.113.99", TTLSeconds: 300},
	}}
	summary, err := Reconcile(context.Background(), provider, splitZone(), nil, AudiencePublic, false)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if summary.Audience != AudiencePublic {
		t.Fatalf("summary = %+v", summary)
	}
	// Public effective: app (TTL 300), docs, v6. Observed app matches;
	// docs+v6 create; stale holds.
	if len(summary.Held) != 1 || summary.Held[0].Record.Name != "stale" {
		t.Fatalf("held = %+v", summary.Held)
	}
	for _, change := range summary.Applied {
		if change.Record.Name == "stale" {
			t.Fatalf("stale applied: %+v", summary.Applied)
		}
	}
	if len(provider.applied) != len(summary.Applied) {
		t.Fatalf("applied = %+v", provider.applied)
	}
}

func TestReconcileOpenAndEmpty(t *testing.T) {
	provider := &scriptProvider{records: []Record{
		{Name: "app", Type: RecordA, Value: "192.168.1.10"},
		{Name: "docs", Type: RecordCNAME, Value: "example.com"},
		{Name: "v6", Type: RecordAAAA, Value: "2001:db8::1"},
	}}
	summary, err := Reconcile(context.Background(), provider, splitZone(), nil, AudienceLocal, true)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(summary.Applied) != 0 || len(summary.Held) != 0 {
		t.Fatalf("converged run changed state: %+v", summary)
	}
	if len(provider.applied) != 0 {
		t.Fatalf("empty apply posted: %+v", provider.applied)
	}
}

func TestReconcileErrors(t *testing.T) {
	provider := &scriptProvider{listErr: errors.New("denied")}
	if _, err := Reconcile(context.Background(), provider, splitZone(), nil, AudiencePublic, false); err == nil ||
		!strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
	good := &scriptProvider{}
	bad := splitZone()
	bad.Name = "BAD"
	if _, err := Reconcile(context.Background(), good, bad, nil, AudiencePublic, false); err == nil {
		t.Fatal("reconciled invalid zone")
	}
}

func TestEffectiveRewrites(t *testing.T) {
	local, err := EffectiveRecords(splitZone(), AudienceLocal)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	rewrites, err := EffectiveRewrites("example.com", local)
	if err != nil {
		t.Fatalf("rewrites: %v", err)
	}
	// app A and v6 AAAA map; docs CNAME stays out.
	want := map[Rewrite]bool{
		{Domain: "app.example.com", Answer: "192.168.1.10"}: true,
		{Domain: "v6.example.com", Answer: "2001:db8::1"}:   true,
	}
	if len(rewrites) != len(want) {
		t.Fatalf("rewrites = %+v", rewrites)
	}
	for _, rw := range rewrites {
		if !want[rw] {
			t.Fatalf("unexpected rewrite %+v", rw)
		}
	}
	if _, err := EffectiveRewrites("BAD", local); err == nil {
		t.Fatal("rewrote bad zone")
	}
}

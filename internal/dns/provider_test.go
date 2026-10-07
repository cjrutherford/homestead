package dns

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func rec(name string, typ RecordType, value string) Record {
	return Record{Name: name, Type: typ, Value: value, TTLSeconds: 300}
}

func TestDiffRecords(t *testing.T) {
	keep := rec("app", RecordA, "192.168.1.10")
	changedOld := rec("www", RecordA, "192.168.1.10")
	changedNew := rec("www", RecordA, "192.168.1.11")
	stale := rec("old", RecordA, "192.168.1.99")
	fresh := rec("new", RecordA, "192.168.1.12")
	desired := []Record{keep, changedNew, fresh}
	observed := []Record{keep, changedOld, stale}
	got := DiffRecords(desired, observed)
	want := []Change{
		{Action: ChangeCreate, Record: fresh},
		{Action: ChangeCreate, Record: changedNew},
		{Action: ChangeDelete, Record: stale},
		{Action: ChangeDelete, Record: changedOld},
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("change %d = %+v, want %+v (all %+v)", i, got[i], want[i], got)
		}
	}
}

func TestDiffRecordsEdgeCases(t *testing.T) {
	if got := DiffRecords(nil, nil); len(got) != 0 {
		t.Fatalf("empty = %+v", got)
	}
	same := []Record{rec("a", RecordA, "10.0.0.1")}
	if got := DiffRecords(same, same); len(got) != 0 {
		t.Fatalf("identical = %+v", got)
	}
	// Round-robin repeats diff independently: one extra observed address
	// deletes alone, one extra desired address creates alone.
	desired := []Record{rec("app", RecordA, "10.0.0.1"), rec("app", RecordA, "10.0.0.2")}
	observed := []Record{rec("app", RecordA, "10.0.0.1"), rec("app", RecordA, "10.0.0.3")}
	got := DiffRecords(desired, observed)
	if len(got) != 2 || got[0].Action != ChangeCreate || got[1].Action != ChangeDelete {
		t.Fatalf("round-robin = %+v", got)
	}
	// TTL-only drift surfaces as a delete/create pair.
	ttlOld := Record{Name: "app", Type: RecordA, Value: "10.0.0.1", TTLSeconds: 300}
	ttlNew := Record{Name: "app", Type: RecordA, Value: "10.0.0.1", TTLSeconds: 600}
	got = DiffRecords([]Record{ttlNew}, []Record{ttlOld})
	if len(got) != 2 || got[0].Record.TTLSeconds != 600 || got[1].Record.TTLSeconds != 300 {
		t.Fatalf("ttl drift = %+v", got)
	}
}

// stubProvider proves the contract is implementable and exercises
// validation; S092+ carry the real ones.
type stubProvider struct {
	name  string
	caps  []Capability
	creds []CredentialField
}

func (s stubProvider) Name() string                           { return s.name }
func (s stubProvider) Capabilities() []Capability             { return s.caps }
func (s stubProvider) RequiredCredentials() []CredentialField { return s.creds }
func (s stubProvider) ListRecords(_ context.Context, _ string, _ Credentials) ([]Record, error) {
	return nil, ErrNoAPI
}
func (s stubProvider) ApplyChanges(_ context.Context, _ string, _ Credentials, _ []Change) error {
	return ErrNoAPI
}

func TestValidateProvider(t *testing.T) {
	var _ Provider = stubProvider{}
	good := stubProvider{
		name:  "stub",
		caps:  []Capability{CapabilityRecords},
		creds: []CredentialField{{Name: "api_token", Label: "API token", Secret: true}},
	}
	if err := ValidateProvider(good); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	for _, p := range []stubProvider{
		{name: "", caps: good.caps, creds: good.creds},
		{name: "stub", caps: []Capability{"telepathy"}, creds: good.creds},
		{name: "stub", caps: good.caps, creds: []CredentialField{{Name: "Bad", Label: "x"}}},
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

func TestErrNoAPI(t *testing.T) {
	p := stubProvider{name: "stub"}
	if _, err := p.ListRecords(context.Background(), "lan", nil); !errors.Is(err, ErrNoAPI) {
		t.Fatalf("err = %v", err)
	}
}

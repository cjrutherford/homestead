package cert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"homestead/internal/dns"
)

func TestChallengeRecords(t *testing.T) {
	got, err := ChallengeRecords("example.com", map[string]string{
		"example.com":        "token-apex",
		"www.example.com":    "token-www",
		"*.apps.example.com": "token-wild",
	})
	if err != nil {
		t.Fatalf("ChallengeRecords: %v", err)
	}
	want := map[string]string{
		"_acme-challenge":      "token-apex",
		"_acme-challenge.www":  "token-www",
		"_acme-challenge.apps": "token-wild",
	}
	if len(got) != len(want) {
		t.Fatalf("records = %+v", got)
	}
	for _, r := range got {
		if r.Type != dns.RecordTXT || r.TTLSeconds != 60 {
			t.Fatalf("record = %+v", r)
		}
		if want[r.Name] != r.Value {
			t.Fatalf("record = %+v", r)
		}
	}
	for _, args := range []struct {
		name  string
		zone  string
		authz map[string]string
		want  string
	}{
		{"empty", "example.com", nil, "must not be empty"},
		{"bad name", "example.com", map[string]string{"BAD": "t"}, "lowercase"},
		{"outside zone", "example.com", map[string]string{"other.com": "t"}, "outside zone"},
		{"empty token", "example.com", map[string]string{"example.com": ""}, "challenge for"},
	} {
		t.Run(args.name, func(t *testing.T) {
			_, err := ChallengeRecords(args.zone, args.authz)
			if err == nil {
				t.Fatal("accepted bad authorizations")
			}
			if !strings.Contains(err.Error(), args.want) {
				t.Fatalf("error %q, want %q", err, args.want)
			}
		})
	}
}

// scriptDNSProvider is a controllable dns.Provider: scripted listing,
// recorded apply.
type scriptDNSProvider struct {
	records []dns.Record
	applied []dns.Change
	listErr error
	fields  []dns.CredentialField
}

func (s *scriptDNSProvider) Name() string { return "script" }
func (s *scriptDNSProvider) Capabilities() []dns.Capability {
	return []dns.Capability{dns.CapabilityDNS01}
}
func (s *scriptDNSProvider) RequiredCredentials() []dns.CredentialField { return s.fields }
func (s *scriptDNSProvider) ListRecords(_ context.Context, _ string, _ dns.Credentials) ([]dns.Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.records, nil
}
func (s *scriptDNSProvider) ApplyChanges(_ context.Context, _ string, _ dns.Credentials, changes []dns.Change) error {
	s.applied = append(s.applied, changes...)
	for _, change := range changes {
		switch change.Action {
		case dns.ChangeCreate:
			s.records = append(s.records, change.Record)
		case dns.ChangeDelete:
			kept := s.records[:0]
			for _, r := range s.records {
				if r != change.Record {
					kept = append(kept, r)
				}
			}
			s.records = kept
		}
	}
	return nil
}

func challengePlacer(records []dns.Record) (Placer, *scriptDNSProvider) {
	provider := &scriptDNSProvider{records: records}
	return Placer{Provider: provider, Zone: "example.com"}, provider
}

func TestPlaceCreatesOnly(t *testing.T) {
	spf := dns.Record{Name: "@", Type: dns.RecordTXT, Value: "v=spf1 -all", TTLSeconds: 300}
	placer, provider := challengePlacer([]dns.Record{spf})
	challenges, err := ChallengeRecords("example.com", map[string]string{"example.com": "token"})
	if err != nil {
		t.Fatalf("ChallengeRecords: %v", err)
	}
	if err := placer.Place(context.Background(), challenges); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if len(provider.applied) != 1 || provider.applied[0].Action != dns.ChangeCreate {
		t.Fatalf("applied = %+v", provider.applied)
	}
	// Replacing converges to nothing: already present is no apply.
	provider.applied = nil
	if err := placer.Place(context.Background(), challenges); err != nil {
		t.Fatalf("re-place: %v", err)
	}
	if len(provider.applied) != 0 {
		t.Fatalf("re-place applied = %+v", provider.applied)
	}
	if err := (Placer{}).Place(context.Background(), challenges); err == nil {
		t.Fatal("placed without provider")
	}
}

func TestCleanupExact(t *testing.T) {
	spf := dns.Record{Name: "@", Type: dns.RecordTXT, Value: "v=spf1 -all", TTLSeconds: 300}
	token := dns.Record{Name: "_acme-challenge", Type: dns.RecordTXT, Value: "token", TTLSeconds: 60}
	placer, provider := challengePlacer([]dns.Record{spf, token})
	if err := placer.Cleanup(context.Background(), []dns.Record{token}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(provider.applied) != 1 || provider.applied[0].Action != dns.ChangeDelete {
		t.Fatalf("applied = %+v", provider.applied)
	}
	// Absent values skip; foreign records untouched.
	provider.applied = nil
	ghost := dns.Record{Name: "_acme-challenge", Type: dns.RecordTXT, Value: "gone", TTLSeconds: 60}
	if err := placer.Cleanup(context.Background(), []dns.Record{ghost, spf}); err != nil {
		t.Fatalf("cleanup foreign: %v", err)
	}
	// spf IS present and requested: it deletes (caller asked explicitly).
	if len(provider.applied) != 1 || provider.applied[0].Record.Value != "v=spf1 -all" {
		t.Fatalf("applied = %+v", provider.applied)
	}
	// Nothing requested present: no apply.
	provider.applied = nil
	if err := placer.Cleanup(context.Background(), []dns.Record{ghost}); err != nil {
		t.Fatalf("cleanup ghost: %v", err)
	}
	if len(provider.applied) != 0 {
		t.Fatalf("applied = %+v", provider.applied)
	}
}

func TestAwaitPropagation(t *testing.T) {
	records := []dns.Record{{Name: "_acme-challenge", Type: dns.RecordTXT, Value: "token", TTLSeconds: 60}}
	calls := 0
	flipping := func(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("SERVFAIL")
		}
		return []string{"token"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := AwaitPropagation(ctx, flipping, "example.com", records, time.Millisecond); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
	never := func(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
		return nil, errors.New("SERVFAIL")
	}
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	err := AwaitPropagation(short, never, "example.com", records, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not propagated") {
		t.Fatalf("err = %v", err)
	}
	if err := AwaitPropagation(ctx, nil, "example.com", records, time.Millisecond); err == nil {
		t.Fatal("accepted nil probe")
	}
	if err := AwaitPropagation(ctx, flipping, "example.com", records, 0); err == nil {
		t.Fatal("accepted zero interval")
	}
}

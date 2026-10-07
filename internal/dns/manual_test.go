package dns

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func publicZone() Zone {
	return Zone{
		Name: "example.com",
		Records: []Record{
			{Name: "internal", Type: RecordA, Value: "192.168.1.10"},
		},
		Views: []View{
			{Name: "lan", Audience: AudienceLocal, Records: []Record{
				{Name: "internal", Type: RecordA, Value: "192.168.1.10"},
			}},
			{Name: "internet", Audience: AudiencePublic, Records: []Record{
				{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
				{Name: "www", Type: RecordCNAME, Value: "example.com"},
				{Name: "_acme-challenge", Type: RecordTXT, Value: "token"},
			}},
		},
	}
}

func TestManualContract(t *testing.T) {
	var _ Provider = Manual{}
	m := Manual{}
	if m.Name() != "manual" {
		t.Fatalf("name = %q", m.Name())
	}
	if len(m.Capabilities()) != 0 || len(m.RequiredCredentials()) != 0 {
		t.Fatalf("manual carries automation: %+v", m)
	}
	if _, err := m.ListRecords(context.Background(), "example.com", nil); !errors.Is(err, ErrNoAPI) {
		t.Fatalf("list err = %v", err)
	}
	if err := m.ApplyChanges(context.Background(), "example.com", nil, nil); !errors.Is(err, ErrNoAPI) {
		t.Fatalf("apply err = %v", err)
	}
	r := NewRegistry()
	if err := r.Register(m); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestManualPublicRecords(t *testing.T) {
	m := Manual{}
	got, err := m.PublicRecords(publicZone())
	if err != nil {
		t.Fatalf("PublicRecords: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("records = %+v", got)
	}
	for _, r := range got {
		if r.Value == "192.168.1.10" {
			t.Fatalf("LAN answer leaked: %+v", got)
		}
	}
	bare := Zone{Name: "example.com"}
	if _, err := m.PublicRecords(bare); err == nil ||
		!strings.Contains(err.Error(), "no public view") {
		t.Fatalf("err = %v", err)
	}
	bad := publicZone()
	bad.Name = "BAD"
	if _, err := m.PublicRecords(bad); err == nil {
		t.Fatal("accepted invalid zone")
	}
}

func TestRenderZoneFile(t *testing.T) {
	m := Manual{}
	pub, err := m.PublicRecords(publicZone())
	if err != nil {
		t.Fatalf("PublicRecords: %v", err)
	}
	text, err := RenderZoneFile("example.com", pub)
	if err != nil {
		t.Fatalf("RenderZoneFile: %v", err)
	}
	for _, want := range []string{
		"$ORIGIN example.com.",
		"@ 300 IN A 203.0.113.7",
		"www 300 IN CNAME example.com.",
		`_acme-challenge 300 IN TXT "token"`,
		"Create these records exactly",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "192.168.1.10") {
		t.Fatalf("LAN leaked:\n%s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	seen := map[string]bool{}
	order := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, ";") || strings.HasPrefix(line, "$") {
			continue
		}
		name := strings.Fields(line)[0]
		if seen[name] {
			t.Fatalf("duplicate row %q", name)
		}
		seen[name] = true
		order = append(order, name)
	}
	if len(order) != 3 || order[0] != "@" || order[1] != "_acme-challenge" || order[2] != "www" {
		t.Fatalf("order = %v", order)
	}
	if _, err := RenderZoneFile("BAD", pub); err == nil {
		t.Fatal("rendered bad zone")
	}
	if _, err := RenderZoneFile("example.com", []Record{{Name: "x", Type: "MX", Value: "y"}}); err == nil {
		t.Fatal("rendered invalid record")
	}
}

func fakeProbe(answers map[string][]string, fail map[string]bool) ProbeFunc {
	return func(_ context.Context, name string, _ RecordType) ([]string, error) {
		if fail[name] {
			return nil, errors.New("SERVFAIL")
		}
		return answers[name], nil
	}
}

func TestVerifyRecords(t *testing.T) {
	m := Manual{}
	pub, err := m.PublicRecords(publicZone())
	if err != nil {
		t.Fatalf("PublicRecords: %v", err)
	}
	ctx := context.Background()
	full := map[string][]string{
		"example.com":                 {"203.0.113.7"},
		"www.example.com":             {"example.com."},
		"_acme-challenge.example.com": {"token"},
	}
	if err := VerifyRecords(ctx, fakeProbe(full, nil), "example.com", pub); err != nil {
		t.Fatalf("full: %v", err)
	}
	partial := map[string][]string{"example.com": {"203.0.113.7"}}
	err = VerifyRecords(ctx, fakeProbe(partial, nil), "example.com", pub)
	if err == nil || !strings.Contains(err.Error(), "2 record(s) not propagated") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "www") || !strings.Contains(err.Error(), "_acme-challenge") {
		t.Fatalf("gaps missing:\n%v", err)
	}
	if err := VerifyRecords(ctx, fakeProbe(nil, map[string]bool{"example.com": true}), "example.com", pub); err == nil ||
		!strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("err = %v", err)
	}
	// TTL drift is unobservable through caching: different TTL still passes.
	ttlShifted := append([]Record(nil), pub...)
	if err := VerifyRecords(ctx, fakeProbe(full, nil), "example.com", ttlShifted); err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if err := VerifyRecords(ctx, nil, "example.com", pub); err == nil {
		t.Fatal("accepted nil probe")
	}
	if err := VerifyRecords(ctx, fakeProbe(full, nil), "BAD", pub); err == nil {
		t.Fatal("accepted bad zone")
	}
}

func TestProbeViaUnreachable(t *testing.T) {
	probe := ProbeVia("127.0.0.1:1")
	if _, err := probe(context.Background(), "example.com", RecordA); err == nil {
		t.Fatal("probed via closed port")
	}
	if _, err := probe(context.Background(), "example.com", RecordType("MX")); err == nil {
		t.Fatal("probed bad type")
	}
}

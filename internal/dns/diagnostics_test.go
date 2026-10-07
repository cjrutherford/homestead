package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func diagRecords() []Record {
	return []Record{
		{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		{Name: "www", Type: RecordCNAME, Value: "example.com"},
	}
}

func TestDiagnose(t *testing.T) {
	ctx := context.Background()
	probe := func(_ context.Context, name string, _ RecordType) ([]string, error) {
		switch name {
		case "example.com":
			return []string{"203.0.113.7"}, nil
		case "www.example.com":
			return []string{"other.example.com."}, nil
		default:
			return nil, errors.New("unreachable")
		}
	}
	report, err := Diagnose(ctx, probe, "example.com", diagRecords())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if report.Zone != "example.com" || len(report.Findings) != 2 {
		t.Fatalf("report = %+v", report)
	}
	apex := report.Findings[0]
	if apex.Status != FindingOK || apex.FQDN != "example.com" {
		t.Fatalf("apex = %+v", apex)
	}
	if !strings.Contains(apex.Detail, "resolves to") {
		t.Fatalf("detail = %q", apex.Detail)
	}
	www := report.Findings[1]
	if www.Status != FindingFail || !strings.Contains(www.Detail, "wrong view") {
		t.Fatalf("www = %+v", www)
	}
	if report.Overall() != FindingFail {
		t.Fatalf("overall = %q", report.Overall())
	}
}

func TestDiagnoseClassifies(t *testing.T) {
	ctx := context.Background()
	nxdomain := &net.DNSError{Err: "no such host", Name: "gone.example.com", IsNotFound: true}
	timeout := &net.DNSError{Err: "i/o timeout", Name: "slow.example.com", IsTimeout: true}
	probe := func(_ context.Context, name string, _ RecordType) ([]string, error) {
		switch name {
		case "gone.example.com":
			return nil, nxdomain
		case "slow.example.com":
			return nil, timeout
		case "empty.example.com":
			return []string{}, nil
		default:
			return nil, errors.New("SERVFAIL")
		}
	}
	desired := []Record{
		{Name: "gone", Type: RecordA, Value: "10.0.0.1"},
		{Name: "slow", Type: RecordA, Value: "10.0.0.2"},
		{Name: "empty", Type: RecordA, Value: "10.0.0.3"},
		{Name: "broken", Type: RecordA, Value: "10.0.0.4"},
	}
	report, err := Diagnose(ctx, probe, "example.com", desired)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	want := []struct {
		status string
		detail string
	}{
		{FindingFail, "NXDOMAIN"},
		{FindingWarn, "timed out"},
		{FindingFail, "no A records"},
		{FindingWarn, "SERVFAIL"},
	}
	for i, w := range want {
		got := report.Findings[i]
		if got.Status != w.status || !strings.Contains(got.Detail, w.detail) {
			t.Fatalf("finding %d = %+v, want %s/%q", i, got, w.status, w.detail)
		}
	}
	if report.Overall() != FindingFail {
		t.Fatalf("overall = %q", report.Overall())
	}
	warnOnly, err := Diagnose(ctx, func(_ context.Context, _ string, _ RecordType) ([]string, error) {
		return nil, timeout
	}, "example.com", desired[:1])
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if warnOnly.Overall() != FindingWarn {
		t.Fatalf("overall = %q", warnOnly.Overall())
	}
	empty, err := Diagnose(ctx, probe, "example.com", nil)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if empty.Overall() != FindingOK || len(empty.Findings) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestDiagnoseRejects(t *testing.T) {
	ctx := context.Background()
	probe := func(_ context.Context, _ string, _ RecordType) ([]string, error) {
		return nil, nil
	}
	if _, err := Diagnose(ctx, nil, "example.com", diagRecords()); err == nil {
		t.Fatal("accepted nil probe")
	}
	if _, err := Diagnose(ctx, probe, "BAD", diagRecords()); err == nil {
		t.Fatal("accepted bad zone")
	}
	bad := []Record{{Name: "x", Type: "MX", Value: "y"}}
	if _, err := Diagnose(ctx, probe, "example.com", bad); err == nil {
		t.Fatal("accepted bad record")
	}
}

func TestVerifyView(t *testing.T) {
	zone := Zone{
		Name: "example.com",
		Records: []Record{
			{Name: "app", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		},
		Views: []View{
			{Name: "lan", Audience: AudienceLocal, Records: []Record{
				{Name: "app", Type: RecordA, Value: "192.168.1.10"},
			}},
		},
	}
	probe := func(_ context.Context, name string, _ RecordType) ([]string, error) {
		if name == "app.example.com" {
			return []string{"192.168.1.10"}, nil
		}
		return nil, errors.New("unexpected " + name)
	}
	report, err := VerifyView(context.Background(), probe, zone, AudienceLocal)
	if err != nil {
		t.Fatalf("VerifyView: %v", err)
	}
	if report.Audience != AudienceLocal || report.Overall() != FindingOK {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Findings) != 1 || report.Findings[0].Record.Value != "192.168.1.10" {
		t.Fatalf("findings = %+v", report.Findings)
	}
	// The public audience expects baseline, but the probe answers the
	// LAN value: a mismatch naming the wrong view.
	public, err := VerifyView(context.Background(), probe, zone, AudiencePublic)
	if err != nil {
		t.Fatalf("VerifyView: %v", err)
	}
	if public.Overall() != FindingFail ||
		!strings.Contains(public.Findings[0].Detail, "wrong view") {
		t.Fatalf("public = %+v", public)
	}
	if _, err := VerifyView(context.Background(), probe, zone, Audience("dmz")); err == nil {
		t.Fatal("verified bad audience")
	}
}

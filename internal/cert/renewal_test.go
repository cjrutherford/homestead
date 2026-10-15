package cert

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/store"
)

func TestFingerprintNormalization(t *testing.T) {
	base := NewFingerprint([]string{"www.example.com", "example.com"}, "cloudflare", "dns-01")
	same := []Fingerprint{
		NewFingerprint([]string{"example.com", "www.example.com"}, "cloudflare", "dns-01"),
		NewFingerprint([]string{"EXAMPLE.com.", "WWW.Example.COM"}, "cloudflare", "dns-01"),
		NewFingerprint([]string{"example.com", "www.example.com", "example.com"}, "cloudflare", "dns-01"),
	}
	for i, fp := range same {
		if !base.Equal(fp) {
			t.Errorf("variant %d should equal base: %s vs %s", i, fp, base)
		}
	}
	different := map[string]Fingerprint{
		"subset":    NewFingerprint([]string{"example.com"}, "cloudflare", "dns-01"),
		"superset":  NewFingerprint([]string{"example.com", "www.example.com", "a.example.com"}, "cloudflare", "dns-01"),
		"provider":  NewFingerprint([]string{"example.com", "www.example.com"}, "route53", "dns-01"),
		"challenge": NewFingerprint([]string{"example.com", "www.example.com"}, "cloudflare", "http-01"),
	}
	for name, fp := range different {
		if base.Equal(fp) {
			t.Errorf("%s must not equal base", name)
		}
	}
	// Wildcards compare literally in both directions.
	wild := NewFingerprint([]string{"*.example.com"}, "p", "dns-01")
	apex := NewFingerprint([]string{"example.com"}, "p", "dns-01")
	host := NewFingerprint([]string{"www.example.com"}, "p", "dns-01")
	if wild.Equal(apex) || apex.Equal(wild) || wild.Equal(host) || host.Equal(wild) {
		t.Fatal("wildcard must neither imply nor be implied by apex or host")
	}
	if !wild.Equal(NewFingerprint([]string{"*.EXAMPLE.com."}, "p", "dns-01")) {
		t.Fatal("wildcard normalizes case and trailing dot")
	}
	// IDNA: unicode and punycode spellings are one name.
	if got := normalizeName("bücher.example"); got != "xn--bcher-kva.example" {
		t.Fatalf("punycode = %q", got)
	}
	if !NewFingerprint([]string{"BÜCHER.example"}, "p", "dns-01").
		Equal(NewFingerprint([]string{"xn--bcher-kva.example"}, "p", "dns-01")) {
		t.Fatal("unicode and punycode must match")
	}
}

func renewalSet(names ...string) Set {
	return Set{Certificates: []Certificate{{
		Name: "www", DNSNames: names, Issuer: scriptIssuer(), State: StateIssued,
		Renewal: Renewal{Auto: true},
	}}}
}

func TestCheckRenewals(t *testing.T) {
	ca := testCA(t)
	issuedAt := testTime()
	chain := issueFor(t, ca, []string{"www.example.com"}, 60*24*time.Hour, issuedAt)
	approved := NewFingerprint([]string{"www.example.com"}, "script", "dns-01")
	issuance := []Issuance{{Certificate: "www", Fingerprint: approved, ChainPEM: chain}}
	set := renewalSet("www.example.com")

	cases := []struct {
		name string
		set  Set
		now  time.Time
		want string
	}{
		{"not due", set, issuedAt.Add(10 * 24 * time.Hour), RenewalOK},
		{"outside window by a day", set, issuedAt.Add(29 * 24 * time.Hour), RenewalOK},
		{"due at window edge", set, issuedAt.Add(30 * 24 * time.Hour), RenewalDue},
		{"due inside window", set, issuedAt.Add(45 * 24 * time.Hour), RenewalDue},
		{"expired", set, issuedAt.Add(61 * 24 * time.Hour), RenewalExpired},
		{"names changed", renewalSet("www.example.com", "extra.example.com"), issuedAt, RenewalPlanRequired},
		{"wildcard swap", renewalSet("*.example.com"), issuedAt, RenewalPlanRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CheckRenewals(tc.set, issuance, tc.now)
			if err != nil || len(got) != 1 {
				t.Fatalf("CheckRenewals = %+v, %v", got, err)
			}
			if got[0].Status != tc.want {
				t.Fatalf("status = %q (%s), want %q", got[0].Status, got[0].Detail, tc.want)
			}
		})
	}

	// Provider change in the desired issuer is a fingerprint mismatch.
	other := renewalSet("www.example.com")
	other.Certificates[0].Issuer.DNSProvider = "elsewhere"
	got, err := CheckRenewals(other, issuance, issuedAt)
	if err != nil || got[0].Status != RenewalPlanRequired || !strings.Contains(got[0].Detail, "differs") {
		t.Fatalf("provider change = %+v, %v", got, err)
	}

	// Large renewal thresholds are capped at 30 days.
	wide := renewalSet("www.example.com")
	wide.Certificates[0].Renewal.DaysBeforeExpiry = 90
	got, _ = CheckRenewals(wide, issuance, issuedAt.Add(10*24*time.Hour))
	if got[0].Status != RenewalOK {
		t.Fatalf("90-day threshold must cap at 30 days, got %q", got[0].Status)
	}

	// Certificates without a recorded issuance say so explicitly.
	if got, _ := CheckRenewals(set, nil, issuedAt); len(got) != 1 || got[0].Status != RenewalNoIssuance {
		t.Fatalf("untracked = %+v", got)
	}

	// An unparseable stored chain is its own error state, never
	// plan-required.
	corrupt := []Issuance{{Certificate: "www", Fingerprint: approved, ChainPEM: []byte("garbage")}}
	if got, _ := CheckRenewals(set, corrupt, issuedAt); len(got) != 1 || got[0].Status != RenewalError ||
		!strings.Contains(got[0].Detail, "corrupt") {
		t.Fatalf("corrupt = %+v", got)
	}

	// An ACME issuance placed through S278 is served from files Caddy does
	// not renew, so auto-renew off still warns inside the window.
	noAuto := renewalSet("www.example.com")
	noAuto.Certificates[0].Renewal.Auto = false
	got, err = CheckRenewals(noAuto, issuance, issuedAt.Add(45*24*time.Hour))
	if err != nil || got[0].Status != RenewalDue || got[0].Detail == "" {
		t.Fatalf("auto off = %+v, %v", got, err)
	}
	// Outside the window it is still ok.
	if got, _ := CheckRenewals(noAuto, issuance, issuedAt.Add(time.Hour)); got[0].Status != RenewalOK {
		t.Fatalf("auto off outside window = %+v", got)
	}
	// A manual issuer keeps the old semantics: never due, told to renew by hand.
	manual := renewalSet("www.example.com")
	manual.Certificates[0].Renewal.Auto = false
	manual.Certificates[0].Issuer = Issuer{Name: "own", Type: IssuerManual}
	manualIssuance := []Issuance{{Certificate: "www", Fingerprint: NewFingerprint([]string{"www.example.com"}, "", ""), ChainPEM: issuance[0].ChainPEM}}
	got, err = CheckRenewals(manual, manualIssuance, issuedAt.Add(45*24*time.Hour))
	if err != nil || got[0].Status != RenewalOK || !strings.Contains(got[0].Detail, "manually") {
		t.Fatalf("manual = %+v, %v", got, err)
	}
}

func TestIssueForRecordsFingerprint(t *testing.T) {
	double := &acmeDouble{}
	client, _ := serveACME(t, double)
	dns01, _ := openDNS01(t, client, &scriptDNSProvider{})
	st, err := store.Open(filepath.Join(t.TempDir(), "iss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dns01.Issuances = NewIssuances(st)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issued, err := dns01.IssueFor(ctx, "www", testAccount(t), scriptIssuer(), "example.com",
		[]string{"WWW.example.com", "example.com"}, true)
	if err != nil {
		t.Fatalf("IssueFor: %v", err)
	}
	stored, err := dns01.Issuances.List()
	if err != nil || len(stored) != 1 {
		t.Fatalf("List = %+v, %v", stored, err)
	}
	want := NewFingerprint([]string{"example.com", "www.example.com"}, "script", "dns-01")
	if stored[0].Certificate != "www" || !stored[0].Fingerprint.Equal(want) ||
		!issued.Fingerprint.Equal(want) || len(stored[0].ChainPEM) == 0 {
		t.Fatalf("stored = %+v", stored[0])
	}
}

func TestNormalizeNameIDNA(t *testing.T) {
	composed := "m\u00fcnchen.example"
	decomposed := "mu\u0308nchen.example"
	for in, want := range map[string]string{
		composed:                "xn--mnchen-3ya.example",
		decomposed:              "xn--mnchen-3ya.example",
		"b\u00fccher.example.":  "xn--bcher-kva.example",
		"WWW.Example.COM":       "www.example.com",
		"xn--mnchen-3ya.de":     "xn--mnchen-3ya.de",
		"M\u00dcNCHEN.de":       "xn--mnchen-3ya.de",
		"*.B\u00fccher.example": "*.xn--bcher-kva.example",
		"*.example.com":         "*.example.com",
	} {
		got, err := NormalizeName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "a..example.com", "example.com..", "..example.com", "-bad.example.com", "a b.example.com"} {
		if got, err := NormalizeName(bad); err == nil {
			t.Errorf("NormalizeName(%q) = %q, want error", bad, got)
		}
	}
	if !NewFingerprint([]string{composed}, "p", "dns-01").Equal(NewFingerprint([]string{decomposed}, "p", "dns-01")) {
		t.Fatal("composed and decomposed forms must be one name")
	}
	// A malformed name never equals a valid one and is reported.
	bad := NewFingerprint([]string{"a..example.com"}, "p", "dns-01")
	if bad.Equal(NewFingerprint([]string{"a.example.com"}, "p", "dns-01")) || len(bad.InvalidNames()) != 1 {
		t.Fatalf("invalid name handling: %+v", bad)
	}
}

// IssueFor stamps the record with the injected clock, and a recording
// failure is joined with, not hidden by, an issue-side failure.
func TestIssueForClockAndJoinedRecordFailure(t *testing.T) {
	double := &acmeDouble{}
	client, _ := serveACME(t, double)
	provider := &scriptDNSProvider{deleteErr: errors.New("provider delete refused")}
	dns01, _ := openDNS01(t, client, provider)
	st, err := store.Open(filepath.Join(t.TempDir(), "iss.db"))
	if err != nil {
		t.Fatal(err)
	}
	dns01.Issuances = NewIssuances(st)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dns01.Now = func() time.Time { return stamp }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Cleanup fails after a good issuance: material returned, error
	// reported, and the record is stamped by the injected clock.
	issued, err := dns01.IssueFor(ctx, "www", testAccount(t), scriptIssuer(), "example.com", []string{"www.example.com"}, true)
	if err == nil || len(issued.ChainPEM) == 0 {
		t.Fatalf("want cleanup error with material, got %v", err)
	}
	stored, _ := dns01.Issuances.List()
	if len(stored) != 1 || !stored[0].IssuedAt.Equal(stamp) {
		t.Fatalf("stored = %+v", stored)
	}

	// Now the store is gone too (fresh CA double; the first one is spent):
	// both failures surface.
	client2, _ := serveACME(t, &acmeDouble{})
	dns02, _ := openDNS01(t, client2, &scriptDNSProvider{deleteErr: errors.New("provider delete refused")})
	st2, err := store.Open(filepath.Join(t.TempDir(), "iss2.db"))
	if err != nil {
		t.Fatal(err)
	}
	dns02.Issuances = NewIssuances(st2)
	st2.Close()
	_, err = dns02.IssueFor(ctx, "www", testAccount(t), scriptIssuer(), "example.com", []string{"www.example.com"}, true)
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") || !strings.Contains(err.Error(), "recording failed") {
		t.Fatalf("err = %v, want both cleanup and recording failures", err)
	}
}

package cert

import (
	"context"
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

	// Certificates without a recorded issuance are omitted.
	if got, _ := CheckRenewals(set, nil, issuedAt); len(got) != 0 {
		t.Fatalf("untracked = %+v", got)
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

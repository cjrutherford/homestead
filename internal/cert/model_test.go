package cert

import (
	"strings"
	"testing"
	"time"
)

func validCert() Certificate {
	return Certificate{
		Name:     "homestead-lan",
		DNSNames: []string{"homestead.lan", "*.apps.lan"},
		Issuer:   Issuer{Name: "homestead-internal", Type: IssuerInternal},
		State:    StateIssued,
		Renewal:  Renewal{Auto: true},
	}
}

func TestCertificateValid(t *testing.T) {
	if err := validCert().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	acme := validCert()
	acme.Issuer = Issuer{Name: "letsencrypt", Type: IssuerACME,
		Directory: "https://acme-v02.api.letsencrypt.org/directory",
		Challenge: ChallengeDNS01, DNSProvider: "cloudflare"}
	if err := acme.Validate(); err != nil {
		t.Fatalf("acme Validate: %v", err)
	}
	manual := validCert()
	manual.Issuer = Issuer{Name: "owner", Type: IssuerManual}
	manual.Renewal = Renewal{}
	if err := manual.Validate(); err != nil {
		t.Fatalf("manual Validate: %v", err)
	}
	failed := validCert()
	failed.State = StateFailed
	failed.LastError = "dns timeout"
	if err := failed.Validate(); err != nil {
		t.Fatalf("failed Validate: %v", err)
	}
}

func TestCertificateInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Certificate)
		want   string
	}{
		{"empty name", func(c *Certificate) { c.Name = "" }, "cert: name"},
		{"bad name", func(c *Certificate) { c.Name = "Bad!" }, "invalid name"},
		{"no names", func(c *Certificate) { c.DNSNames = nil }, "at least one"},
		{"uppercase san", func(c *Certificate) { c.DNSNames = []string{"App.lan"} }, "lowercase"},
		{"bare wildcard", func(c *Certificate) { c.DNSNames = []string{"*"} }, "invalid label"},
		{"dup san", func(c *Certificate) { c.DNSNames = []string{"a.lan", "a.lan"} }, "duplicate DNS name"},
		{"bad issuer", func(c *Certificate) { c.Issuer.Type = "selfsigned" }, "issuer type"},
		{"acme http dir", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerACME, Directory: "http://x/", Challenge: ChallengeHTTP01}
		}, "https URL"},
		{"acme no challenge", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerACME, Directory: "https://x/"}
		}, "challenge"},
		{"dns01 no provider", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerACME, Directory: "https://x/", Challenge: ChallengeDNS01}
		}, "needs a DNS provider"},
		{"http01 with provider", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerACME, Directory: "https://x/", Challenge: ChallengeHTTP01, DNSProvider: "cf"}
		}, "only applies to dns-01"},
		{"internal with dir", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerInternal, Directory: "https://x/"}
		}, "takes no ACME parameters"},
		{"bad state", func(c *Certificate) { c.State = "expiring" }, "cert: state"},
		{"stale error", func(c *Certificate) { c.LastError = "old" }, "only belongs on failed"},
		{"manual auto", func(c *Certificate) {
			c.Issuer = Issuer{Name: "x", Type: IssuerManual}
		}, "cannot auto-renew"},
		{"bad days", func(c *Certificate) { c.Renewal.DaysBeforeExpiry = 91 }, "0-90"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCert()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("accepted invalid certificate")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestRenewalDue(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	auto := Renewal{Auto: true}
	if !auto.RenewalDue(now, now.AddDate(0, 0, 29)) {
		t.Fatal("29 days left with 30-day threshold should renew")
	}
	if auto.RenewalDue(now, now.AddDate(0, 0, 31)) {
		t.Fatal("31 days left should not renew")
	}
	if !auto.RenewalDue(now, now.AddDate(0, 0, 30)) {
		t.Fatal("boundary should renew")
	}
	if (Renewal{}).RenewalDue(now, now) {
		t.Fatal("manual renewal came due")
	}
	custom := Renewal{Auto: true, DaysBeforeExpiry: 7}
	if custom.RenewalDue(now, now.AddDate(0, 0, 8)) {
		t.Fatal("8 days left with 7-day threshold should not renew")
	}
	if !custom.RenewalDue(now, now.AddDate(0, 0, -1)) {
		t.Fatal("expired should renew")
	}
}

func TestSet(t *testing.T) {
	set := Set{Certificates: []Certificate{
		validCert(),
		func() Certificate {
			c := validCert()
			c.Name = "public"
			c.DNSNames = []string{"example.com"}
			c.Issuer = Issuer{Name: "le", Type: IssuerACME,
				Directory: "https://x/", Challenge: ChallengeHTTP01}
			return c
		}(),
	}}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got, ok := set.Find("*.apps.lan")
	if !ok || got.Name != "homestead-lan" {
		t.Fatalf("Find = %+v %v", got, ok)
	}
	if _, ok := set.Find("missing.lan"); ok {
		t.Fatal("found missing name")
	}
	dup := Set{Certificates: []Certificate{validCert(), validCert()}}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate certificate name") {
		t.Fatalf("err = %v", err)
	}
	clash := validCert()
	clash.Name = "other"
	over := Set{Certificates: []Certificate{validCert(), clash}}
	if err := over.Validate(); err == nil || !strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("err = %v", err)
	}
	bad := Set{Certificates: []Certificate{func() Certificate {
		c := validCert()
		c.State = "bogus"
		return c
	}()}}
	if err := bad.Validate(); err == nil {
		t.Fatal("accepted invalid member")
	}
}

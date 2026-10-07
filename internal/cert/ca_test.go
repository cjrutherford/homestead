package cert

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func testTime() time.Time {
	return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
}

func testCA(t *testing.T) Authority {
	t.Helper()
	ca, err := GenerateCA("Homestead Internal CA", DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	return ca
}

func TestGenerateCA(t *testing.T) {
	ca := testCA(t)
	if !ca.Cert.IsCA || !ca.Cert.BasicConstraintsValid || !ca.Cert.MaxPathLenZero {
		t.Fatalf("not a single-tier CA: %+v", ca.Cert)
	}
	if ca.Cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatal("CA cannot sign")
	}
	if ca.Cert.Subject.CommonName != "Homestead Internal CA" {
		t.Fatalf("CN = %q", ca.Cert.Subject.CommonName)
	}
	if ca.Cert.NotAfter.Sub(ca.Cert.NotBefore) != DefaultCALifetime {
		t.Fatal("CA lifetime wrong")
	}
	// Self-signed verifies against itself.
	if err := ca.Cert.CheckSignatureFrom(ca.Cert); err != nil {
		t.Fatalf("self-sign: %v", err)
	}
	// Round-trips through storage forms.
	parsed, err := ParseAuthority(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		t.Fatalf("ParseAuthority: %v", err)
	}
	if parsed.Cert.SerialNumber.Cmp(ca.Cert.SerialNumber) != 0 {
		t.Fatal("serial mismatch after round-trip")
	}
	for _, args := range []struct {
		name     string
		cn       string
		lifetime time.Duration
		want     string
	}{
		{"empty cn", "", DefaultCALifetime, "1-64"},
		{"short life", "x", time.Hour, "1 day"},
		{"long life", "x", 40 * 365 * 24 * time.Hour, "30 years"},
	} {
		t.Run(args.name, func(t *testing.T) {
			_, err := GenerateCA(args.cn, args.lifetime, testTime())
			if err == nil || !strings.Contains(err.Error(), args.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseAuthorityRejects(t *testing.T) {
	ca := testCA(t)
	other, err := GenerateCA("Other", DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if _, err := ParseAuthority([]byte("junk"), ca.KeyPEM); err == nil {
		t.Fatal("parsed junk cert")
	}
	if _, err := ParseAuthority(ca.KeyPEM, ca.KeyPEM); err == nil {
		t.Fatal("parsed key as cert")
	}
	serverChain, serverKey, err := ca.IssueServerCert([]string{"app.lan"}, DefaultServerLifetime, testTime())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := ParseAuthority(serverChain, serverKey); err == nil ||
		!strings.Contains(err.Error(), "not a CA") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseAuthority(ca.CertPEM, other.KeyPEM); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseAuthority(ca.CertPEM, []byte("junk")); err == nil {
		t.Fatal("parsed junk key")
	}
}

func TestIssueServerCert(t *testing.T) {
	ca := testCA(t)
	chainPEM, keyPEM, err := ca.IssueServerCert(
		[]string{"app.lan", "*.apps.lan"}, DefaultServerLifetime, testTime())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	block, _ := pem.Decode(chainPEM)
	if block == nil {
		t.Fatal("chain is not PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if leaf.IsCA {
		t.Fatal("server cert is a CA")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("server cannot sign handshakes")
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("EKU = %v", leaf.ExtKeyUsage)
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) != DefaultServerLifetime {
		t.Fatal("server lifetime wrong")
	}
	if _, err := ParseAccountKey(keyPEM); err != nil {
		t.Fatalf("key: %v", err)
	}
	// Verifies against the CA pool for every covered name.
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	for _, name := range []string{"app.lan", "web.apps.lan"} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			DNSName: name, Roots: pool, CurrentTime: testTime().Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("verify %s: %v", name, err)
		}
	}
	// Distinct serials per issuance.
	second, _, err := ca.IssueServerCert([]string{"app.lan"}, DefaultServerLifetime, testTime())
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	block2, _ := pem.Decode(second)
	leaf2, err := x509.ParseCertificate(block2.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if leaf2.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
		t.Fatal("serials collide")
	}
	// Expired authorities still sign math, but verification at issuance
	// time anchors honesty: not-before/not-after always derive from now.
	if !leaf.NotBefore.Equal(testTime()) {
		t.Fatalf("not-before = %v", leaf.NotBefore)
	}
}

func TestIssueServerCertRejects(t *testing.T) {
	ca := testCA(t)
	for _, args := range []struct {
		name     string
		names    []string
		lifetime time.Duration
		want     string
	}{
		{"no names", nil, DefaultServerLifetime, "at least one"},
		{"bad name", []string{"App.lan"}, DefaultServerLifetime, "lowercase"},
		{"dup names", []string{"a.lan", "a.lan"}, DefaultServerLifetime, "duplicate"},
		{"short life", []string{"a.lan"}, time.Minute, "1 hour"},
		{"long life", []string{"a.lan"}, 4 * 365 * 24 * time.Hour, "3 years"},
	} {
		t.Run(args.name, func(t *testing.T) {
			_, _, err := ca.IssueServerCert(args.names, args.lifetime, testTime())
			if err == nil || !strings.Contains(err.Error(), args.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, _, err := (Authority{}).IssueServerCert([]string{"a.lan"}, time.Hour, testTime()); err == nil {
		t.Fatal("empty authority issued")
	}
}

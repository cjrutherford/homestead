package jellyfin

import (
	"strings"
	"testing"
	"time"

	"homestead/internal/cert"
	"homestead/internal/docker"
)

func tlsRoute() docker.Route {
	return docker.Route{
		Host:     "jellyfin.lan",
		Target:   docker.Target{Host: "jellyfin", Port: 8096},
		Exposure: docker.ExposureLocal,
		TLS:      docker.TLSInternal,
	}
}

func testAuthority(t *testing.T) *cert.Authority {
	t.Helper()
	ca, err := cert.GenerateCA("Test CA", cert.DefaultCALifetime, testTime())
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	return &ca
}

func TestCheckReadiness(t *testing.T) {
	ready, detail := CheckReadiness(tlsRoute(), true)
	if !ready || !strings.Contains(detail, "configured") {
		t.Fatalf("ready=%v detail=%q", ready, detail)
	}
	if ready, _ := CheckReadiness(tlsRoute(), false); ready {
		t.Fatal("ready without authority")
	}
	auto := tlsRoute()
	auto.Host = "jellyfin.example.com"
	auto.Exposure = docker.ExposurePublic
	auto.TLS = docker.TLSAuto
	if ready, _ := CheckReadiness(auto, false); !ready {
		t.Fatal("valid auto not ready")
	}
	auto.Exposure = docker.ExposureLocal
	if ready, detail := CheckReadiness(auto, false); ready ||
		!strings.Contains(detail, "DNS-01") {
		t.Fatalf("ready=%v detail=%q", ready, detail)
	}
	plain := tlsRoute()
	plain.TLS = docker.TLSOff
	if ready, _ := CheckReadiness(plain, false); !ready {
		t.Fatal("plaintext not acknowledged")
	}
	bad := tlsRoute()
	bad.Host = ""
	if ready, _ := CheckReadiness(bad, true); ready {
		t.Fatal("ready with invalid route")
	}
}

func TestIssueInternal(t *testing.T) {
	ca := testAuthority(t)
	issued, err := IssueInternal(ca, "jellyfin.lan", testTime())
	if err != nil {
		t.Fatalf("IssueInternal: %v", err)
	}
	if len(issued.ChainPEM) == 0 || len(issued.KeyPEM) == 0 {
		t.Fatal("empty material")
	}
	if _, err := IssueInternal(nil, "jellyfin.lan", testTime()); err == nil {
		t.Fatal("issued without authority")
	}
	if _, err := IssueInternal(ca, "Bad!", testTime()); err == nil {
		t.Fatal("issued bad hostname")
	}
}

func TestVerifyCertificate(t *testing.T) {
	ca := testAuthority(t)
	issued, err := IssueInternal(ca, "jellyfin.lan", testTime())
	if err != nil {
		t.Fatalf("IssueInternal: %v", err)
	}
	if err := VerifyCertificate(issued.ChainPEM, "jellyfin.lan", testTime()); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	if err := VerifyCertificate(issued.ChainPEM, "other.lan", testTime()); err == nil {
		t.Fatal("verified wrong name")
	}
	if err := VerifyCertificate(issued.ChainPEM, "jellyfin.lan", testTime().Add(400*24*time.Hour)); err == nil {
		t.Fatal("verified expired material")
	}
	if err := VerifyCertificate([]byte("junk"), "jellyfin.lan", testTime()); err == nil {
		t.Fatal("verified junk")
	}
	wild, err := IssueInternal(ca, "*.apps.lan", testTime())
	if err != nil {
		t.Fatalf("IssueInternal: %v", err)
	}
	if err := VerifyCertificate(wild.ChainPEM, "web.apps.lan", testTime()); err != nil {
		t.Fatalf("wildcard: %v", err)
	}
	if err := VerifyCertificate(wild.ChainPEM, "deep.web.apps.lan", testTime()); err == nil {
		t.Fatal("wildcard covered two labels")
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"a.lan", "a.lan", true},
		{"*.apps.lan", "web.apps.lan", true},
		{"*.apps.lan", "apps.lan", false},
		{"*.apps.lan", "deep.web.apps.lan", false},
		{"*.apps.lan", "web.other.lan", false},
		{"a.lan", "b.lan", false},
	}
	for _, tc := range cases {
		if got := WildcardMatch(tc.pattern, tc.host); got != tc.want {
			t.Fatalf("match(%q, %q) = %v", tc.pattern, tc.host, got)
		}
	}
}

func testTime() time.Time {
	return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
}

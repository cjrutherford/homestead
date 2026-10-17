package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/cert"
)

func TestTLSRootShowAndExport(t *testing.T) {
	ca, err := cert.GenerateConstrainedCA("Homestead Local CA (t)", 24*time.Hour*365, time.Now(), cert.LocalPermittedDomains)
	if err != nil {
		t.Fatal(err)
	}
	fp := cert.CertFingerprint(ca.Cert.Raw)
	report := cert.Report{CA: cert.CAInfo{Present: true, Subject: ca.Cert.Subject.CommonName, FingerprintSHA256: fp,
		Serial: "ab", NotBefore: ca.Cert.NotBefore, NotAfter: ca.Cert.NotAfter, PermittedDNS: cert.LocalPermittedDomains,
		Generation: 1, KeyPresent: true}}
	tamper := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tls/local-ca":
			_ = json.NewEncoder(w).Encode(report)
		case "/api/v1/tls/local-ca/root.pem":
			if tamper {
				other, _ := cert.GenerateCA("evil", 24*time.Hour*30, time.Now())
				_, _ = w.Write(other.CertPEM)
				return
			}
			_, _ = w.Write(ca.CertPEM)
		case "/api/v1/tls/local-ca/root.der":
			_, _ = w.Write(ca.Cert.Raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	code, out, _ := invoke(t, []string{"--server", ts.URL, "tls", "root", "show"})
	if code != exitOK || !strings.Contains(out, fp) || !strings.Contains(out, "openssl x509") {
		t.Fatalf("show exit=%d out=%q", code, out)
	}
	code, out, errOut := invoke(t, []string{"--server", ts.URL, "tls", "root", "export"})
	if code != exitOK || !strings.Contains(out, "BEGIN CERTIFICATE") || strings.Contains(out, "PRIVATE") || !strings.Contains(errOut, fp) {
		t.Fatalf("export exit=%d out=%q err=%q", code, out, errOut)
	}
	file := filepath.Join(t.TempDir(), "root.crt")
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "tls", "root", "export", "--format", "der", "--out", file}); code != exitOK {
		t.Fatalf("der export exit = %d", code)
	}
	if raw, _ := os.ReadFile(file); string(raw) != string(ca.Cert.Raw) {
		t.Fatal("DER file differs")
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "tls", "root", "export", "--format", "der"}); code != exitUsage {
		t.Fatalf("DER to stdout exit = %d, want usage", code)
	}
	tamper = true
	code, out, errOut = invoke(t, []string{"--server", ts.URL, "tls", "root", "export"})
	if code != exitError || out != "" || !strings.Contains(errOut, "refusing") {
		t.Fatalf("tampered export exit=%d out=%q err=%q", code, out, errOut)
	}
}

func TestTLSLocalCommands(t *testing.T) {
	var reissue, rotate map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/tls/local-ca":
			_, _ = w.Write([]byte(`{"ca":{"present":true,"subject":"CA","fingerprint_sha256":"AA:BB","key_present":true},"created":true}`))
		case "GET /api/v1/tls/local-ca/leaves":
			_, _ = w.Write([]byte(`{"leaves":[{"host":"craft.lan","status":"ok","not_after":"2027-01-01T00:00:00Z"}]}`))
		case "POST /api/v1/tls/local-ca/leaves/reissue":
			_ = json.NewDecoder(r.Body).Decode(&reissue)
			_, _ = w.Write([]byte(`{"id":5}`))
		case "GET /api/v1/tls/local-ca/rotation":
			_, _ = w.Write([]byte(`{"current_fingerprint_sha256":"AA:BB","generation":1,"leaves_to_reissue":["craft.lan"],"devices_must_retrust":"re-trust everywhere","steps":["s1"],"digest":"d1"}`))
		case "POST /api/v1/tls/local-ca/rotation":
			_ = json.NewDecoder(r.Body).Decode(&rotate)
			_, _ = w.Write([]byte(`{"id":6}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	run := func(args ...string) (int, string) {
		code, out, _ := invoke(t, append([]string{"--server", ts.URL, "tls", "local"}, args...))
		return code, out
	}
	if code, out := run("create"); code != exitOK || !strings.Contains(out, "created") || !strings.Contains(out, "AA:BB") {
		t.Fatalf("create %d %q", code, out)
	}
	if code, out := run("leaves"); code != exitOK || !strings.Contains(out, "craft.lan: ok") {
		t.Fatalf("leaves %d %q", code, out)
	}
	if code, out := run("reissue", "craft.lan"); code != exitOK || !strings.Contains(out, "operation: 5") || reissue["host"] != "craft.lan" {
		t.Fatalf("reissue %d %q %v", code, out, reissue)
	}
	if code, out := run("rotate"); code != exitOK || !strings.Contains(out, "--digest d1") || !strings.Contains(out, "re-trust everywhere") {
		t.Fatalf("rotate plan %d %q", code, out)
	}
	if code, out := run("rotate", "--digest", "d1"); code != exitOK || !strings.Contains(out, "operation: 6") || rotate["digest"] != "d1" {
		t.Fatalf("rotate %d %q %v", code, out, rotate)
	}
	if code, _ := run("bogus"); code != exitUsage {
		t.Fatalf("bogus %d", code)
	}
}

package exposure

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"homestead/internal/dns"
)

func validationTarget() Target {
	return Target{
		Host:    "app.example.com",
		Address: netip.MustParseAddr("203.0.113.7"),
	}
}

func okProbe(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
	return []string{"203.0.113.7"}, nil
}

func TestValidateRejects(t *testing.T) {
	for _, target := range []Target{
		{},
		{Host: "app.example.com"},
		{Host: "app.example.com", Address: netip.MustParseAddr("::")},
		{Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"), Path: "nope"},
		{Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"), WantStatus: 99},
	} {
		if _, err := Validate(context.Background(), okProbe, target); err == nil {
			t.Fatalf("validated %+v", target)
		}
	}
	if _, err := Validate(context.Background(), nil, validationTarget()); err == nil {
		t.Fatal("validated without probe")
	}
}

func TestValidateDNSMismatch(t *testing.T) {
	wrong := func(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
		return []string{"198.51.100.9"}, nil
	}
	report, err := Validate(context.Background(), wrong, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Addr: "127.0.0.1:1", Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if report.Passed || report.Checks[0].OK {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Checks[0].Detail, "want 203.0.113.7") {
		t.Fatalf("detail = %q", report.Checks[0].Detail)
	}
	failing := func(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
		return nil, fmt.Errorf("SERVFAIL")
	}
	report, err = Validate(context.Background(), failing, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Addr: "127.0.0.1:1", Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !strings.Contains(report.Checks[0].Detail, "SERVFAIL") {
		t.Fatalf("detail = %q", report.Checks[0].Detail)
	}
}

func tcpListener(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}

func TestValidateTCPLayers(t *testing.T) {
	open, close := tcpListener(t)
	defer close()
	report, err := Validate(context.Background(), okProbe, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Addr: open, Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// DNS ok (fake), TCP ok via override, TLS skipped, HTTP hits a bare
	// TCP socket (not HTTP) and fails honestly.
	byCheck := map[string]Finding{}
	for _, finding := range report.Checks {
		byCheck[finding.Check] = finding
	}
	if !byCheck[CheckDNS].OK || !byCheck[CheckTCP80].OK || !byCheck[CheckTCP443].OK {
		t.Fatalf("report = %+v", report)
	}
	if !byCheck[CheckTLS].OK || !strings.Contains(byCheck[CheckTLS].Detail, "skips TLS") {
		t.Fatalf("tls = %+v", byCheck[CheckTLS])
	}
	if byCheck[CheckHTTP].OK {
		t.Fatalf("http passed against bare TCP: %+v", byCheck[CheckHTTP])
	}
	if report.Passed {
		t.Fatal("passed with failing layer")
	}
}

func TestValidateHTTPContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("healthy serving traffic"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	probe := func(_ context.Context, _ string, _ dns.RecordType) ([]string, error) {
		return []string{"203.0.113.7"}, nil
	}
	good, err := Validate(context.Background(), probe, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Path: "/healthz", WantBody: "healthy", Addr: addr, Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, finding := range good.Checks {
		if finding.Check == CheckTLS {
			continue
		}
		if !finding.OK {
			t.Fatalf("finding = %+v (all %+v)", finding, good.Checks)
		}
	}
	if !good.Passed {
		t.Fatalf("report = %+v", good)
	}
	wrongStatus, err := Validate(context.Background(), probe, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Path: "/missing", Addr: addr, Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if wrongStatus.Passed {
		t.Fatal("passed with 404")
	}
	wrongBody, err := Validate(context.Background(), probe, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"),
		Path: "/healthz", WantBody: "absent-text", Addr: addr, Plaintext: true,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if wrongBody.Passed {
		t.Fatal("passed with wrong body")
	}
}

func TestValidateTLSUntrusted(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	report, err := Validate(context.Background(), okProbe, Target{
		Host: "app.example.com", Address: netip.MustParseAddr("203.0.113.7"), Addr: addr,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	byCheck := map[string]Finding{}
	for _, finding := range report.Checks {
		byCheck[finding.Check] = finding
	}
	// Self-signed test double is not publicly trusted: TLS classifies,
	// TCP dials succeed, HTTP fails trust too.
	if byCheck[CheckTLS].OK || !strings.Contains(byCheck[CheckTLS].Detail, "not publicly trusted") {
		t.Fatalf("tls = %+v", byCheck[CheckTLS])
	}
	if !byCheck[CheckTCP443].OK {
		t.Fatalf("tcp = %+v", byCheck[CheckTCP443])
	}
	if report.Passed {
		t.Fatal("passed with untrusted TLS")
	}
}

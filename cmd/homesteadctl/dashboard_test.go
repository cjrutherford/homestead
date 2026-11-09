package main

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dashboardAddressJSON = `{"name":"homestead.lan","default":"homestead.lan","configured":true,"exposure":"local","mdns_name":"homestead.local","names":["homestead.lan","homestead.local"],"urls":["https://homestead.local","https://homestead.lan"],"root_path":"/api/v1/tls/local-ca/root.pem"}`

func dashboardStub(t *testing.T, tls bool) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization")+" cookie="+r.Header.Get("Cookie"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dashboard/address":
			_, _ = w.Write([]byte(dashboardAddressJSON))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/dashboard/address":
			body, _ := io.ReadAll(r.Body)
			seen = append(seen, "body="+string(body))
			_, _ = w.Write([]byte(`{"id":31}`))
		default:
			http.NotFound(w, r)
		}
	})
	var ts *httptest.Server
	if tls {
		ts = httptest.NewTLSServer(h)
	} else {
		ts = httptest.NewServer(h)
	}
	t.Cleanup(ts.Close)
	return ts, &seen
}

func TestDashboardAddressGetListsLocalNameFirst(t *testing.T) {
	ts, _ := dashboardStub(t, false)
	code, out, _ := invoke(t, []string{"--server", ts.URL, "dashboard", "address"})
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	i, j := strings.Index(out, "https://homestead.local"), strings.Index(out, "https://homestead.lan")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("output = %q", out)
	}
}

func TestDashboardAddressSet(t *testing.T) {
	ts, seen := dashboardStub(t, false)
	code, out, _ := invoke(t, []string{"--server", ts.URL, "dashboard", "address", "set", "box.lan", "--exposure", "overlay"})
	if code != exitOK || !strings.Contains(out, "operation: 31") {
		t.Fatalf("exit = %d out = %q", code, out)
	}
	joined := strings.Join(*seen, "\n")
	if !strings.Contains(joined, `"name":"box.lan"`) || !strings.Contains(joined, `"exposure":"overlay"`) {
		t.Fatalf("request = %s", joined)
	}
}

func TestDashboardAddressRefusesPublicAndBadUsage(t *testing.T) {
	ts, seen := dashboardStub(t, false)
	code, _, errOut := invoke(t, []string{"--server", ts.URL, "dashboard", "address", "set", "box.lan", "--exposure", "public"})
	if code != exitError || !strings.Contains(errOut, "tunnel") {
		t.Fatalf("public: exit = %d err = %q", code, errOut)
	}
	if len(*seen) != 0 {
		t.Fatalf("a public request reached the daemon: %v", *seen)
	}
	for _, args := range [][]string{{"dashboard"}, {"dashboard", "nope"}, {"dashboard", "address", "set"}, {"dashboard", "address", "set", "a.lan", "--bogus"}} {
		if code, _, _ := invoke(t, append([]string{"--server", ts.URL}, args...)); code != exitUsage {
			t.Errorf("%v: exit = %d, want usage", args, code)
		}
	}
}

func TestDashboardOverHTTPSVerifiesAgainstRootFile(t *testing.T) {
	ts, seen := dashboardStub(t, true)
	t.Setenv("HOMESTEAD_TOKEN", "tok-123")
	// Without the root the certificate is not trusted and nothing is sent.
	code, _, errOut := invoke(t, []string{"--server", ts.URL, "dashboard", "address"})
	if code == exitOK || len(*seen) != 0 {
		t.Fatalf("an untrusted certificate was accepted: exit %d err %q seen %v", code, errOut, *seen)
	}
	caPath := filepath.Join(t.TempDir(), "root.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := invoke(t, []string{"--server", ts.URL, "--ca-file", caPath, "dashboard", "address"})
	if code != exitOK || !strings.Contains(out, "homestead.local") {
		t.Fatalf("exit = %d out = %q err = %q", code, out, errOut)
	}
	joined := strings.Join(*seen, "\n")
	if !strings.Contains(joined, "auth=Bearer tok-123") {
		t.Fatalf("request = %s", joined)
	}
	// The env var works too, and a bad file is a usage error, not a skip.
	t.Setenv("HOMESTEAD_CA_FILE", caPath)
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "dashboard", "address"}); code != exitOK {
		t.Fatalf("HOMESTEAD_CA_FILE exit = %d", code)
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	_ = os.WriteFile(bad, []byte("not a cert"), 0o644)
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "--ca-file", bad, "dashboard", "address"}); code == exitOK {
		t.Fatal("a file with no certificate was accepted")
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "--ca-file", filepath.Join(t.TempDir(), "missing"), "status"}); code != exitUsage {
		t.Fatalf("missing file exit = %d", code)
	}
}

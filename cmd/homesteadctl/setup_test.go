package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupCLI(t *testing.T) {
	var seen []string
	body := `{"origin":"fresh","legacy":false,"complete":false,"gated":true,"current":"account","choices":{"provider":"cloudflare","access":"tunnel"},"steps":[` +
		`{"name":"account","title":"Secure the owner account","state":"pending","detail":"Export the recovery identity."},` +
		`{"name":"sign-in","title":"Sign-in","state":"skipped","detail":"Skipped."},` +
		`{"name":"domain","title":"Domain and access","state":"pending","detail":"Choose a DNS provider."},` +
		`{"name":"apps","title":"First apps","state":"done","detail":"Finished."}],"facts":{},"providers":[],"access":[],"starter_apps":[]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	code, out, _ := invoke(t, []string{"--server", ts.URL, "setup", "status"})
	if code != exitOK || !strings.Contains(out, "in progress (next: account)") || !strings.Contains(out, "not gated") || !strings.Contains(out, "skipped") {
		t.Fatalf("status: %d %s", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "setup", "skip", "sign-in"})
	if code != exitOK || seen[len(seen)-1] != `PUT /api/v1/setup/steps/sign-in {"state":"skipped"}` {
		t.Fatalf("skip: %d %v", code, seen)
	}
	invoke(t, []string{"--server", ts.URL, "setup", "unskip", "sign-in"})
	if seen[len(seen)-1] != `PUT /api/v1/setup/steps/sign-in {"state":"pending"}` {
		t.Fatalf("unskip: %v", seen)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "setup", "step", "domain", "--provider", "cloudflare", "--access", "tunnel"})
	if code != exitOK || !strings.Contains(out, "provider: cloudflare") || !strings.Contains(out, "access: tunnel") {
		t.Fatalf("step: %d %s", code, out)
	}
	if seen[len(seen)-1] != `PUT /api/v1/setup/steps/domain {"access":"tunnel","provider":"cloudflare"}` {
		t.Fatalf("step body: %v", seen)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "setup", "step", "apps", "--acknowledge"})
	if code != exitOK || seen[len(seen)-1] != `PUT /api/v1/setup/steps/apps {"state":"done"}` {
		t.Fatalf("ack: %d %v", code, seen)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "setup", "step", "account", "--acknowledge"})
	if code != exitOK || seen[len(seen)-1] != `PUT /api/v1/setup/steps/account {"state":"done"}` {
		t.Fatalf("account ack: %d %v", code, seen)
	}
	n := len(seen)
	code, out, _ = invoke(t, []string{"--server", ts.URL, "setup", "step", "account"})
	if code != exitOK || len(seen) != n+1 || !strings.HasPrefix(seen[n], "GET /api/v1/setup") || !strings.Contains(out, "Export the recovery identity") {
		t.Fatalf("show: %d %v %s", code, seen, out)
	}
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "setup", "bogus"}); code != exitUsage {
		t.Fatalf("bogus = %d", code)
	}
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "setup", "step", "nope"}); code != exitUsage {
		t.Fatalf("unknown step = %d", code)
	}
}

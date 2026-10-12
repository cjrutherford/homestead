package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTLSRenewal(t *testing.T) {
	body := `{"recorded_issuances":1,"summary":"1 recorded issuance(s)","certificates":[{"certificate":"www","status":"renewal-due","days_remaining":12,"not_after":"2026-10-23T00:00:00Z","detail":"www expires soon"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tls/renewal" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	code, stdout, _ := invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitOK || !strings.Contains(stdout, "www: renewal-due days=12") {
		t.Fatalf("exit = %d stdout = %q", code, stdout)
	}
	body = strings.Replace(body, "renewal-due", "expired", 1)
	code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitError {
		t.Fatalf("expired exit = %d, want %d", code, exitError)
	}
	body = strings.Replace(body, "expired", "error", 1)
	if code, _, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"}); code != exitError {
		t.Fatalf("error exit = %d, want %d", code, exitError)
	}
	body = `{"recorded_issuances":0,"summary":"no recorded issuances: nothing to renew","certificates":[]}`
	code, stdout, _ = invoke(t, []string{"--server", ts.URL, "tls", "renewal"})
	if code != exitOK || !strings.Contains(stdout, "no recorded issuances") {
		t.Fatalf("empty exit = %d stdout = %q", code, stdout)
	}
	code, _, _ = invoke(t, []string{"--server", ts.URL, "tls"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func secretsStub(t *testing.T, rekeyed *bool) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/key":
			_, _ = w.Write([]byte(`{"version":3,"in_progress":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/secrets/rekey":
			*rekeyed = true
			_, _ = w.Write([]byte(`{"id":21}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestSecretsStatusAndRekey(t *testing.T) {
	rekeyed := false
	url := secretsStub(t, &rekeyed)
	code, stdout, _ := invoke(t, []string{"--server", url, "secrets", "status"})
	if code != exitOK || !strings.Contains(stdout, "key version: 3") {
		t.Fatalf("status = %d %q", code, stdout)
	}
	// Unconfirmed re-key is a usage error and sends nothing.
	code, _, stderr := invoke(t, []string{"--server", url, "secrets", "rekey"})
	if code != exitUsage || rekeyed || !strings.Contains(stderr, "recovery material") {
		t.Fatalf("unconfirmed = %d rekeyed=%v %q", code, rekeyed, stderr)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "secrets", "rekey", "--confirm"})
	if code != exitOK || !rekeyed || !strings.Contains(stdout, "operation: 21") {
		t.Fatalf("rekey = %d %q", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "secrets"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

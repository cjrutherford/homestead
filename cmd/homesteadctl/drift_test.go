package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func driftStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/jellyfin/drift":
			_, _ = w.Write([]byte(`[{"path":"compose.yaml","kind":"modified","recorded":"services: {}\n","current":"services: {expert: true}\n"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/jellyfin/drift":
			_, _ = w.Write([]byte(`{"accepted":["compose.yaml"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/plain/drift":
			_, _ = w.Write([]byte(`{"id":0,"detail":"no drift"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestDriftReview(t *testing.T) {
	url := driftStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "drift", "review", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"compose.yaml (modified)", "--- managed ---", "--- current ---", "expert: true"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if code, _, _ := invoke(t, []string{"--server", url, "drift"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestDriftAccept(t *testing.T) {
	url := driftStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "drift", "accept", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "adopted compose.yaml") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestDriftRestore(t *testing.T) {
	url := driftStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "drift", "restore", "plain"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "no drift") {
		t.Fatalf("stdout = %q", stdout)
	}
}

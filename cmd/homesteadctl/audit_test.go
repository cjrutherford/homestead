package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func auditStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("limit") == "bogus" {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":2,"actor":"owner","action":"admin.login","plan":"","outcome":"error","detail":"invalid username or password","override":false,"override_reason":"","created_at":"2026-10-06T00:00:01Z"},{"id":1,"actor":"owner","action":"admin.login","plan":"","outcome":"ok","detail":"","override":false,"override_reason":"","created_at":"2026-10-06T00:00:00Z"}]`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestAuditList(t *testing.T) {
	url := auditStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "audit", "list"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "admin.login") || !strings.Contains(stdout, "owner") {
		t.Fatalf("stdout = %q", stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "2 ") {
		t.Fatalf("stdout = %q, want newest-first", stdout)
	}
}

func TestAuditListLimit(t *testing.T) {
	url := auditStub(t)
	code, _, _ := invoke(t, []string{"--server", url, "audit", "list", "--limit", "10"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	code, _, _ = invoke(t, []string{"--server", url, "audit", "list", "-n", "10"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, args := range [][]string{
		{"audit"},
		{"audit", "bogus"},
		{"audit", "list", "--limit", "bogus"},
		{"audit", "list", "--limit", "-3"},
	} {
		full := append([]string{"--server", url}, args...)
		if code, _, _ := invoke(t, full); code != exitUsage {
			t.Fatalf("args %q exit = %d, want 2", args, code)
		}
	}
}

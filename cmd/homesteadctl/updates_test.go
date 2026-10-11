package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func updatesStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/updates":
			_, _ = w.Write([]byte(`[
				{"package":"jellyfin","installed":true,"plan":{"Package":"jellyfin",
					"Services":[{"Service":"jellyfin","Current":"jellyfin/jellyfin:12.1","Target":"jellyfin/jellyfin:12.2","Source":"manifest"}],
					"Backup":true,"BackupWhy":"backup paths declared","Affected":["jellyfin"],"Warnings":[]}},
				{"package":"plain","installed":false,"plan":{"Package":"plain","Services":[],"Backup":false,"BackupWhy":"","Affected":[],"Warnings":[]}}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/updates/jellyfin/apply":
			_, _ = w.Write([]byte(`{"id":13}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/updates/plain/apply":
			_, _ = w.Write([]byte(`{"id":0,"detail":"already current"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestUpdatesList(t *testing.T) {
	url := updatesStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "updates", "list"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{
		"jellyfin updates:",
		"jellyfin/jellyfin:12.1 -> jellyfin/jellyfin:12.2",
		"backup required: backup paths declared",
		"plain not installed",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if code, _, _ := invoke(t, []string{"--server", url, "updates"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestUpdatesApply(t *testing.T) {
	url := updatesStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "updates", "apply", "jellyfin", "--backup"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "operation: 13") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "updates", "apply", "plain"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "already current") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, _ = invoke(t, []string{"--server", url, "updates", "apply"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func backupStub(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/destination":
			_, _ = w.Write([]byte(`{"destination":"/mnt/backup"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/schedules":
			_, _ = w.Write([]byte(`[{"package":"jellyfin","interval_seconds":86400,"retain":7,"enabled":true,"actor":"owner"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/schedules":
			_, _ = w.Write([]byte(`{"package":"jellyfin","interval_seconds":86400,"retain":7,"enabled":true,"actor":"owner"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/backups/schedules/jellyfin":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestBackupDestination(t *testing.T) {
	url := backupStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "backup", "destination"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "/mnt/backup") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestBackupSchedules(t *testing.T) {
	url := backupStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "backup", "schedules"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "jellyfin every 86400s") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestBackupSchedule(t *testing.T) {
	url := backupStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "backup", "schedule",
		"--package", "jellyfin", "--interval", "24", "--retain", "7", "--actor", "owner"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "jellyfin every 86400s") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, _, stderr := invoke(t, []string{"--server", url, "backup", "schedule",
		"--package", "jellyfin", "--interval", "0", "--actor", "owner"})
	if code != exitUsage || !strings.Contains(stderr, "--interval") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	code, _, _ = invoke(t, []string{"--server", url, "backup", "schedule", "--package", "jellyfin"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func TestBackupUnschedule(t *testing.T) {
	url := backupStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "backup", "unschedule", "--package", "jellyfin"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "unscheduled jellyfin") {
		t.Fatalf("stdout = %q", stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup"}); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
}

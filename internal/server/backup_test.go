package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

func backupTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountBackups(s)
	return srv
}

func TestBackupSchedulesFlow(t *testing.T) {
	srv := backupTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules", nil)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", map[string]any{
		"package": "jellyfin", "interval_seconds": 86400, "retain": 7, "enabled": true, "actor": "owner",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var saved struct {
		Package string `json:"Package"`
		Retain  int    `json:"Retain"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if saved.Package != "jellyfin" || saved.Retain != 7 {
		t.Fatalf("saved = %+v", saved)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/jellyfin", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/backups/schedules/jellyfin", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d", recorder.Code)
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/jellyfin", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestBackupScheduleRefusals(t *testing.T) {
	srv := backupTestServer(t)
	// Short intervals refuse: hourly floors, never tighter.
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", map[string]any{
		"package": "jellyfin", "interval_seconds": 60, "retain": 7, "enabled": true, "actor": "owner",
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestBackupDestinationUnassigned(t *testing.T) {
	srv := backupTestServer(t)
	// No backup assignment on a fresh store: the destination names
	// its absence instead of guessing a path.
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/destination", nil)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "backup") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

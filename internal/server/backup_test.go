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
	srv.MountBackups(s, t.TempDir())
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
		Package string `json:"package"`
		Retain  int    `json:"retain"`
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

func TestBackupScheduleStatusCodes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0")
	srv.MountBackups(st, t.TempDir())

	body := func(actor string, seconds int) map[string]any {
		return map[string]any{"package": "jellyfin", "interval_seconds": seconds, "retain": 1, "enabled": true, "actor": actor}
	}
	for name, b := range map[string]map[string]any{"short": body("owner", 60), "blank actor": body("   ", 3600)} {
		if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", b); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: code = %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: code = %d", rec.Code)
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", body("  owner  ", 3600))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"actor":"owner"`) ||
		!strings.Contains(rec.Body.String(), `"interval_seconds":3600`) {
		t.Fatalf("save: code = %d (%s)", rec.Code, rec.Body.String())
	}

	// Store failures are server errors, not 404 or 422.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/backups/schedules/jellyfin", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get on broken store: code = %d", rec.Code)
	}
	if rec := serveRequest(t, srv, http.MethodPost, "/api/v1/backups/schedules", body("owner", 3600)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save on broken store: code = %d", rec.Code)
	}
}

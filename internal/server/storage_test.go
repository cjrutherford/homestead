package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

func storageTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountStorage(s)
	return srv
}

func TestStorageMounts(t *testing.T) {
	srv := storageTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/storage/mounts", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var mounts []struct {
		Mountpoint string `json:"Mountpoint"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &mounts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(mounts) == 0 {
		t.Fatal("no mounts discovered")
	}
	found := false
	for _, mount := range mounts {
		if mount.Mountpoint == "/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mounts = %+v, want /", mounts)
	}
}

func TestStorageRegisterAssignFlow(t *testing.T) {
	srv := storageTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/storage/registrations", nil)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations",
		map[string]any{"mountpoint": "/", "label": "root", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"Label":"root"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/storage/assignments", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"Class":"application"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestStorageRefusals(t *testing.T) {
	srv := storageTestServer(t)
	// Phantom paths refuse: validation runs against live discovery.
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations",
		map[string]any{"mountpoint": "/no/such/mount", "label": "ghost", "actor": "owner"})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Unknown classes refuse before touching storage.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "telepathy", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Unregistered mounts refuse assignment.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

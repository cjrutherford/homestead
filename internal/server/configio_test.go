package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/desired"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// configIOTestServer mounts export/import over scratch state: temp
// desired file with settings, temp secret key with one secret.
func configIOTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	if err := secrets.Set("dns_api_token", "token-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	desiredPath := filepath.Join(dir, "desired.yaml")
	if err := desired.Save(desiredPath, desired.State{Version: desired.CurrentVersion, Settings: map[string]any{"hostname": "homestead"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	srv := New("127.0.0.1:0")
	srv.MountConfigIO(desiredPath, secrets)
	return srv, desiredPath
}

func TestConfigExport(t *testing.T) {
	srv, _ := configIOTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/config/export", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var export desired.Export
	if err := json.Unmarshal(recorder.Body.Bytes(), &export); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if export.Settings["hostname"] != "homestead" {
		t.Fatalf("export = %+v", export)
	}
	if len(export.Secrets) != 1 || export.Secrets[0] != "dns_api_token" {
		t.Fatalf("export = %+v", export)
	}
	if strings.Contains(recorder.Body.String(), "token-value") {
		t.Fatal("export leaks secret value")
	}
}

func TestConfigExportMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	secrets, err := secret.New(s, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	srv := New("127.0.0.1:0")
	srv.MountConfigIO(filepath.Join(dir, "missing.yaml"), secrets)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/config/export", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestConfigImport(t *testing.T) {
	srv, desiredPath := configIOTestServer(t)
	body := map[string]any{
		"document": map[string]any{
			"version": 1, "homestead": "0.0.0-test",
			"store_schema": 1, "desired_version": 1,
			"settings": map[string]any{"hostname": "recovered"},
			"secrets":  []string{"dns_api_token"},
		},
		"secrets": map[string]string{"dns_api_token": "new-value"},
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"secrets":1`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	state, err := desired.Load(desiredPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Settings["hostname"] != "recovered" {
		t.Fatalf("settings = %+v", state.Settings)
	}
	// Missing values refuse naming the gap.
	broken := map[string]any{
		"document": body["document"],
		"secrets":  map[string]string{},
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", broken)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "dns_api_token") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	// Malformed bodies refuse before touching anything.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Well-formed but unsatisfiable documents refuse.
	refused := map[string]any{
		"document": map[string]any{"version": 99},
		"secrets":  map[string]string{},
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", refused)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
}

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
	srv.MountConfigIO(desiredPath, secrets, nil, nil)
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
	srv.MountConfigIO(filepath.Join(dir, "missing.yaml"), secrets, nil, nil)
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

// S483: the YAML export is byte-for-byte what the CLI writes and holds
// names only; the preview diffs without writing and refuses bad input.
func TestConfigExportYAMLAndPreview(t *testing.T) {
	srv, desiredPath := configIOTestServer(t)
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/config/export?format=yaml", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "yaml") {
		t.Fatalf("code = %d type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	text := rec.Body.String()
	if !strings.Contains(text, "dns_api_token") || strings.Contains(text, "token-value") {
		t.Fatalf("yaml export wrong: %s", text)
	}
	// Round trip: previewing a fresh export changes nothing.
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import/preview", map[string]string{"text": text})
	var preview desired.ImportPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !preview.Valid || len(preview.Changes) != 0 || len(preview.Secrets) != 1 {
		t.Fatalf("round trip preview = %+v", preview)
	}
	// A different hostname shows as a change and nothing is written.
	edited := strings.Replace(text, "hostname: homestead", "hostname: other", 1)
	rec = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import/preview", map[string]string{"text": edited})
	preview = desired.ImportPreview{}
	_ = json.Unmarshal(rec.Body.Bytes(), &preview)
	if !preview.Valid || len(preview.Changes) != 1 || preview.Changes[0].Key != "hostname" {
		t.Fatalf("edited preview = %+v", preview)
	}
	state, err := desired.Load(desiredPath)
	if err != nil || state.Settings["hostname"] != "homestead" {
		t.Fatalf("preview wrote: %+v %v", state, err)
	}
	// Garbage and too-new documents are reported, not applied.
	for _, bad := range []string{"::: not yaml [", "version: 99\n"} {
		rec = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import/preview", map[string]string{"text": bad})
		preview = desired.ImportPreview{}
		_ = json.Unmarshal(rec.Body.Bytes(), &preview)
		if rec.Code != http.StatusOK || preview.Valid || preview.Error == "" {
			t.Fatalf("bad %q preview = %d %+v", bad, rec.Code, preview)
		}
	}
}

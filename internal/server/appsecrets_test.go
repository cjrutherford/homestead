package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/desired"
	"homestead/internal/secret"
	"homestead/internal/store"
)

func appSecretsServer(t *testing.T) (*Server, *secret.Store, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := secret.New(st, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0")
	srv.MountAppSecrets(st, "../../catalog", secrets)
	return srv, secrets, st
}

func TestAppSecretsListShowsPresenceNeverValues(t *testing.T) {
	srv, secrets, _ := appSecretsServer(t)
	if err := secrets.Set("app_authentik_db_password", "existing-value-123"); err != nil {
		t.Fatal(err)
	}
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/authentik/secrets", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "existing-value-123") {
		t.Fatal("list leaks a value")
	}
	var out []appSecretStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || !out[0].Present || !out[0].Database || out[1].Present || !out[1].Immutable {
		t.Fatalf("out = %+v", out)
	}
	if rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/ghost/secrets", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown package code = %d", rec.Code)
	}
}

func TestAppSecretRegister(t *testing.T) {
	srv, secrets, st := appSecretsServer(t)
	path := "/api/v1/catalog/authentik/secrets/db_password"

	// Verification needs the running database; with none, nothing is stored.
	rec := serveRequest(t, srv, http.MethodPost, path, map[string]any{"value": "old-password-xyz"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unverifiable code = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := secrets.Get("app_authentik_db_password"); err == nil {
		t.Fatal("unverified password stored")
	}

	rec = serveRequest(t, srv, http.MethodPost, path, map[string]any{"value": "old-password-xyz", "skip_verify": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := secrets.Get("app_authentik_db_password"); got != "old-password-xyz" {
		t.Fatalf("stored = %q", got)
	}
	if strings.Contains(rec.Body.String(), "old-password-xyz") {
		t.Fatal("response echoes the value")
	}
	for _, bad := range []string{"/api/v1/catalog/authentik/secrets/ghost", "/api/v1/catalog/ghost/secrets/db_password"} {
		if rec := serveRequest(t, srv, http.MethodPost, bad, map[string]any{"value": "x", "skip_verify": true}); rec.Code < 400 {
			t.Fatalf("%s accepted", bad)
		}
	}
	entries, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Detail, "old-password-xyz") || strings.Contains(e.Action+e.Detail, "old-password") {
			t.Fatalf("audit leaks the value: %+v", e)
		}
		found = found || e.Action == "app.secret.register"
	}
	if !found {
		t.Fatal("registration not audited")
	}
}

func TestDatabasePasswordResolvesFromSecretStore(t *testing.T) {
	_, secrets, _ := appSecretsServer(t)
	entries, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := entries.Get("authentik")

	_, err = databasePassword(secrets, manifest)
	if err == nil || !strings.Contains(err.Error(), "app_authentik_db_password") ||
		!strings.Contains(err.Error(), "apps secrets register") {
		t.Fatalf("missing-secret error = %v", err)
	}
	if _, err := databasePassword(nil, manifest); err == nil {
		t.Fatal("nil store resolved a password")
	}
	if err := secrets.Set("app_authentik_db_password", "resolved-pw"); err != nil {
		t.Fatal(err)
	}
	if got, err := databasePassword(secrets, manifest); err != nil || got != "resolved-pw" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestConfigExportExcludesAppSecrets(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := secret.New(st, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	_ = secrets.Set("app_authentik_db_password", "app-value")
	_ = secrets.Set("dns_api_token", "token-value")
	desiredPath := filepath.Join(dir, "desired.yaml")
	if err := desired.Save(desiredPath, desired.State{Version: desired.CurrentVersion, Settings: map[string]any{"hostname": "h"}}); err != nil {
		t.Fatal(err)
	}
	export, err := desired.ExportState(desiredPath, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Secrets) != 1 || export.Secrets[0] != "dns_api_token" {
		t.Fatalf("export secrets = %v, want only dns_api_token", export.Secrets)
	}
}

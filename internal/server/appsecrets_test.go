package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
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
	if err := secrets.SetApp("app_authentik_db_password", "existing-value-123"); err != nil {
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
	if err := secrets.SetApp("app_authentik_db_password", "resolved-pw"); err != nil {
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
	_ = secrets.SetApp("app_authentik_db_password", "app-value")
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

func TestUninstallRefusesToOrphanVolumeData(t *testing.T) {
	env := newMigrationEnv(t)
	env.srv.MountCatalog(env.st, filepath.Join(filepath.Dir(env.mount), "catalog"), nil)

	rec := env.do(t, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "homesteadctl data plan demo") {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	// An explicit acknowledgement proceeds and is audited.
	rec = env.do(t, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{"abandon_volumes": true}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledged uninstall code = %d: %s", rec.Code, rec.Body)
	}
	entries, err := audit.New(env.st).List(50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		found = found || (e.Action == "app.uninstall.abandon-volumes" && e.Actor == "owner")
	}
	if !found {
		t.Fatal("abandon-volumes acknowledgement not audited")
	}
}

func TestRegisterRejectsMalformedBodyGenerically(t *testing.T) {
	srv, _, _ := appSecretsServer(t)
	// Decode errors are generic: they never echo request content.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/authentik/secrets/db_password", strings.NewReader(`{"value": "echo-me-not" "x"`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != "invalid request" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body)
	}
}

func TestSecretsListReportsWiringState(t *testing.T) {
	srv, _, _ := appSecretsServer(t)
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/authentik/secrets", nil)
	var out []appSecretStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) == 0 || out[0].Wired || out[0].Installed {
		t.Fatalf("out = %+v", out)
	}
}

// S312: a secret the package marks reveal (Nextcloud's admin password)
// can be read by the owner with a confirm, audited without the value;
// every other secret stays presence-only.
func TestAppSecretReveal(t *testing.T) {
	srv, secrets, st := appSecretsServer(t)
	reveal := func(path string, body any, session bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
		if session {
			req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
				operatorSession{Session: auth.VerifiedSession{Username: "owner"}}))
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	path := "/api/v1/catalog/nextcloud/secrets/admin_password/reveal"
	confirm := map[string]any{"confirm": true}
	if rec := reveal(path, confirm, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", rec.Code)
	}
	if rec := reveal(path, map[string]any{}, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("no confirm = %d", rec.Code)
	}
	if rec := reveal(path, confirm, true); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "created when Nextcloud is installed") {
		t.Fatalf("missing = %d %s", rec.Code, rec.Body.String())
	}
	if err := secrets.SetApp(catalog.SecretStoreName("nextcloud", "admin_password"), "generated-admin-pw"); err != nil {
		t.Fatal(err)
	}
	if err := secrets.SetApp("app_authentik_db_password", "db-pw-not-shown"); err != nil {
		t.Fatal(err)
	}
	if rec := reveal("/api/v1/catalog/authentik/secrets/db_password/reveal", confirm, true); rec.Code != http.StatusForbidden ||
		strings.Contains(rec.Body.String(), "db-pw-not-shown") {
		t.Fatalf("non-reveal secret = %d %s", rec.Code, rec.Body.String())
	}
	if rec := reveal("/api/v1/catalog/nextcloud/secrets/ghost/reveal", confirm, true); rec.Code != http.StatusNotFound {
		t.Fatalf("undeclared = %d", rec.Code)
	}
	rec := reveal(path, confirm, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal = %d %v", rec.Code, rec.Header())
	}
	var out struct{ Name, Value, Note string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Value != "generated-admin-pw" || !strings.Contains(out.Note, `"admin"`) {
		t.Fatalf("out = %+v, %v", out, err)
	}
	// The list marks it revealable, still without the value.
	list := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/nextcloud/secrets", nil)
	if strings.Contains(list.Body.String(), "generated-admin-pw") || !strings.Contains(list.Body.String(), `"reveal":true`) {
		t.Fatalf("list = %s", list.Body.String())
	}
	entries, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Detail, "generated-admin-pw") {
			t.Fatalf("audit leaks the value: %+v", e)
		}
		found = found || (e.Action == "app.secret.reveal" && e.Actor == "owner")
	}
	if !found {
		t.Fatal("reveal not audited")
	}
}

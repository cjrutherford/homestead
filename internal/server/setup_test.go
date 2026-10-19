package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/secret"
	"homestead/internal/setup"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func setupTestServer(t *testing.T) (*Server, *store.Store, *secret.Store) {
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
	srv.MountWizards(wizard.New(st))
	srv.MountSetup(SetupConfig{Store: st, Secrets: secrets, Wizards: wizard.New(st), CatalogDir: "../../catalog"})
	return srv, st, secrets
}

func decodeSetup(t *testing.T, body []byte) setup.Status {
	t.Helper()
	var s setup.Status
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return s
}

func TestSetupRoutesAreSessionClass(t *testing.T) {
	for _, p := range []string{"GET /api/v1/setup", "PUT /api/v1/setup/steps/{name}"} {
		if c, ok := Classify(p); !ok || c != ClassSession {
			t.Fatalf("%s class = %v %v, want session", p, c, ok)
		}
	}
}

func TestSetupStatusAndSteps(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	rec := serveRequest(t, srv, http.MethodGet, "/api/v1/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d %s", rec.Code, rec.Body)
	}
	s := decodeSetup(t, rec.Body.Bytes())
	if !s.Gated || s.Current != "account" || len(s.Steps) != 4 || len(s.Providers) != 4 {
		t.Fatalf("status = %+v", s)
	}
	if len(s.StarterApps) == 0 {
		t.Fatal("expected starter apps from the catalog")
	}
	rec = serveRequest(t, srv, http.MethodPut, "/api/v1/setup/steps/apps", map[string]string{"state": "skipped"})
	if rec.Code != http.StatusOK || decodeSetup(t, rec.Body.Bytes()).Steps[3].State != "skipped" {
		t.Fatalf("skip: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		step string
		body any
		code int
	}{
		{"nope", map[string]string{"state": "skipped"}, 404},
		{"apps", map[string]string{"bogus": "x"}, 400},
		{"apps", map[string]string{}, 400},
		{"account", map[string]string{"state": "done"}, 400},
		{"domain", map[string]string{"provider": "godaddy"}, 400},
		{"domain", map[string]string{"access": "tunnel"}, 400},
	} {
		if rec := serveRequest(t, srv, http.MethodPut, "/api/v1/setup/steps/"+c.step, c.body); rec.Code != c.code {
			t.Fatalf("%s %v: code %d, want %d (%s)", c.step, c.body, rec.Code, c.code, rec.Body)
		}
	}
}

func TestGenericWizardRoutesRefuseSetup(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	body := map[string]any{"step": "account", "payload": map[string]any{"origin": "legacy"}}
	if rec := serveRequest(t, srv, http.MethodPut, "/api/v1/wizards/setup", body); rec.Code != http.StatusForbidden {
		t.Fatalf("PUT = %d", rec.Code)
	}
	if rec := serveRequest(t, srv, http.MethodDelete, "/api/v1/wizards/setup", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	// Other wizards still work.
	if rec := serveRequest(t, srv, http.MethodPut, "/api/v1/wizards/other", body); rec.Code != http.StatusOK {
		t.Fatalf("other PUT = %d", rec.Code)
	}
}

// Credentials are sealed per ADR-021 and never appear in wizard payloads,
// audit details or any setup response.
func TestSetupNeverCarriesCredentials(t *testing.T) {
	srv, st, secrets := setupTestServer(t)
	const sentinel = "S3NTINEL-TOKEN-VALUE-9f2"
	if err := secrets.Set("dns_cloudflare_api_token", sentinel); err != nil {
		t.Fatal(err)
	}
	wizard.New(st).Put("domain", "credentials", map[string]any{"provider": "cloudflare", "zone": "example.com", "base": "home"})
	var all strings.Builder
	get := serveRequest(t, srv, http.MethodGet, "/api/v1/setup", nil)
	all.Write(get.Body.Bytes())
	s := decodeSetup(t, get.Body.Bytes())
	if !s.Facts.Domain.CredentialsSealed || !s.Facts.Domain.Configured {
		t.Fatalf("expected sealed credentials to be noticed by presence: %+v", s.Facts.Domain)
	}
	for _, body := range []map[string]string{
		{"provider": "cloudflare", "access": "tunnel"},
		{"state": "done"},
	} {
		rec := serveRequest(t, srv, http.MethodPut, "/api/v1/setup/steps/domain", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("put: %d %s", rec.Code, rec.Body)
		}
		all.Write(rec.Body.Bytes())
	}
	rows, err := st.DB().Query(`SELECT wizard, step, payload FROM wizard_state`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var w, step, payload string
		if err := rows.Scan(&w, &step, &payload); err != nil {
			t.Fatal(err)
		}
		all.WriteString(w + step + payload)
	}
	entries, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		all.WriteString(e.Actor + e.Action + e.Detail)
		if e.Action == "setup.step" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected setup.step audit entries")
	}
	if strings.Contains(all.String(), sentinel) {
		t.Fatal("credential value leaked into a setup response, wizard row or audit entry")
	}
}

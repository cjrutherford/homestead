package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/firewall"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// S502: import and its preview validate settings against the config
// schema, and a refused import changes neither secrets nor settings.
func TestConfigImportValidatesSettings(t *testing.T) {
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
	if err := secrets.Set("dns_api_token", "old-value"); err != nil {
		t.Fatal(err)
	}
	desiredPath := filepath.Join(dir, "desired.yaml")
	before := map[string]any{firewall.ModeField: firewall.ModeManaged}
	if err := desired.Save(desiredPath, desired.State{Version: desired.CurrentVersion, Settings: before}); err != nil {
		t.Fatal(err)
	}
	svc := config.New(firewall.Schema(), desiredPath)
	srv := New("127.0.0.1:0")
	srv.MountConfigIO(desiredPath, secrets, nil, svc.ValidateSettings)

	for name, settings := range map[string]map[string]any{
		"unknown key": {"no_such_setting": "x"},
		"wrong type":  {firewall.ModeField: 42},
	} {
		doc := map[string]any{
			"version": 1, "homestead": "0.0.0-test", "store_schema": 1, "desired_version": 1,
			"settings": settings, "secrets": []string{"dns_api_token"},
		}
		text, _ := json.Marshal(doc)
		rec := serveRequest(t, srv, http.MethodPost, "/api/v1/config/import/preview", map[string]string{"text": string(text)})
		var preview desired.ImportPreview
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Valid || preview.Error == "" {
			t.Errorf("%s: preview = %+v, want an error", name, preview)
		}
		rec = serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", map[string]any{
			"document": doc, "secrets": map[string]string{"dns_api_token": "new-value"},
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: import code = %d (%s)", name, rec.Code, rec.Body.String())
		}
		if got, _ := secrets.Get("dns_api_token"); got != "old-value" {
			t.Errorf("%s: secret changed to %q", name, got)
		}
		state, err := desired.Load(desiredPath)
		if err != nil || len(state.Settings) != 1 || state.Settings[firewall.ModeField] != firewall.ModeManaged {
			t.Errorf("%s: settings changed: %+v %v", name, state, err)
		}
	}

	// A valid document still imports.
	ok := map[string]any{
		"version": 1, "homestead": "0.0.0-test", "store_schema": 1, "desired_version": 1,
		"settings": map[string]any{firewall.ModeField: firewall.ModeExternal}, "secrets": []string{"dns_api_token"},
	}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/config/import", map[string]any{
		"document": ok, "secrets": map[string]string{"dns_api_token": "new-value"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid import = %d (%s)", rec.Code, rec.Body.String())
	}
}

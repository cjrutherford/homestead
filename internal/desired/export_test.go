package desired

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/secret"
	"homestead/internal/store"
)

func openExportSecret(t *testing.T) (*store.Store, *secret.Store) {
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
	return s, secrets
}

func writeDesired(t *testing.T, settings map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desired.yaml")
	if err := Save(path, State{Version: CurrentVersion, Settings: settings}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

func TestExportRoundTrip(t *testing.T) {
	_, secrets := openExportSecret(t)
	for name, value := range map[string]string{"dns_api_token": "super-secret-value", "db_pass": "another-secret-value"} {
		if err := secrets.Set(name, value); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	path := writeDesired(t, map[string]any{"hostname": "homestead", "memory": 8})
	export, err := ExportState(path, secrets)
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	if export.Version != ExportVersion || export.Homestead == "" ||
		export.StoreSchema <= 0 || export.DesiredVersion != CurrentVersion {
		t.Fatalf("export = %+v", export)
	}
	if export.Settings["hostname"] != "homestead" {
		t.Fatalf("settings = %+v", export.Settings)
	}
	if len(export.Secrets) != 2 || export.Secrets[0] != "db_pass" || export.Secrets[1] != "dns_api_token" {
		t.Fatalf("secrets = %+v", export.Secrets)
	}
	if time.Since(export.ExportedAt) > time.Minute {
		t.Fatalf("exported_at = %v", export.ExportedAt)
	}
	out := filepath.Join(t.TempDir(), "export.yaml")
	if err := WriteExport(out, export); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The leak proof: values never reach the document.
	for _, value := range []string{"super-secret-value", "another-secret-value"} {
		if strings.Contains(string(raw), value) {
			t.Fatalf("export leaks secret value %q", value)
		}
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, err = %v", info, err)
	}
	back, err := ReadExport(out)
	if err != nil {
		t.Fatalf("ReadExport: %v", err)
	}
	if back.Version != export.Version || back.Homestead != export.Homestead ||
		back.StoreSchema != export.StoreSchema || len(back.Secrets) != 2 ||
		back.Settings["hostname"] != "homestead" {
		t.Fatalf("back = %+v", back)
	}
}

func TestExportRefusals(t *testing.T) {
	_, secrets := openExportSecret(t)
	path := writeDesired(t, map[string]any{"hostname": "homestead"})
	if _, err := ExportState(path, nil); err == nil {
		t.Fatal("nil secret store accepted")
	}
	if _, err := ExportState(filepath.Join(t.TempDir(), "missing.yaml"), secrets); err == nil {
		t.Fatal("missing desired file accepted")
	}
	if err := WriteExport(filepath.Join(t.TempDir(), "out.yaml"), Export{}); err == nil {
		t.Fatal("empty export written")
	}
	if _, err := ReadExport(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing export read")
	}
	future := filepath.Join(t.TempDir(), "future.yaml")
	if err := os.WriteFile(future, []byte("version: 99\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadExport(future); err == nil {
		t.Fatal("future version read")
	}
	typo := filepath.Join(t.TempDir(), "typo.yaml")
	if err := os.WriteFile(typo, []byte("version: 1\nsetings: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadExport(typo); err == nil {
		t.Fatal("unknown field read")
	}
}

func TestExportEmpty(t *testing.T) {
	_, secrets := openExportSecret(t)
	path := writeDesired(t, nil)
	export, err := ExportState(path, secrets)
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	if len(export.Secrets) != 0 {
		t.Fatalf("secrets = %+v", export.Secrets)
	}
	out := filepath.Join(t.TempDir(), "export.yaml")
	if err := WriteExport(out, export); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	back, err := ReadExport(out)
	if err != nil {
		t.Fatalf("ReadExport: %v", err)
	}
	if back.Version != ExportVersion {
		t.Fatalf("back = %+v", back)
	}
}

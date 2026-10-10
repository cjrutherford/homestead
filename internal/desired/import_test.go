package desired

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/secret"
	"homestead/internal/store"
)

func openImportSecret(t *testing.T) (*secret.Store, string) {
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
	return secrets, dir
}

// exportFixture builds an export from one store: desired settings
// plus two secrets, written to a document.
func exportFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	secrets, dir := openImportSecret(t)
	values := map[string]string{"dns_api_token": "token-value", "db_pass": "db-value"}
	for name, value := range values {
		if err := secrets.Set(name, value); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	desiredPath := filepath.Join(dir, "desired.yaml")
	if err := Save(desiredPath, State{Version: CurrentVersion, Settings: map[string]any{"hostname": "homestead"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	export, err := ExportState(desiredPath, secrets)
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	doc := filepath.Join(dir, "export.yaml")
	if err := WriteExport(doc, export); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	return doc, values
}

func TestImportRoundTrip(t *testing.T) {
	doc, values := exportFixture(t)
	// Fresh machine: new store, new secret key, empty directory.
	secrets, dir := openImportSecret(t)
	desiredPath := filepath.Join(dir, "desired.yaml")
	summary, err := ImportState(doc, desiredPath, secrets, values)
	if err != nil {
		t.Fatalf("ImportState: %v", err)
	}
	if summary.Settings != 1 || summary.Secrets != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	state, err := Load(desiredPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Settings["hostname"] != "homestead" {
		t.Fatalf("settings = %+v", state.Settings)
	}
	// Values decrypt under the NEW key: recovery, not copying.
	for name, want := range values {
		got, err := secrets.Get(name)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		if got != want {
			t.Fatalf("Get %s = %q", name, got)
		}
	}
	// Re-running converges on replaced values.
	values["db_pass"] = "rotated-value"
	if _, err := ImportState(doc, desiredPath, secrets, values); err != nil {
		t.Fatalf("ImportState: %v", err)
	}
	got, err := secrets.Get("db_pass")
	if err != nil || got != "rotated-value" {
		t.Fatalf("Get = %q, err = %v", got, err)
	}
}

func TestImportRefusals(t *testing.T) {
	doc, values := exportFixture(t)
	secrets, dir := openImportSecret(t)
	desiredPath := filepath.Join(dir, "desired.yaml")
	if _, err := ImportState(doc, desiredPath, nil, values); err == nil {
		t.Fatal("nil secret store accepted")
	}
	if _, err := ImportState(filepath.Join(dir, "missing.yaml"), desiredPath, secrets, values); err == nil {
		t.Fatal("missing document accepted")
	}
	// Missing values refuse naming every gap, writing nothing.
	partial := map[string]string{"dns_api_token": "token-value"}
	if _, err := ImportState(doc, desiredPath, secrets, partial); err == nil ||
		!strings.Contains(err.Error(), "db_pass") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(desiredPath); !os.IsNotExist(err) {
		t.Fatal("desired file written despite missing secrets")
	}
	// Empty values count as missing.
	blank := map[string]string{"dns_api_token": "token-value", "db_pass": ""}
	if _, err := ImportState(doc, desiredPath, secrets, blank); err == nil {
		t.Fatal("blank secret value accepted")
	}
	// Extras refuse rather than stashing unrelated material.
	extra := map[string]string{"dns_api_token": "t", "db_pass": "d", "stowaway": "s"}
	if _, err := ImportState(doc, desiredPath, secrets, extra); err == nil {
		t.Fatal("extra secret value accepted")
	}
	if _, err := os.Stat(desiredPath); !os.IsNotExist(err) {
		t.Fatal("desired file written despite extras")
	}
}

func TestImportCompatibility(t *testing.T) {
	secrets, dir := openImportSecret(t)
	desiredPath := filepath.Join(dir, "desired.yaml")
	values := map[string]string{}
	write := func(export Export) string {
		t.Helper()
		doc := filepath.Join(t.TempDir(), "export.yaml")
		if err := WriteExport(doc, export); err != nil {
			t.Fatalf("WriteExport: %v", err)
		}
		return doc
	}
	newer := Export{Version: ExportVersion, StoreSchema: 9999, DesiredVersion: CurrentVersion}
	if _, err := ImportState(write(newer), desiredPath, secrets, values); err == nil {
		t.Fatal("newer store schema accepted")
	}
	newerDesired := Export{Version: ExportVersion, StoreSchema: 1, DesiredVersion: 9999}
	if _, err := ImportState(write(newerDesired), desiredPath, secrets, values); err == nil {
		t.Fatal("newer desired version accepted")
	}
	unversioned := Export{Version: ExportVersion, StoreSchema: 1, DesiredVersion: 0}
	if _, err := ImportState(write(unversioned), desiredPath, secrets, values); err == nil {
		t.Fatal("unversioned desired accepted")
	}
	// Invalid secret names fail before the desired file lands.
	poisoned := Export{Version: ExportVersion, StoreSchema: 1, DesiredVersion: CurrentVersion, Secrets: []string{"bad name!"}}
	if _, err := ImportState(write(poisoned), desiredPath, secrets, map[string]string{"bad name!": "x"}); err == nil {
		t.Fatal("invalid secret name accepted")
	}
	if _, err := os.Stat(desiredPath); !os.IsNotExist(err) {
		t.Fatal("desired file written despite bad secret name")
	}
}

package desired

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/layout"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, New()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
}

func TestSavedFileIsHumanReadableYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, New()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "version: 1") {
		t.Fatalf("saved file not plain version YAML:\n%s", data)
	}
}

func TestSavedFileModeIsCustomerReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, New()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %o, want 644", info.Mode().Perm())
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Save(filepath.Join(dir, FileName), New()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %q, want only %q", names, FileName)
	}
}

func TestSaveReplacesExistingAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("version: 999\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := Save(path, New()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != CurrentVersion {
		t.Fatalf("Version = %d, want %d", got.Version, CurrentVersion)
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("version: 999\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted newer version, want error")
	}
}

func TestLoadRejectsMissingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted versionless file, want error")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("version: 1\nverzion: 2\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted unknown field, want error")
	}
}

func TestLoadMissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("Load of missing file succeeded, want error")
	}
}

func TestLoadMissingFileIsNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load error = %v, want errors.Is ErrNotFound", err)
	}
}

func TestSaveInvalidStateTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := Save(filepath.Join(dir, FileName), State{Version: 999}); err == nil {
		t.Fatal("Save accepted newer version, want error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid Save left %d files behind", len(entries))
	}
}

func TestDefaultPathLivesUnderEtcDir(t *testing.T) {
	if filepath.Dir(DefaultPath()) != layout.EtcDir.Path {
		t.Fatalf("DefaultPath dir = %q, want %q", filepath.Dir(DefaultPath()), layout.EtcDir.Path)
	}
	if filepath.Base(DefaultPath()) != FileName {
		t.Fatalf("DefaultPath base = %q, want %q", filepath.Base(DefaultPath()), FileName)
	}
}

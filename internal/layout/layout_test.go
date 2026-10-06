package layout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirsMatchArchitecture(t *testing.T) {
	want := map[string]os.FileMode{
		"/etc/homestead":        0o755,
		"/var/lib/homestead":    0o700,
		"/var/log/homestead":    0o755,
		"/run/homestead":        0o755,
		"/opt/homestead/stacks": 0o755,
	}
	if len(Dirs) != len(want) {
		t.Fatalf("len(Dirs) = %d, want %d", len(Dirs), len(want))
	}
	for _, dir := range Dirs {
		mode, ok := want[dir.Path]
		if !ok {
			t.Fatalf("unexpected directory %q", dir.Path)
		}
		if dir.Mode != mode {
			t.Fatalf("%s mode = %o, want %o", dir.Path, dir.Mode, mode)
		}
		if dir.Purpose == "" {
			t.Fatalf("%s has no documented purpose", dir.Path)
		}
	}
}

func TestLibDirIsPrivate(t *testing.T) {
	if LibDir.Mode != 0o700 {
		t.Fatalf("LibDir mode = %o, want 0700 (sessions/tokens live here)", LibDir.Mode)
	}
}

func TestEnsureCreatesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := Dir{Path: filepath.Join(root, "nested", "homestead"), Mode: 0o755}
	if err := Ensure(dir); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := Ensure(dir); err != nil {
		t.Fatalf("Ensure re-run: %v", err)
	}
	info, err := os.Stat(dir.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir.Path)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestEnsureRepairsModeDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homestead")
	if err := os.MkdirAll(path, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := Ensure(Dir{Path: path, Mode: 0o700}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", info.Mode().Perm())
	}
}

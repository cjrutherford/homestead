package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/backup"
	"homestead/internal/docker"
	"homestead/internal/storage"
)

func TestBackupGuard(t *testing.T) {
	manifest, mountpoints, mount := guardFixture(t)
	source := []backup.Source{{Mountpoint: mount, Path: "demo", Class: storage.ClassApplication}}
	web := func(mounts ...docker.Mount) []docker.Container {
		return []docker.Container{{Labels: map[string]string{docker.LabelService: "web"}, Mounts: mounts}}
	}
	data := filepath.Join(mount, "demo", "data")
	bind := docker.Mount{Type: "bind", Source: data, Destination: "/data"}
	fails := func(t *testing.T, containers []docker.Container, sources []backup.Source, want string) {
		t.Helper()
		_, err := guardBackupSources(manifest, mountpoints, sources, containers)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want error containing %q, got %v", want, err)
		}
	}

	t.Run("missing declared path fails even beside a stray directory", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(mount, "demo", "stray"), 0o755); err != nil {
			t.Fatal(err)
		}
		fails(t, web(bind), source, "is missing")
	})
	t.Run("empty declared path warns", func(t *testing.T) {
		if err := os.MkdirAll(data, 0o755); err != nil {
			t.Fatal(err)
		}
		warnings, err := guardBackupSources(manifest, mountpoints, source, web(bind))
		if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "empty") {
			t.Fatalf("warnings = %v err = %v", warnings, err)
		}
	})
	if err := os.WriteFile(filepath.Join(data, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("declared path in a volume fails", func(t *testing.T) {
		fails(t, web(docker.Mount{Type: "volume", Name: "v", Destination: "/data"}), source, "Docker volume v")
	})
	t.Run("declared path in the container layer fails", func(t *testing.T) {
		fails(t, web(), source, "writable layer")
	})
	t.Run("tmpfs at a declared persistent path fails", func(t *testing.T) {
		fails(t, web(docker.Mount{Type: "tmpfs", Destination: "/data"}), source, "tmpfs")
	})
	t.Run("declared bind outside sources fails", func(t *testing.T) {
		fails(t, web(docker.Mount{Type: "bind", Source: "/elsewhere", Destination: "/data"}), source, "differs from the declared")
	})
	t.Run("not covered by sources fails", func(t *testing.T) {
		fails(t, web(bind), nil, "not covered")
	})
	t.Run("undeclared image volume warns but passes", func(t *testing.T) {
		warnings, err := guardBackupSources(manifest, mountpoints, source,
			web(bind, docker.Mount{Type: "volume", Name: "imgvol", Destination: "/cache"}))
		if err != nil {
			t.Fatalf("an undeclared image volume must not block backups: %v", err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "imgvol") {
			t.Fatalf("warnings = %v", warnings)
		}
	})
	t.Run("healthy passes", func(t *testing.T) {
		warnings, err := guardBackupSources(manifest, mountpoints, source, web(bind))
		if err != nil || len(warnings) != 0 {
			t.Fatalf("warnings = %v err = %v", warnings, err)
		}
	})
}

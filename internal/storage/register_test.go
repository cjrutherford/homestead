package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/store"
)

func discoveredFixtures() discover.Storage {
	mount := func(point, fstype string, readOnly, accessible bool) discover.Mount {
		return discover.Mount{Mountpoint: point, FSType: fstype, ReadOnly: readOnly, Accessible: accessible}
	}
	return discover.Storage{Mounts: []discover.Mount{
		mount("/", "ext4", false, true),
		mount("/mnt/media", "ext4", false, true),
		mount("/mnt/backup", "ext4", false, true),
		mount("/mnt/readonly", "ext4", true, true),
		mount("/mnt/stale", "nfs", false, false),
		mount("/proc", "proc", false, true),
		mount("/dev/shm", "tmpfs", false, true),
	}}
}

func openRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewRegistry(s)
}

func TestRegisterFlow(t *testing.T) {
	registry := openRegistry(t)
	discovered := discoveredFixtures()
	got, err := registry.Register("/mnt/media", "media", "owner", discovered)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.Mountpoint != "/mnt/media" || got.Label != "media" || got.Actor != "owner" || got.AddedAt.IsZero() {
		t.Fatalf("registration = %+v", got)
	}
	fetched, err := registry.Get("/mnt/media")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched != got {
		t.Fatalf("fetched = %+v, want %+v", fetched, got)
	}
	if _, err := registry.Register("/mnt/backup", "backup", "owner", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	listed, err := registry.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 || listed[0].Mountpoint != "/mnt/backup" || listed[1].Mountpoint != "/mnt/media" {
		t.Fatalf("listed = %+v", listed)
	}
	// Re-registration renames without churn.
	renamed, err := registry.Register("/mnt/media", "videos", "owner", discovered)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if renamed.Label != "videos" {
		t.Fatalf("registration = %+v", renamed)
	}
	listed, err = registry.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %+v", listed)
	}
	if _, err := registry.Get("/mnt/missing"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterRefusals(t *testing.T) {
	registry := openRegistry(t)
	discovered := discoveredFixtures()
	if _, err := registry.Register("/mnt/media", "media", "owner", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cases := []struct {
		name       string
		mountpoint string
		label      string
		actor      string
		want       string
	}{
		{"unknown mount", "/mnt/ghost", "ghost", "owner", "not a discovered mount"},
		{"relative", "mnt/media", "media", "owner", "absolute path"},
		{"read-only", "/mnt/readonly", "ro", "owner", "read-only"},
		{"stale", "/mnt/stale", "stale", "owner", "no usage data"},
		{"proc", "/proc", "proc", "owner", "pseudo-filesystem"},
		{"tmpfs", "/dev/shm", "shm", "owner", "pseudo-filesystem"},
		{"bad label", "/mnt/backup", "Bad!", "owner", "label must match"},
		{"dup label", "/mnt/backup", "media", "owner", "already names"},
		{"no actor", "/mnt/backup", "backup", "", "actor must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.Register(tc.mountpoint, tc.label, tc.actor, discovered)
			if err == nil {
				t.Fatal("registered invalid approval")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestUnregister(t *testing.T) {
	registry := openRegistry(t)
	discovered := discoveredFixtures()
	if err := registry.Unregister("/mnt/ghost"); err != nil {
		t.Fatalf("unregister absent: %v", err)
	}
	if _, err := registry.Register("/mnt/media", "media", "owner", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Unregister("/mnt/media"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := registry.Get("/mnt/media"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v", err)
	}
	listed, err := registry.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed = %+v", listed)
	}
}

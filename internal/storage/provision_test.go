package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/store"
)

func provisionRegistry(t *testing.T, mountpoint string) *Registry {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	registry := NewRegistry(s)
	discovered := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: mountpoint, FSType: "ext4", Accessible: true},
	}}
	if _, err := registry.Register(mountpoint, "test", "owner", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return registry
}

func provisionBase(t *testing.T) (string, *Registry) {
	t.Helper()
	base := t.TempDir()
	return base, provisionRegistry(t, base)
}

func TestProvisionFlow(t *testing.T) {
	base, registry := provisionBase(t)
	dirs := []Directory{
		{Path: "apps/jellyfin/config", Mode: 0o750, Uid: UnsetID, Gid: UnsetID},
		{Path: "apps/jellyfin/data", Mode: 0o700, Uid: UnsetID, Gid: UnsetID},
	}
	if err := Provision(registry, base, dirs); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for _, dir := range dirs {
		info, err := os.Stat(filepath.Join(base, dir.Path))
		if err != nil || !info.IsDir() {
			t.Fatalf("missing %s: %v", dir.Path, err)
		}
		if info.Mode().Perm() != dir.Mode.Perm() {
			t.Fatalf("%s mode = %o", dir.Path, info.Mode().Perm())
		}
	}
	// Converges: wrong perms heal, reruns succeed.
	if err := os.Chmod(filepath.Join(base, "apps/jellyfin/config"), 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := Provision(registry, base, dirs); err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	info, err := os.Stat(filepath.Join(base, "apps/jellyfin/config"))
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("perms not healed: %v %v", info, err)
	}
}

func TestProvisionOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership needs root")
	}
	base, registry := provisionBase(t)
	owned := []Directory{{Path: "owned", Mode: 0o750, Uid: 0, Gid: 0}}
	if err := Provision(registry, base, owned); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "owned")); err != nil {
		t.Fatalf("missing: %v", err)
	}
}

func TestProvisionRefusals(t *testing.T) {
	base, registry := provisionBase(t)
	// Existing file never becomes a directory.
	blocker := filepath.Join(base, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Symlink escape refuses.
	outside := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cases := []struct {
		name string
		dirs []Directory
		want string
	}{
		{"empty", nil, "nothing to provision"},
		{"absolute", []Directory{{Path: "/abs", Mode: 0o750}}, "must be relative"},
		{"dotdot", []Directory{{Path: "../escape", Mode: 0o750}}, "inside the mount"},
		{"nested dotdot", []Directory{{Path: "a/../../escape", Mode: 0o750}}, "inside the mount"},
		{"world writable", []Directory{{Path: "open", Mode: 0o777}}, "world-write"},
		{"too open", func() []Directory { return []Directory{{Path: "open", Mode: 0o776}} }(), "world-write"},
		{"too closed", []Directory{{Path: "closed", Mode: 0o600}}, "0700-0775"},
		{"bad uid", []Directory{{Path: "x", Mode: 0o750, Uid: -2}}, "owner IDs"},
		{"file blocker", []Directory{{Path: "file", Mode: 0o750}}, "not a directory"},
		{"symlink", []Directory{{Path: "link/through", Mode: 0o750}}, "symlink"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Provision(registry, base, tc.dirs)
			if err == nil {
				t.Fatal("provisioned unsafe input")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	// Unregistered mounts refuse even when the directory exists.
	other := t.TempDir()
	if err := Provision(registry, other, []Directory{{Path: "x", Mode: 0o750}}); err == nil {
		t.Fatal("provisioned unregistered mount")
	}
}

func TestProvisionDefinition(t *testing.T) {
	base, registry := provisionBase(t)
	dirs := []Directory{{Path: "apps/demo", Mode: 0o750, Uid: UnsetID, Gid: UnsetID}}
	def, err := ProvisionDefinition(registry, base, dirs)
	if err != nil {
		t.Fatalf("ProvisionDefinition: %v", err)
	}
	if def.Type != "storage.provision" || len(def.Steps) != 1 || def.Steps[0].Name != "provision:apps/demo" {
		t.Fatalf("def = %+v", def)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	runner := operation.NewRunner(s)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	if _, err := os.Stat(filepath.Join(base, "apps/demo")); err != nil {
		t.Fatalf("missing: %v", err)
	}
	if _, err := ProvisionDefinition(registry, base, nil); err == nil {
		t.Fatal("planned empty set")
	}
	if _, err := ProvisionDefinition(registry, "/mnt/ghost", dirs); err == nil {
		t.Fatal("planned unregistered mount")
	}
}

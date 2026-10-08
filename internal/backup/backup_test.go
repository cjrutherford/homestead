package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

func fixedTime() time.Time {
	return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
}

func backupFixtures(t *testing.T) (mount, dest string, discovered discover.Storage) {
	t.Helper()
	mount = t.TempDir()
	dest = t.TempDir()
	mk := func(root, name, content string, mode os.FileMode) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mk(mount, "jellyfin/config/settings.json", `{"k":"v"}`, 0o640)
	mk(mount, "jellyfin/config/db.sqlite", "db", 0o600)
	discovered = discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: dest, FSType: "ext4", Accessible: true},
	}}
	return mount, dest, discovered
}

func backupRegistry(t *testing.T, dest string, discovered discover.Storage) (*storage.Assignments, *storage.Registry) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	registry := storage.NewRegistry(s)
	if _, err := registry.Register(dest, "backup", "owner", discovered); err != nil {
		t.Fatalf("Register: %v", err)
	}
	assignments := storage.NewAssignments(s)
	if _, err := assignments.Assign("backup", dest, "owner", registry, discovered, 0); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	return assignments, registry
}

func TestResolveDestination(t *testing.T) {
	_, dest, discovered := backupFixtures(t)
	assignments, _ := backupRegistry(t, dest, discovered)
	got, err := ResolveDestination(assignments, discovered)
	if err != nil {
		t.Fatalf("ResolveDestination: %v", err)
	}
	if got != dest {
		t.Fatalf("destination = %q", got)
	}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ResolveDestination(storage.NewAssignments(s), discovered); err == nil {
		t.Fatal("resolved without assignment")
	}
}

func TestBackupFlow(t *testing.T) {
	mount, dest, discovered := backupFixtures(t)
	assignments, _ := backupRegistry(t, dest, discovered)
	root, err := ResolveDestination(assignments, discovered)
	if err != nil {
		t.Fatalf("ResolveDestination: %v", err)
	}
	request := Request{
		Package:     "jellyfin",
		Destination: root,
		Sources:     []Source{{Mountpoint: mount, Path: "jellyfin/config"}},
	}
	def, err := Definition(request, fixedTime)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if def.Type != "backup.run" || len(def.Steps) != 2 {
		t.Fatalf("def = %+v", def)
	}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
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
	run := filepath.Join(dest, "jellyfin", "20261008T120000Z", "jellyfin", "config")
	for _, name := range []string{"settings.json", "db.sqlite"} {
		if _, err := os.Stat(filepath.Join(run, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(run, "db.sqlite"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(filepath.Join(dest, "jellyfin", "20261008T120000Z", "backup.json"))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if manifest.Package != "jellyfin" || manifest.Files != 2 || len(manifest.Entries) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	// Missing sources fail loudly instead of backing up nothing.
	missing := request
	missing.Sources = []Source{{Mountpoint: mount, Path: "ghost"}}
	def, err = Definition(missing, fixedTime)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err = runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}

func TestBackupRefusals(t *testing.T) {
	mount, _, _ := backupFixtures(t)
	link := filepath.Join(mount, "jellyfin", "link")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	good := Request{Package: "jellyfin", Destination: "/mnt/backup",
		Sources: []Source{{Mountpoint: mount, Path: "jellyfin/config"}}}
	for _, tc := range []struct {
		name    string
		mutate  func(*Request)
		want    string
		runtime bool
	}{
		{"no package", func(r *Request) { r.Package = "" }, "package must not be empty", false},
		{"no destination", func(r *Request) { r.Destination = "" }, "destination must be absolute", false},
		{"no sources", func(r *Request) { r.Sources = nil }, "at least one source", false},
		{"bad mount", func(r *Request) { r.Sources[0].Mountpoint = "relative" }, "must be absolute", false},
		{"escape", func(r *Request) { r.Sources[0].Path = "../x" }, "inside its mount", false},
		{"symlink", func(r *Request) { r.Sources[0].Path = "jellyfin/link" }, "refused", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := good
			req.Sources = append([]Source(nil), good.Sources...)
			tc.mutate(&req)
			def, err := Definition(req, fixedTime)
			if err != nil {
				if !tc.runtime && strings.Contains(err.Error(), tc.want) {
					return
				}
				t.Fatalf("definition err = %v", err)
			}
			if !tc.runtime {
				t.Fatal("defined invalid request")
			}
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "run.db"))
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
			if done.Status != operation.StatusFailed {
				t.Fatal("symlink tree backed up")
			}
		})
	}
}

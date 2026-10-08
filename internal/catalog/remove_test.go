package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
)

func removeStack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: jellyfin\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func removeData(t *testing.T) (string, string) {
	t.Helper()
	mount := t.TempDir()
	data := filepath.Join(mount, "jellyfin", "config")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(data, "db.sqlite"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return mount, "jellyfin/config"
}

func TestRemovePreservesData(t *testing.T) {
	dir := removeStack(t)
	mount, subpath := removeData(t)
	downs := 0
	old := composeDown
	composeDown = func(_ context.Context, _, _ string) error {
		downs++
		return nil
	}
	t.Cleanup(func() { composeDown = old })
	def, err := RemoveDefinition(Removal{Package: "jellyfin",
		DataDirs: []DataDir{{Mountpoint: mount, Path: subpath}}}, dir)
	if err != nil {
		// Data roots without removal flag refuse: intent ambiguous.
		if !strings.Contains(err.Error(), "explicit removal") {
			t.Fatalf("err = %v", err)
		}
		def, err = RemoveDefinition(Removal{Package: "jellyfin"}, dir)
		if err != nil {
			t.Fatalf("RemoveDefinition: %v", err)
		}
	}
	if def.Type != "catalog.remove" || len(def.Steps) != 2 ||
		def.Steps[0].Name != "down" || def.Steps[1].Name != "remove-files" {
		t.Fatalf("def = %+v", def)
	}
	runner := openInstallRunner(t)
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
	if downs != 1 {
		t.Fatal("down not executed")
	}
	// Stack files go; data survives by default.
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("compose survived")
	}
	if _, err := os.Stat(filepath.Join(mount, subpath, "db.sqlite")); err != nil {
		t.Fatalf("data lost: %v", err)
	}
}

func TestRemoveDataExplicit(t *testing.T) {
	dir := removeStack(t)
	mount, subpath := removeData(t)
	old := composeDown
	composeDown = func(_ context.Context, _, _ string) error { return nil }
	t.Cleanup(func() { composeDown = old })
	def, err := RemoveDefinition(Removal{Package: "jellyfin", RemoveData: true,
		DataDirs: []DataDir{{Mountpoint: mount, Path: subpath}}}, dir)
	if err != nil {
		t.Fatalf("RemoveDefinition: %v", err)
	}
	if len(def.Steps) != 3 || def.Steps[2].Name != "remove-data" {
		t.Fatalf("def = %+v", def)
	}
	runner := openInstallRunner(t)
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
	if _, err := os.Stat(filepath.Join(mount, subpath)); !os.IsNotExist(err) {
		t.Fatal("data survived explicit removal")
	}
}

func TestRemoveValidation(t *testing.T) {
	dir := removeStack(t)
	if _, err := RemoveDefinition(Removal{}, dir); err == nil {
		t.Fatal("removed without package")
	}
	if _, err := RemoveDefinition(Removal{Package: "x"}, ""); err == nil {
		t.Fatal("removed without dir")
	}
	if _, err := RemoveDefinition(Removal{Package: "x", RemoveData: true}, dir); err == nil {
		t.Fatal("removed data without roots")
	}
	if _, err := RemoveDefinition(Removal{Package: "x",
		DataDirs: []DataDir{{Mountpoint: "/m", Path: ".."}}}, dir); err == nil {
		t.Fatal("removed escaping path")
	}
	if _, err := RemoveDefinition(Removal{Package: "x",
		DataDirs: []DataDir{{Mountpoint: "/m", Path: "."}}}, dir); err == nil {
		t.Fatal("removed mount root")
	}
	if _, err := RemoveDefinition(Removal{Package: "ghost"}, t.TempDir()); err == nil {
		t.Fatal("removed never-installed app")
	}
	// Missing data roots fail loudly instead of silently passing.
	mount, _ := removeData(t)
	def, err := RemoveDefinition(Removal{Package: "jellyfin", RemoveData: true,
		DataDirs: []DataDir{{Mountpoint: mount, Path: "ghost"}}}, dir)
	if err != nil {
		t.Fatalf("RemoveDefinition: %v", err)
	}
	runner := openInstallRunner(t)
	old := composeDown
	composeDown = func(_ context.Context, _, _ string) error { return nil }
	t.Cleanup(func() { composeDown = old })
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}

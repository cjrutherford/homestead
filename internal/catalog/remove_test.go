package catalog

import (
	"context"
	"errors"
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
		DataDirs: []DataDir{{Mountpoint: mount, Path: subpath}}}, dir, openDriftStore(t))
	if err != nil {
		// Data roots without removal flag refuse: intent ambiguous.
		if !strings.Contains(err.Error(), "explicit removal") {
			t.Fatalf("err = %v", err)
		}
		def, err = RemoveDefinition(Removal{Package: "jellyfin"}, dir, openDriftStore(t))
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
		DataDirs: []DataDir{{Mountpoint: mount, Path: subpath}}}, dir, openDriftStore(t))
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
	if _, err := RemoveDefinition(Removal{}, dir, openDriftStore(t)); err == nil {
		t.Fatal("removed without package")
	}
	if _, err := RemoveDefinition(Removal{Package: "x"}, "", openDriftStore(t)); err == nil {
		t.Fatal("removed without dir")
	}
	if _, err := RemoveDefinition(Removal{Package: "x", RemoveData: true}, dir, openDriftStore(t)); err == nil {
		t.Fatal("removed data without roots")
	}
	if _, err := RemoveDefinition(Removal{Package: "x",
		DataDirs: []DataDir{{Mountpoint: "/m", Path: ".."}}}, dir, openDriftStore(t)); err == nil {
		t.Fatal("removed escaping path")
	}
	if _, err := RemoveDefinition(Removal{Package: "x",
		DataDirs: []DataDir{{Mountpoint: "/m", Path: "."}}}, dir, openDriftStore(t)); err == nil {
		t.Fatal("removed mount root")
	}
	if _, err := RemoveDefinition(Removal{Package: "ghost"}, t.TempDir(), openDriftStore(t)); err == nil {
		t.Fatal("removed never-installed app")
	}
	// Missing data roots fail loudly instead of silently passing.
	mount, _ := removeData(t)
	def, err := RemoveDefinition(Removal{Package: "jellyfin", RemoveData: true,
		DataDirs: []DataDir{{Mountpoint: mount, Path: "ghost"}}}, dir, openDriftStore(t))
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

func TestRemoveStackFilesRemovesOnlyAnEmptyDirectory(t *testing.T) {
	st := openDriftStore(t)
	empty := removeStack(t)
	if err := removeStackFiles(st, "jellyfin", empty); err != nil {
		t.Fatalf("removeStackFiles: %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty stack directory survived: %v", err)
	}
	// A directory still holding data keeps itself and the data.
	kept := removeStack(t)
	if err := os.MkdirAll(filepath.Join(kept, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "data", "db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeStackFiles(st, "jellyfin", kept); err != nil {
		t.Fatalf("removeStackFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kept, "data", "db")); err != nil {
		t.Fatalf("data lost: %v", err)
	}
}

func TestSecondRemoveIsNotInstalled(t *testing.T) {
	dir := removeStack(t)
	old := composeDown
	composeDown = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { composeDown = old })
	st := openDriftStore(t)
	def, err := RemoveDefinition(Removal{Package: "jellyfin"}, dir, st)
	if err != nil {
		t.Fatal(err)
	}
	runner := openInstallRunner(t)
	op, _ := runner.Start(context.Background(), def, "")
	if _, err := runner.Wait(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveDefinition(Removal{Package: "jellyfin"}, dir, st); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("second removal err = %v, want ErrNotInstalled", err)
	}
	// A pre-fix leftover empty directory is cleaned up and still reads not installed.
	left := t.TempDir()
	if _, err := RemoveDefinition(Removal{Package: "jellyfin"}, left, st); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatal("leftover empty directory not cleaned")
	}
}

func TestStackInstalledHasNoSideEffects(t *testing.T) {
	empty := t.TempDir()
	if StackInstalled(empty) {
		t.Fatal("empty directory reads installed")
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatalf("a check removed the directory: %v", err)
	}
	if err := RemoveEmptyStackDir(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatal("RemoveEmptyStackDir left an empty leftover")
	}
	full := removeStack(t)
	if err := RemoveEmptyStackDir(full); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(full, "compose.yaml")); err != nil {
		t.Fatalf("installed stack touched: %v", err)
	}
}

func TestEmptyDirRemovalNeverLosesAConcurrentInstall(t *testing.T) {
	files, err := Generate(composeManifest(), composeGeneration())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		dir := filepath.Join(t.TempDir(), "jellyfin")
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
					_ = RemoveEmptyStackDir(dir)
					_ = StackInstalled(dir)
				}
			}
		}()
		err := WriteStack(dir, files, nil)
		close(stop)
		<-done
		if err != nil {
			t.Fatalf("round %d: install lost its directory: %v", i, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
}

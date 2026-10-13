package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/discover"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

func installRegistry(t *testing.T, mounts ...string) *storage.Registry {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	registry := storage.NewRegistry(s)
	var discovered discover.Storage
	for _, point := range mounts {
		discovered.Mounts = append(discovered.Mounts, discover.Mount{
			Mountpoint: point, FSType: "ext4", Accessible: true,
		})
	}
	for _, point := range mounts {
		if _, err := registry.Register(point, "testmount", "owner", discovered); err != nil {
			t.Fatalf("Register %s: %v", point, err)
		}
	}
	return registry
}

func installPlan(t *testing.T, base string) InstallPlan {
	t.Helper()
	req := installRequest()
	req.Storage = map[storage.Class]string{storage.ClassApplication: base, storage.ClassBulk: base}
	req.Volumes = []VolumeSpec{
		{Service: "jellyfin", Class: storage.ClassApplication, Subpath: "config", Target: "/config"},
		{Service: "jellyfin", Class: storage.ClassBulk, Subpath: "media", Target: "/media", ReadOnly: true},
	}
	plan, err := PlanInstall(req)
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	return plan
}

func stubComposeUp(t *testing.T, fail bool) *[]string {
	t.Helper()
	var calls []string
	old := composeUp
	composeUp = func(_ context.Context, dir, project string) error {
		calls = append(calls, dir+" "+project)
		if fail {
			return errors.New("exit status 1: compose failed")
		}
		return nil
	}
	t.Cleanup(func() { composeUp = old })
	return &calls
}

func openDriftStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "drift.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openInstallRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestInstallFlow(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	stackDir := t.TempDir()
	calls := stubComposeUp(t, false)
	def, err := InstallDefinition(plan, registry, stackDir, openDriftStore(t), nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
	}
	if def.Type != "catalog.install" || len(def.Steps) != 4 ||
		def.Steps[0].Name != "provision:jellyfin/config" ||
		def.Steps[1].Name != "provision:jellyfin/media" ||
		def.Steps[2].Name != "write-stack" || def.Steps[3].Name != "up" {
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
	if _, err := os.Stat(filepath.Join(base, "jellyfin", "config")); err != nil {
		t.Fatalf("directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stackDir, "compose.yaml")); err != nil {
		t.Fatalf("compose missing: %v", err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "jellyfin") {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestInstallValidates(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	empty := InstallPlan{}
	if _, err := InstallDefinition(empty, registry, "", openDriftStore(t), nil); err == nil {
		t.Fatal("installed empty plan")
	}
	// Production directory resolves from the plan when unset.
	stubComposeUp(t, false)
	def, err := InstallDefinition(plan, registry, "", openDriftStore(t), nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
	}
	if len(def.Steps) != 4 {
		t.Fatalf("def = %+v", def)
	}
	if _, err := InstallDefinition(plan, nil, "", openDriftStore(t), nil); err == nil {
		t.Fatal("installed without registry")
	}
}

func TestInstallUpFailureAborts(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	stubComposeUp(t, true)
	def, err := InstallDefinition(plan, registry, t.TempDir(), openDriftStore(t), nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
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
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	// Earlier steps still applied before the failed up.
	if _, err := os.Stat(filepath.Join(base, "jellyfin", "config")); err != nil {
		t.Fatalf("directory missing after abort: %v", err)
	}
}

func TestComposeUpSerializesPerProject(t *testing.T) {
	// Regression: two concurrent ups on one stack raced container
	// naming live. The seam serializes per project while letting
	// different projects proceed together.
	var current, peak int
	var mu sync.Mutex
	old := composeRun
	composeRun = func(ctx context.Context, dir, project string, args ...string) error {
		mu.Lock()
		current++
		if current > peak {
			peak = current
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		current--
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { composeRun = old })
	ctx := context.Background()
	done := make(chan error, 4)
	go func() { done <- composeUp(ctx, t.TempDir(), "same") }()
	go func() { done <- composeUp(ctx, t.TempDir(), "same") }()
	go func() { done <- composeUp(ctx, t.TempDir(), "other") }()
	go func() { done <- composeDown(ctx, t.TempDir(), "same") }()
	for range 4 {
		if err := <-done; err != nil {
			t.Fatalf("compose: %v", err)
		}
	}
	// Same-project calls never overlapped; "other" may have joined
	// either, so peak 2 proves parallelism survived serialization.
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak)
	}
}

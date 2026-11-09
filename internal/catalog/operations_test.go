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

// No catalog test may reach a real Docker daemon: the edge network call
// defaults to a no-op and tests that care install a spy.
func init() {
	ensureEdgeNetwork = func(context.Context) error { return nil }
}

// stubEdgeNetwork records ensureEdgeNetwork calls and the order they ran in.
func stubEdgeNetwork(t *testing.T, order *[]string, fail error) *int {
	t.Helper()
	calls := 0
	old := ensureEdgeNetwork
	ensureEdgeNetwork = func(context.Context) error {
		calls++
		if order != nil {
			*order = append(*order, "network")
		}
		return fail
	}
	t.Cleanup(func() { ensureEdgeNetwork = old })
	return &calls
}

func stubComposeDown(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := composeDown
	composeDown = func(_ context.Context, dir, project string) error {
		calls = append(calls, project)
		return nil
	}
	t.Cleanup(func() { composeDown = old })
	return &calls
}

func runInstall(t *testing.T, def operation.Definition) operation.Operation {
	t.Helper()
	runner := openInstallRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return done
}

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
	if def.Type != "catalog.install" || len(def.Steps) != 5 ||
		def.Steps[0].Name != "ensure-homestead-edge" ||
		def.Steps[1].Name != "provision:jellyfin/config" ||
		def.Steps[2].Name != "provision:jellyfin/media" ||
		def.Steps[3].Name != "write-stack" || def.Steps[4].Name != "up" {
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
	if len(def.Steps) != 5 {
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

func TestInstallCreatesEdgeNetworkBeforeUp(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	if !plan.EdgeNetwork {
		t.Fatal("routed plan does not flag the edge network")
	}
	joined := strings.Join(plan.Notes, "\n")
	if !strings.Contains(joined, "creates the homestead-edge network") || !strings.Contains(joined, "does not deploy the edge proxy") {
		t.Fatalf("plan does not say the network is created and the proxy is not deployed: %v", plan.Notes)
	}
	var order []string
	calls := stubEdgeNetwork(t, &order, nil)
	old := composeUp
	composeUp = func(context.Context, string, string) error { order = append(order, "up"); return nil }
	t.Cleanup(func() { composeUp = old })
	def, err := InstallDefinition(plan, registry, t.TempDir(), openDriftStore(t), nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
	}
	if done := runInstall(t, def); done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	if *calls != 1 || strings.Join(order, ",") != "network,up" {
		t.Fatalf("calls=%d order=%v, want network before up", *calls, order)
	}
}

func TestInstallEdgeNetworkFailureLeavesNothing(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	stackDir := t.TempDir()
	st := openDriftStore(t)
	stubEdgeNetwork(t, nil, errors.New("docker unavailable"))
	calls := stubComposeUp(t, false)
	def, err := InstallDefinition(plan, registry, stackDir, st, nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
	}
	if done := runInstall(t, def); done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	if len(*calls) != 0 {
		t.Fatalf("up ran after the network failed: %v", *calls)
	}
	if ok, _ := Installed(st, plan.Package, stackDir); ok {
		t.Fatal("package recorded as installed")
	}
}

func TestFailedInstallIsNotRecordedAndCanRetry(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	stackDir := t.TempDir()
	st := openDriftStore(t)
	downs := stubComposeDown(t)
	stubComposeUp(t, true)
	def, err := InstallDefinition(plan, registry, stackDir, st, nil)
	if err != nil {
		t.Fatalf("InstallDefinition: %v", err)
	}
	done := runInstall(t, def)
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	if ok, err := Installed(st, plan.Package, stackDir); err != nil || ok {
		t.Fatalf("Installed = %v, %v after a failed install", ok, err)
	}
	if err := RefuseInstalled(st, plan.Package, stackDir, nil); err != nil {
		t.Fatalf("retry refused: %v", err)
	}
	if len(*downs) != 1 || (*downs)[0] != plan.Package {
		t.Fatalf("containers the failed up created were not stopped: %v", *downs)
	}
	// Data directories the install provisioned are never deleted.
	if _, err := os.Stat(filepath.Join(base, "jellyfin", "config")); err != nil {
		t.Fatalf("rollback removed a data directory: %v", err)
	}
	if !strings.Contains(done.Error, "rolled back") {
		t.Fatalf("error does not say what happened: %q", done.Error)
	}
	// The retry succeeds and records the package.
	stubComposeUp(t, false)
	def, err = InstallDefinition(plan, registry, stackDir, st, nil)
	if err != nil {
		t.Fatalf("retry InstallDefinition: %v", err)
	}
	if done := runInstall(t, def); done.Status != operation.StatusSucceeded {
		t.Fatalf("retry status = %q", done.Status)
	}
	if ok, _ := Installed(st, plan.Package, stackDir); !ok {
		t.Fatal("retry did not record the install")
	}
}

func TestRollbackKeepsSecretsAndReportsCleanupFailure(t *testing.T) {
	st := openDriftStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordFingerprints(st, "x", dir); err != nil {
		t.Fatal(err)
	}
	old := composeDown
	composeDown = func(context.Context, string, string) error { return errors.New("daemon gone") }
	t.Cleanup(func() { composeDown = old })
	err := rollbackInstall(context.Background(), st, "x", dir, true, errors.New("up failed"))
	if err == nil || !strings.Contains(err.Error(), "up failed") || !strings.Contains(err.Error(), "apps uninstall x") {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := Installed(st, "x", dir); ok {
		t.Fatal("files and records should still clear when down fails")
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

package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func updateStack(t *testing.T, images map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	body.WriteString("name: demo\nservices:\n")
	for service, image := range images {
		body.WriteString("  " + service + ":\n    image: " + image + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func stubUpdate(t *testing.T, ps string, pullFails bool) *[]string {
	t.Helper()
	var calls []string
	old := execCompose
	execCompose = func(_ context.Context, dir, project string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		switch {
		case strings.HasPrefix(joined, "pull"):
			if pullFails {
				return "", errors.New("exit status 1: pull failed")
			}
			return "pulled", nil
		case strings.HasPrefix(joined, "up"):
			return "up", nil
		case strings.HasPrefix(joined, "ps "):
			return ps, nil
		}
		return "", errors.New("unexpected " + joined)
	}
	t.Cleanup(func() { execCompose = old })
	return &calls
}

func updateRequest(dir string) UpdateRequest {
	return UpdateRequest{
		Package:  "demo",
		StackDir: dir,
		Moves: []ServiceUpdate{
			{Service: "web", Current: "example/web:1.0.0", Target: "example/web:2.0.0", Source: SourceRegistry},
		},
		Backup: func(context.Context) error { return nil },
	}
}

func openUpdateRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

// driftStore opens a store and records the stack's fingerprints, as an
// install does, so a test can ask whether an update leaves it clean.
func driftStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "drift.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := catalog.RecordFingerprints(s, "demo", dir); err != nil {
		t.Fatalf("RecordFingerprints: %v", err)
	}
	return s
}

func runUpdate(t *testing.T, req UpdateRequest) operation.Operation {
	t.Helper()
	def, err := UpdateDefinition(req)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	runner := openUpdateRunner(t)
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

func assertDriftClean(t *testing.T, s *store.Store, dir string) {
	t.Helper()
	res, err := catalog.Scan(s, func(string) string { return dir }, time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !res.Clean() {
		t.Fatalf("drift after update: %+v", res)
	}
}

func TestUpdateLeavesDriftClean(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	s := driftStore(t, dir)
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"running\"}\n", false)
	req := updateRequest(dir)
	req.Store = s
	if done := runUpdate(t, req); done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	assertDriftClean(t, s, dir)
}

func TestRollbackLeavesDriftClean(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	s := driftStore(t, dir)
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"exited\"}\n", false)
	req := updateRequest(dir)
	req.Store = s
	if done := runUpdate(t, req); done.Status != operation.StatusFailed || !strings.Contains(done.Error, "rolled back") {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	assertDriftClean(t, s, dir)
}

func TestUpdateFlow(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"running\"}\n", false)
	def, err := UpdateDefinition(updateRequest(dir))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	if def.Type != "app.update" || len(def.Steps) != 4 ||
		def.Steps[0].Name != "backup" || def.Steps[3].Name != "up" {
		t.Fatalf("def = %+v", def)
	}
	runner := openUpdateRunner(t)
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
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "image: example/web:2.0.0") {
		t.Fatalf("pins not rewritten:\n%s", raw)
	}
	// Without backup hooks the definition shortens honestly.
	bare := updateRequest(dir)
	bare.Backup = nil
	def, err = UpdateDefinition(bare)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	if len(def.Steps) != 3 || def.Steps[0].Name != "pull" {
		t.Fatalf("def = %+v", def)
	}
}

func TestUpdateRollback(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	// New image never runs: verify fails, rollback rewrites and ups,
	// and the error reports both outcomes.
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"exited\"}\n", false)
	def, err := UpdateDefinition(updateRequest(dir))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	runner := openUpdateRunner(t)
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
	if !strings.Contains(done.Error, "rolled back") {
		t.Fatalf("error = %q", done.Error)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "image: example/web:1.0.0") {
		t.Fatalf("pins not restored:\n%s", raw)
	}
}

func TestUpdateRefusals(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:2.0.0"})
	if _, err := UpdateDefinition(UpdateRequest{}); err == nil {
		t.Fatal("updated empty request")
	}
	// Compose files must describe the plan: unknown images refuse
	// before pull spends network.
	req := updateRequest(dir)
	req.Moves[0].Current = "example/web:9.9.9"
	def, err := UpdateDefinition(req)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	stubUpdate(t, "", false)
	runner := openUpdateRunner(t)
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
	// Pull failures abort before rewriting anything.
	stubUpdate(t, "", true)
	def, err = UpdateDefinition(updateRequest(updateStack(t, map[string]string{"web": "example/web:1.0.0"})))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
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

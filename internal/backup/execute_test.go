package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
)

func seedTreeRun(t *testing.T, dest, live string) (RestorePlan, string) {
	t.Helper()
	root := filepath.Join(dest, "photos", "20261008T120000Z", "config")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "db.sqlite"), []byte("db-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runs, err := ListRuns(dest, "photos")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	// Rewrite the manifest with directory entries: seedRun writes
	// files, but restore entries are trees.
	manifest := Manifest{
		Package: "photos", Created: "20261008T120000Z",
		Entries: []string{"config"}, Files: 2,
		Sources: map[string]string{"config": "/mnt/data"},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "photos", "20261008T120000Z", "backup.json"), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runs, err = ListRuns(dest, "photos")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	plan, err := PlanRestore(runs[0], map[string]string{"config": filepath.Join(live, "config")})
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	return plan, filepath.Join(live, "config")
}

func TestRestoreFlow(t *testing.T) {
	dest := t.TempDir()
	live := t.TempDir()
	plan, liveConfig := seedTreeRun(t, dest, live)
	_ = liveConfig
	def, err := RestoreDefinition(plan)
	if err != nil {
		t.Fatalf("RestoreDefinition: %v", err)
	}
	if def.Type != "backup.restore" || len(def.Steps) != 2 ||
		def.Steps[0].Name != "restore:"+filepath.Join(dest, "photos", "20261008T120000Z", "config") ||
		def.Steps[1].Name != "verify" {
		t.Fatalf("def = %+v", def)
	}
	runner := openJobRunner(t)
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
	raw, err := os.ReadFile(filepath.Join(live, "config", "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(raw) != `{"theme":"dark"}` {
		t.Fatalf("content = %q", raw)
	}
	// Orphaned live files do not survive mirrored restores.
	if err := os.WriteFile(filepath.Join(live, "config", "orphan.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err = runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	if _, err := os.Stat(filepath.Join(live, "config", "orphan.txt")); !os.IsNotExist(err) {
		t.Fatal("orphan survived restore")
	}
}

func TestRestoreValidation(t *testing.T) {
	if _, err := RestoreDefinition(RestorePlan{}); err == nil {
		t.Fatal("planned empty restore")
	}
	plan := RestorePlan{Entries: []PlannedEntry{{BackupPath: "", Target: "/x"}}}
	if _, err := RestoreDefinition(plan); err == nil {
		t.Fatal("planned empty backup path")
	}
	// Verification compares file sets and sizes directly.
	have := t.TempDir()
	want := t.TempDir()
	if err := os.WriteFile(filepath.Join(have, "a"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(want, "a"), []byte("12"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mismatch := RestorePlan{Entries: []PlannedEntry{{BackupPath: want, Target: have}}}
	if err := verifyRestore(mismatch); err == nil ||
		!strings.Contains(err.Error(), "differs") {
		t.Fatalf("err = %v", err)
	}
	if err := verifyRestore(RestorePlan{Entries: []PlannedEntry{
		{BackupPath: want, Target: filepath.Join(t.TempDir(), "ghost")},
	}}); err == nil {
		t.Fatal("verified missing target")
	}
}

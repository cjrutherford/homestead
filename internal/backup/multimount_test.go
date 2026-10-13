package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/storage"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// A package spread over two mounts (S276: application and bulk) backs
// up both trees into distinct run subpaths, and restore returns each
// tree to its own mount.
func TestJobBacksUpPackageAcrossMounts(t *testing.T) {
	app := t.TempDir()
	bulk := t.TempDir()
	dest := t.TempDir()
	job := scheduleJob(t, app, dest)
	writeFile(t, filepath.Join(app, "photos", "config", "settings.json"), `{"app":true}`)
	writeFile(t, filepath.Join(bulk, "photos", "library", "a.jpg"), "bulk-bytes")
	job.Sources = []Source{
		{Mountpoint: app, Path: "photos", Class: storage.ClassApplication},
		{Mountpoint: bulk, Path: "photos", Class: storage.ClassBulk},
	}
	stubExec(t, func(_ string, _ string, _ []string) (string, error) { return "ok", nil })
	def, err := JobDefinition(job, fixedJobTime)
	if err != nil {
		t.Fatalf("JobDefinition: %v", err)
	}
	if def.Steps[0].Name != "backup:application:photos" || def.Steps[1].Name != "backup:bulk:photos" {
		t.Fatalf("steps = %+v", def.Steps)
	}
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %+v (%v)", done, err)
	}
	run := filepath.Join(dest, "photos", "20261008T120000Z")
	if got := readFile(t, filepath.Join(run, "application", "photos", "config", "settings.json")); got != `{"app":true}` {
		t.Fatalf("app tree = %q", got)
	}
	if got := readFile(t, filepath.Join(run, "bulk", "photos", "library", "a.jpg")); got != "bulk-bytes" {
		t.Fatalf("bulk tree = %q", got)
	}
	runs, err := ListRuns(dest, "photos")
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v %+v", err, runs)
	}
	manifest := runs[0].Manifest
	if manifest.Sources["bulk/photos"] != bulk || manifest.Sources["application/photos"] != app ||
		manifest.Paths["bulk/photos"] != "photos" {
		t.Fatalf("manifest = %+v", manifest)
	}

	// Lose both trees, then restore: each returns to its own mount.
	for _, root := range []string{app, bulk} {
		if err := os.RemoveAll(filepath.Join(root, "photos", "config")); err != nil {
			t.Fatalf("remove: %v", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(bulk, "photos")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	plan, err := PlanRestore(runs[0], map[string]string{"app.db": filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	// File entries (the database export) restore through their own
	// path; this test covers the tree entries.
	var trees []PlannedEntry
	for _, entry := range plan.Entries {
		if !strings.HasSuffix(entry.BackupPath, "app.db") {
			trees = append(trees, entry)
		}
	}
	plan.Entries = trees
	restore, err := RestoreDefinition(plan)
	if err != nil {
		t.Fatalf("RestoreDefinition: %v", err)
	}
	op, err = runner.Start(context.Background(), restore, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err = runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("restore status = %+v (%v)", done, err)
	}
	if got := readFile(t, filepath.Join(app, "photos", "config", "settings.json")); got != `{"app":true}` {
		t.Fatalf("restored app = %q", got)
	}
	if got := readFile(t, filepath.Join(bulk, "photos", "library", "a.jpg")); got != "bulk-bytes" {
		t.Fatalf("restored bulk = %q", got)
	}
	if _, err := os.Stat(filepath.Join(app, "photos", "library")); !os.IsNotExist(err) {
		t.Fatal("bulk tree leaked onto the application mount")
	}
}

func TestRequestBacksUpPackageAcrossMounts(t *testing.T) {
	app, bulk, dest := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(app, "photos", "a"), "1")
	writeFile(t, filepath.Join(bulk, "photos", "b"), "2")
	def, err := Definition(Request{
		Package: "photos", Destination: dest,
		Sources: []Source{
			{Mountpoint: app, Path: "photos", Class: storage.ClassApplication},
			{Mountpoint: bulk, Path: "photos", Class: storage.ClassBulk},
		},
	}, fixedJobTime)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %+v (%v)", done, err)
	}
	run := filepath.Join(dest, "photos", "20261008T120000Z")
	if readFile(t, filepath.Join(run, "application", "photos", "a")) != "1" ||
		readFile(t, filepath.Join(run, "bulk", "photos", "b")) != "2" {
		t.Fatal("trees not in distinct subpaths")
	}
}

func TestSharedDestinationRefused(t *testing.T) {
	dest := t.TempDir()
	two := []Source{
		{Mountpoint: "/mnt/a", Path: "photos"},
		{Mountpoint: "/mnt/b", Path: "photos"},
	}
	if _, err := Definition(Request{Package: "photos", Destination: dest, Sources: two}, fixedJobTime); err == nil ||
		!strings.Contains(err.Error(), "share the destination") {
		t.Fatalf("err = %v", err)
	}
	job := scheduleJob(t, t.TempDir(), dest)
	job.Sources = two
	if _, err := JobDefinition(job, fixedJobTime); err == nil || !strings.Contains(err.Error(), "share the destination") {
		t.Fatalf("job err = %v", err)
	}
	if err := (Source{Mountpoint: "/mnt/a", Path: "p", Class: "a/b"}).Validate(); err == nil {
		t.Fatal("accepted a class with a slash")
	}
}

// Runs written before per-class layouts (no paths map, entry = path)
// still plan and restore to their recorded mount.
func TestRestoreOldLayoutManifest(t *testing.T) {
	dest, live := t.TempDir(), t.TempDir()
	run := filepath.Join(dest, "photos", "20260101T000000Z")
	writeFile(t, filepath.Join(run, "photos", "config", "x.txt"), "old")
	raw, err := json.Marshal(map[string]any{
		"package": "photos", "created_at": "20260101T000000Z",
		"entries": []string{"photos"}, "files": 1,
		"sources": map[string]string{"photos": live},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeFile(t, filepath.Join(run, "backup.json"), string(raw))
	runs, err := ListRuns(dest, "photos")
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v %+v", err, runs)
	}
	plan, err := PlanRestore(runs[0], nil)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Target != filepath.Join(live, "photos") ||
		plan.Entries[0].BackupPath != filepath.Join(run, "photos") {
		t.Fatalf("plan = %+v", plan)
	}
	def, err := RestoreDefinition(plan)
	if err != nil {
		t.Fatalf("RestoreDefinition: %v", err)
	}
	runner := openJobRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %+v (%v)", done, err)
	}
	if got := readFile(t, filepath.Join(live, "photos", "config", "x.txt")); got != "old" {
		t.Fatalf("restored = %q", got)
	}
}

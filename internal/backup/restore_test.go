package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func seedRun(t *testing.T, dest, pkg, stamp string, entries map[string]string) string {
	t.Helper()
	root := filepath.Join(dest, pkg, stamp)
	for entry, content := range entries {
		path := filepath.Join(root, entry)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	manifest := Manifest{Package: pkg, Created: stamp, Sources: map[string]string{}}
	var names []string
	for entry := range entries {
		names = append(names, entry)
		manifest.Sources[entry] = "/mnt/data"
	}
	sort.Strings(names)
	manifest.Entries = names
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "backup.json"), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func TestListRuns(t *testing.T) {
	dest := t.TempDir()
	seedRun(t, dest, "photos", "20261008T120000Z", map[string]string{"config/a": "1"})
	seedRun(t, dest, "photos", "20261007T120000Z", map[string]string{"config/a": "0"})
	// Foreign files, partial runs, and other packages stay out quietly.
	_ = os.WriteFile(filepath.Join(dest, "photos", "keep.txt"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(dest, "photos", "20261006T120000Z"), 0o755)
	_ = os.MkdirAll(filepath.Join(dest, "other", "20261008T120000Z"), 0o755)
	runs, err := ListRuns(dest, "photos")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].Stamp != "20261008T120000Z" || runs[1].Stamp != "20261007T120000Z" {
		t.Fatalf("runs = %+v", runs)
	}
	if len(runs[0].Manifest.Entries) != 1 || runs[0].Manifest.Sources["config/a"] != "/mnt/data" {
		t.Fatalf("manifest = %+v", runs[0].Manifest)
	}
	empty, err := ListRuns(dest, "ghost")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("runs = %+v", empty)
	}
	if _, err := ListRuns(dest, ""); err == nil {
		t.Fatal("listed empty package")
	}
}

func TestPlanRestore(t *testing.T) {
	dest := t.TempDir()
	live := t.TempDir()
	seedRun(t, dest, "photos", "20261008T120000Z", map[string]string{
		"config/settings.json": "{}",
		"app.db":               "db",
	})
	runs, err := ListRuns(dest, "photos")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	// Default targets resolve under provenance mounts: point provenance
	// at the live tree by overriding per entry.
	overrides := map[string]string{
		"config/settings.json": filepath.Join(live, "config", "settings.json"),
		"app.db":               filepath.Join(live, "app.db"),
	}
	plan, err := PlanRestore(runs[0], overrides)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if len(plan.Entries) != 2 || plan.Destructive {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "stop photos") {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
	// Existing targets mark destructive with named warnings.
	if err := os.MkdirAll(filepath.Join(live, "config"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(live, "config", "settings.json"), []byte("live"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan, err = PlanRestore(runs[0], overrides)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if !plan.Destructive || plan.Entries[0].Exists || !plan.Entries[1].Exists {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(plan.Warnings[1], "overwrites") {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
	// Missing provenance without override fails instead of guessing.
	bare := runs[0]
	bare.Manifest.Sources = nil
	if _, err := PlanRestore(bare, nil); err == nil ||
		!strings.Contains(err.Error(), "explicit target") {
		t.Fatalf("err = %v", err)
	}
	// Empty runs and relative overrides refuse.
	empty := runs[0]
	empty.Manifest.Entries = nil
	if _, err := PlanRestore(empty, nil); err == nil {
		t.Fatal("planned empty run")
	}
	rel := map[string]string{"config/settings.json": "relative/path"}
	if _, err := PlanRestore(runs[0], rel); err == nil {
		t.Fatal("planned relative target")
	}
	if _, err := PlanRestore(Run{}, nil); err == nil {
		t.Fatal("planned empty run struct")
	}
}

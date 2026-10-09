package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"homestead/internal/operation"
)

func writeDriftFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestFingerprintsHashPresent(t *testing.T) {
	dir := t.TempDir()
	writeDriftFile(t, dir, "compose.yaml", "services: {}\n")
	writeDriftFile(t, dir, OverrideFileName, "services: {}\n")
	hashes, err := Fingerprints(dir)
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if len(hashes) != 2 || hashes["compose.yaml"] == "" || hashes[OverrideFileName] == "" {
		t.Fatalf("hashes = %v", hashes)
	}
	if hashes["compose.yaml"] != hashes[OverrideFileName] {
		t.Fatalf("identical content hashed differently: %v", hashes)
	}
	// Absent files skip silently; foreign files never enter.
	empty := t.TempDir()
	hashes, err = Fingerprints(empty)
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("hashes = %v", hashes)
	}
	if err := os.WriteFile(filepath.Join(empty, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	hashes, err = Fingerprints(empty)
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("hashes = %v", hashes)
	}
}

func TestRecordStoredClear(t *testing.T) {
	st := openDriftStore(t)
	dir := t.TempDir()
	writeDriftFile(t, dir, "compose.yaml", "services: {}\n")
	if err := RecordFingerprints(st, "jellyfin", dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	stored, err := StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if len(stored) != 1 || stored["compose.yaml"] == "" {
		t.Fatalf("stored = %v", stored)
	}
	// Re-baselining replaces: edited content stores a new hash, and
	// stacks with no files present clear stale rows.
	writeDriftFile(t, dir, "compose.yaml", "services: {edited: true}\n")
	first := stored["compose.yaml"]
	if err := RecordFingerprints(st, "jellyfin", dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	stored, err = StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if stored["compose.yaml"] == first {
		t.Fatal("re-baseline kept the stale hash")
	}
	if err := os.Remove(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := RecordFingerprints(st, "jellyfin", dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	stored, err = StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored = %v", stored)
	}
	// Unknown stacks read empty; clearing them succeeds.
	stored, err = StoredFingerprints(st, "ghost")
	if err != nil || len(stored) != 0 {
		t.Fatalf("stored = %v, err = %v", stored, err)
	}
	if err := ClearFingerprints(st, "ghost"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	writeDriftFile(t, dir, "compose.yaml", "services: {}\n")
	writeDriftFile(t, dir, OverrideFileName, "overrides: {}\n")
	hashes, err := Fingerprints(dir)
	if err != nil {
		t.Fatalf("Fingerprints: %v", err)
	}
	// Clean tree reports nothing.
	drifts, err := Detect(dir, hashes)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(drifts) != 0 {
		t.Fatalf("drifts = %+v", drifts)
	}
	// Edited content reports modified.
	writeDriftFile(t, dir, "compose.yaml", "services: {expert: true}\n")
	drifts, err = Detect(dir, hashes)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(drifts) != 1 || drifts[0] != (Drift{Path: "compose.yaml", Kind: DriftModified}) {
		t.Fatalf("drifts = %+v", drifts)
	}
	// Deleted files report removed, sorted by path.
	if err := os.Remove(filepath.Join(dir, OverrideFileName)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	drifts, err = Detect(dir, hashes)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	byPath := map[string]string{}
	for _, drift := range drifts {
		byPath[drift.Path] = drift.Kind
	}
	if len(drifts) != 2 || byPath["compose.yaml"] != DriftModified || byPath[OverrideFileName] != DriftRemoved {
		t.Fatalf("drifts = %+v", drifts)
	}
	// Output sorts by path for stable reports.
	if drifts[0].Path != OverrideFileName || drifts[1].Path != "compose.yaml" {
		t.Fatalf("drifts = %+v", drifts)
	}
	// Unwatched entries never report.
	drifts, err = Detect(dir, map[string]string{"unwatched": "x"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(drifts) != 0 {
		t.Fatalf("drifts = %+v", drifts)
	}
}

func TestInstallRecordsFingerprints(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	stackDir := t.TempDir()
	st := openDriftStore(t)
	stubComposeUp(t, false)
	def, err := InstallDefinition(plan, registry, stackDir, st)
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
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	stored, err := StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if stored["compose.yaml"] == "" {
		t.Fatalf("stored = %v", stored)
	}
	// An expert edit through Dockge or compose reads as drift.
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {expert: true}\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	drifts, err := Detect(stackDir, stored)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(drifts) != 1 || drifts[0].Kind != DriftModified {
		t.Fatalf("drifts = %+v", drifts)
	}
}

func TestConfigureRecordsFingerprints(t *testing.T) {
	dir := t.TempDir()
	st := openDriftStore(t)
	stubComposeUp(t, false)
	def, err := ConfigureDefinition(configPlan(), dir, "jellyfin", st)
	if err != nil {
		t.Fatalf("ConfigureDefinition: %v", err)
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
		t.Fatalf("status = %q", done.Status)
	}
	stored, err := StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if stored[OverrideFileName] == "" {
		t.Fatalf("stored = %v", stored)
	}
}

func TestRemoveClearsFingerprints(t *testing.T) {
	dir := removeStack(t)
	st := openDriftStore(t)
	writeDriftFile(t, dir, "compose.yaml", "services: {}\n")
	if err := RecordFingerprints(st, "jellyfin", dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	old := composeDown
	composeDown = func(_ context.Context, _, _ string) error { return nil }
	t.Cleanup(func() { composeDown = old })
	def, err := RemoveDefinition(Removal{Package: "jellyfin"}, dir, st)
	if err != nil {
		t.Fatalf("RemoveDefinition: %v", err)
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
		t.Fatalf("status = %q", done.Status)
	}
	stored, err := StoredFingerprints(st, "jellyfin")
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored = %v", stored)
	}
}

func TestDefinitionsNeedStore(t *testing.T) {
	base := t.TempDir()
	registry := installRegistry(t, base)
	plan := installPlan(t, base)
	if _, err := InstallDefinition(plan, registry, t.TempDir(), nil); err == nil {
		t.Fatal("install without store accepted")
	}
	if _, err := ConfigureDefinition(configPlan(), t.TempDir(), "jellyfin", nil); err == nil {
		t.Fatal("configure without store accepted")
	}
	dir := removeStack(t)
	if _, err := RemoveDefinition(Removal{Package: "jellyfin"}, dir, nil); err == nil {
		t.Fatal("remove without store accepted")
	}
}

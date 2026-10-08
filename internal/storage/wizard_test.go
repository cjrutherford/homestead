package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openStorageWizard(t *testing.T) *Wizard {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewWizard(wizard.New(s), NewRegistry(s), NewAssignments(s))
}

func wizardDiscovery() discover.Storage {
	mount := func(point, fstype string, readOnly, accessible bool, free uint64) discover.Mount {
		return discover.Mount{Mountpoint: point, FSType: fstype, ReadOnly: readOnly, Accessible: accessible, FreeBytes: free}
	}
	return discover.Storage{Mounts: []discover.Mount{
		mount("/", "ext4", false, true, 10<<30),
		mount("/mnt/media", "ext4", false, true, 1000<<30),
		mount("/mnt/ro", "ext4", true, true, 100<<30),
	}}
}

func wizardPayload() map[string]any {
	mounts := []any{
		map[string]any{"mountpoint": "/mnt/media", "label": "media"},
	}
	assignments := []any{
		map[string]any{"class": "bulk", "mountpoint": "/mnt/media", "min_free_bytes": 0},
		map[string]any{"class": "application", "mountpoint": "/mnt/media", "min_free_bytes": 0},
	}
	rawMounts, _ := json.Marshal(mounts)
	rawAssign, _ := json.Marshal(assignments)
	var decodedMounts, decodedAssign any
	_ = json.Unmarshal(rawMounts, &decodedMounts)
	_ = json.Unmarshal(rawAssign, &decodedAssign)
	return map[string]any{"mounts": decodedMounts, "assignments": decodedAssign}
}

func TestStorageWizardFlow(t *testing.T) {
	w := openStorageWizard(t)
	payload := wizardPayload()
	for _, step := range StorageSteps {
		if err := w.Save(step, payload); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	if len(StorageSteps) != 4 || StorageSteps[0] != "mounts" {
		t.Fatalf("steps = %v", StorageSteps)
	}
	plan, err := w.Plan(wizardDiscovery())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Ready() || len(plan.Mounts) != 1 || len(plan.Assignments) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	registrations, err := w.ApplyMounts("owner", wizardDiscovery())
	if err != nil {
		t.Fatalf("ApplyMounts: %v", err)
	}
	if len(registrations) != 1 || registrations[0].Label != "media" {
		t.Fatalf("registrations = %+v", registrations)
	}
	// Root was never selected for registration: assignment fails until
	// the mount approves. Register it through a second wizard pass is
	// out of scope here; the refusal path is what matters.
	unapproved := wizardPayload()
	unapproved["assignments"] = []any{map[string]any{"class": "cache", "mountpoint": "/"}}
	if err := w.Save("classes", unapproved); err != nil {
		t.Fatalf("save: %v", err)
	}
	plan, err = w.Plan(wizardDiscovery())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Ready() {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := w.ApplyAssignments("owner", wizardDiscovery()); err == nil ||
		!strings.Contains(err.Error(), "unregistered") {
		t.Fatalf("err = %v", err)
	}
	// Re-saving the approved payload restores the appliable plan.
	if err := w.Save("classes", wizardPayload()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := w.ApplyAssignments("owner", wizardDiscovery()); err != nil {
		t.Fatalf("ApplyAssignments: %v", err)
	}
}

func TestStorageWizardRejects(t *testing.T) {
	w := openStorageWizard(t)
	good := wizardPayload()
	mount := func(point, label string) map[string]any {
		payload := wizardPayload()
		payload["mounts"] = []any{map[string]any{"mountpoint": point, "label": label}}
		return payload
	}
	assign := func(class, point string) map[string]any {
		payload := wizardPayload()
		payload["assignments"] = []any{map[string]any{"class": class, "mountpoint": point}}
		return payload
	}
	cases := []struct {
		name    string
		step    string
		payload map[string]any
		want    string
	}{
		{"unknown step", "nope", good, "unknown step"},
		{"no mounts", "mounts", map[string]any{}, "needs mounts"},
		{"relative", "mounts", mount("mnt/media", "media"), "absolute path"},
		{"bad label", "mounts", mount("/mnt/media", "Bad!"), "label must match"},
		{"no assignments", "classes", map[string]any{
			"mounts": good["mounts"],
		}, "needs assignments"},
		{"bad class", "classes", assign("archive", "/mnt/media"), "unknown class"},
		{"dup class", "classes", map[string]any{
			"mounts": good["mounts"],
			"assignments": []any{
				map[string]any{"class": "bulk", "mountpoint": "/mnt/media"},
				map[string]any{"class": "bulk", "mountpoint": "/"},
			},
		}, "duplicate assignment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := w.Save(tc.step, tc.payload)
			if err == nil {
				t.Fatal("saved invalid payload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	empty := openStorageWizard(t)
	if _, err := empty.Plan(wizardDiscovery()); err == nil {
		t.Fatal("planned without selection")
	}
}

func TestStorageWizardReadiness(t *testing.T) {
	w := openStorageWizard(t)
	payload := wizardPayload()
	payload["mounts"] = []any{map[string]any{"mountpoint": "/mnt/ro", "label": "ro"}}
	if err := w.Save("mounts", payload); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Read-only selection previews unready with reason.
	plan, err := w.Plan(wizardDiscovery())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Ready() || plan.Mounts[0].Ready || plan.Mounts[0].Detail != "is read-only" {
		t.Fatalf("plan = %+v", plan)
	}
	// Applying refuses through registration with the reason attached.
	if _, err := w.ApplyMounts("owner", wizardDiscovery()); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v", err)
	}
}

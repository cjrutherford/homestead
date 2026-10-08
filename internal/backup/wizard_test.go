package backup

import (
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
	"homestead/internal/wizard"
)

func openBackupWizard(t *testing.T) *Wizard {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewWizard(wizard.New(s), NewSchedules(s))
}

func backupPayload() map[string]any {
	return map[string]any{
		"package": "photos",
		"schedule": map[string]any{
			"interval_seconds": 86400,
			"retain":           7,
			"enabled":          true,
		},
	}
}

func TestBackupWizardFlow(t *testing.T) {
	w := openBackupWizard(t)
	payload := backupPayload()
	for _, step := range BackupSteps {
		if err := w.Save(step, payload); err != nil {
			t.Fatalf("save %s: %v", step, err)
		}
	}
	if len(BackupSteps) != 4 || BackupSteps[0] != "package" {
		t.Fatalf("steps = %v", BackupSteps)
	}
	plan, err := w.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Package != "photos" || plan.IntervalSeconds != 86400 || plan.Retain != 7 || !plan.Enabled {
		t.Fatalf("plan = %+v", plan)
	}
	saved, err := w.ApplySchedule("owner")
	if err != nil {
		t.Fatalf("ApplySchedule: %v", err)
	}
	if saved.Actor != "owner" || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}
	fetched, err := w.schedules.Get("photos")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched != saved {
		t.Fatalf("fetched = %+v, want %+v", fetched, saved)
	}
}

func TestBackupWizardRejects(t *testing.T) {
	w := openBackupWizard(t)
	good := backupPayload()
	cases := []struct {
		name    string
		step    string
		payload map[string]any
		want    string
	}{
		{"unknown step", "nope", good, "unknown step"},
		{"no package", "package", map[string]any{}, "needs a package"},
		{"no schedule", "schedule", map[string]any{"package": "photos"}, "needs a schedule"},
		{"sub-hourly", "schedule", map[string]any{
			"package":  "photos",
			"schedule": map[string]any{"interval_seconds": 60, "retain": 1},
		}, "at least one hour"},
		{"negative retain", "schedule", map[string]any{
			"package":  "photos",
			"schedule": map[string]any{"interval_seconds": 3600, "retain": -1},
		}, "must not be negative"},
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
	if _, err := w.ApplySchedule(""); err == nil {
		t.Fatal("applied without actor")
	}
	empty := openBackupWizard(t)
	if _, err := empty.Plan(); err == nil {
		t.Fatal("planned without selection")
	}
}

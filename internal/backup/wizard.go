package backup

import (
	"encoding/json"
	"fmt"

	"homestead/internal/wizard"
)

// BackupWizardID identifies the backup setup flow in the generic
// wizard store (S036). Payloads carry package, interval, and
// retention only — destinations resolve from assignments at plan and
// apply time, so saved state never pins a mount that later vanishes.
const BackupWizardID = "backup"

// BackupSteps orders the flow: choose package, set cadence, review
// readiness, done. Wall-clock triggering belongs to timers; the
// wizard persists intent, and runs execute through operations.
var BackupSteps = []string{"package", "schedule", "review", "done"}

// backupStepIndex orders steps; -1 means unknown.
func backupStepIndex(step string) int {
	for i, name := range BackupSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// Wizard guides backup setup over the wizard store and schedules. It
// owns no HTTP surface: the generic wizard endpoints and later slices
// drive it.
type Wizard struct {
	wizards   *wizard.Service
	schedules *Schedules
}

// NewWizard returns a Wizard.
func NewWizard(wizards *wizard.Service, schedules *Schedules) *Wizard {
	return &Wizard{wizards: wizards, schedules: schedules}
}

// decodePackage renders the package payload.
func decodePackage(payload map[string]any) (string, error) {
	packageName, _ := payload["package"].(string)
	if packageName == "" {
		return "", fmt.Errorf("backup: package step needs a package")
	}
	return packageName, nil
}

// decodeSchedule renders the cadence payload into a validated
// schedule: hourly floors, non-negative retention, actor from the
// apply call rather than stored state.
func decodeSchedule(payload map[string]any) (Schedule, error) {
	packageName, err := decodePackage(payload)
	if err != nil {
		return Schedule{}, err
	}
	raw, ok := payload["schedule"]
	if !ok || raw == nil {
		return Schedule{}, fmt.Errorf("backup: schedule step needs a schedule")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Schedule{}, fmt.Errorf("backup: schedule encode: %w", err)
	}
	var fields struct {
		IntervalSeconds int64 `json:"interval_seconds"`
		Retain          int   `json:"retain"`
		Enabled         bool  `json:"enabled"`
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return Schedule{}, fmt.Errorf("backup: schedule decode: %w", err)
	}
	schedule := Schedule{Package: packageName, IntervalSeconds: fields.IntervalSeconds, Retain: fields.Retain, Enabled: fields.Enabled}
	if err := schedule.validateShape(); err != nil {
		return Schedule{}, err
	}
	return schedule, nil
}

// Save validates the payload for step and persists it.
func (w *Wizard) Save(step string, payload map[string]any) error {
	idx := backupStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("backup: unknown step %q", step)
	}
	if _, err := decodePackage(payload); err != nil {
		return err
	}
	if idx >= backupStepIndex("schedule") {
		if _, err := decodeSchedule(payload); err != nil {
			return err
		}
	}
	_, err := w.wizards.Put(BackupWizardID, step, payload)
	return err
}

// selection loads the saved payload.
func (w *Wizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(BackupWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// Plan returns the reviewable outcome: the validated schedule as it
// would persist, without writing anything.
func (w *Wizard) Plan() (Schedule, error) {
	payload, err := w.selection()
	if err != nil {
		return Schedule{}, err
	}
	return decodeSchedule(payload)
}

// ApplySchedule persists the saved schedule under the acting owner.
func (w *Wizard) ApplySchedule(actor string) (Schedule, error) {
	if actor == "" {
		return Schedule{}, fmt.Errorf("backup: actor must not be empty")
	}
	schedule, err := w.Plan()
	if err != nil {
		return Schedule{}, err
	}
	schedule.Actor = actor
	return w.schedules.Save(schedule)
}

package storage

import (
	"encoding/json"
	"fmt"

	"homestead/internal/discover"
	"homestead/internal/wizard"
)

// StorageWizardID identifies the storage setup flow in the generic
// wizard store (S036). Payloads carry selections only — mount facts
// stay in discovery, approvals in the registry, bindings in
// assignments — so saved state never goes stale beside live truth.
const StorageWizardID = "storage"

// StorageSteps orders the flow: choose mounts, assign classes, review
// readiness, done. Registration and assignment apply at the caller's
// layer through ApplyMounts and ApplyAssignments; the wizard guides
// and previews, never writes.
var StorageSteps = []string{"mounts", "classes", "review", "done"}

// storageStepIndex orders steps; -1 means unknown.
func storageStepIndex(step string) int {
	for i, name := range StorageSteps {
		if name == step {
			return i
		}
	}
	return -1
}

// MountSelection is one wizard-chosen location: where plus the
// owner's name for it. Actor rides the apply call, not the payload.
type MountSelection struct {
	Mountpoint string
	Label      string
}

// ClassSelection binds one class to one chosen mount with its minimum
// free-space requirement in bytes.
type ClassSelection struct {
	Class        Class
	Mountpoint   string
	MinFreeBytes uint64
}

// Wizard guides storage setup over the wizard store, registry, and
// assignments. It owns no HTTP surface: the generic wizard endpoints
// and later slices drive it.
type Wizard struct {
	wizards     *wizard.Service
	registry    *Registry
	assignments *Assignments
}

// NewWizard returns a Wizard.
func NewWizard(wizards *wizard.Service, registry *Registry, assignments *Assignments) *Wizard {
	return &Wizard{wizards: wizards, registry: registry, assignments: assignments}
}

// decodeMounts renders the mounts payload into shape-checked selections.
func decodeMounts(payload map[string]any) ([]MountSelection, error) {
	raw, ok := payload["mounts"]
	if !ok {
		return nil, fmt.Errorf("storage: mounts step needs mounts")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("storage: mounts encode: %w", err)
	}
	var selections []MountSelection
	if err := json.Unmarshal(encoded, &selections); err != nil {
		return nil, fmt.Errorf("storage: mounts decode: %w", err)
	}
	if len(selections) == 0 {
		return nil, fmt.Errorf("storage: at least one mount is required")
	}
	seenPoints := map[string]bool{}
	seenLabels := map[string]bool{}
	for i := range selections {
		if len(selections[i].Mountpoint) == 0 || selections[i].Mountpoint[0] != '/' {
			return nil, fmt.Errorf("storage: mount #%d must be an absolute path", i)
		}
		if !validLabel.MatchString(selections[i].Label) || len(selections[i].Label) > 32 {
			return nil, fmt.Errorf("storage: mount #%d label must match [a-z][a-z0-9-]* (max 32)", i)
		}
		if seenPoints[selections[i].Mountpoint] {
			return nil, fmt.Errorf("storage: duplicate mount %q", selections[i].Mountpoint)
		}
		if seenLabels[selections[i].Label] {
			return nil, fmt.Errorf("storage: duplicate label %q", selections[i].Label)
		}
		seenPoints[selections[i].Mountpoint] = true
		seenLabels[selections[i].Label] = true
	}
	return selections, nil
}

// decodeClasses renders the classes payload into validated selections:
// known classes on absolute mountpoints.
func decodeClasses(payload map[string]any) ([]ClassSelection, error) {
	raw, ok := payload["assignments"]
	if !ok {
		return nil, fmt.Errorf("storage: classes step needs assignments")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("storage: assignments encode: %w", err)
	}
	var selections []ClassSelection
	if err := json.Unmarshal(encoded, &selections); err != nil {
		return nil, fmt.Errorf("storage: assignments decode: %w", err)
	}
	if len(selections) == 0 {
		return nil, fmt.Errorf("storage: at least one assignment is required")
	}
	seen := map[Class]bool{}
	for i := range selections {
		if _, err := ProfileFor(selections[i].Class); err != nil {
			return nil, fmt.Errorf("storage: assignment #%d: %w", i, err)
		}
		if len(selections[i].Mountpoint) == 0 || selections[i].Mountpoint[0] != '/' {
			return nil, fmt.Errorf("storage: assignment #%d mountpoint must be absolute", i)
		}
		if seen[selections[i].Class] {
			return nil, fmt.Errorf("storage: duplicate assignment for %s", selections[i].Class)
		}
		seen[selections[i].Class] = true
	}
	return selections, nil
}

// Save validates the payload for step and persists it.
func (w *Wizard) Save(step string, payload map[string]any) error {
	idx := storageStepIndex(step)
	if idx < 0 {
		return fmt.Errorf("storage: unknown step %q", step)
	}
	if _, err := decodeMounts(payload); err != nil {
		return err
	}
	if idx >= storageStepIndex("classes") {
		if _, err := decodeClasses(payload); err != nil {
			return err
		}
	}
	_, err := w.wizards.Put(StorageWizardID, step, payload)
	return err
}

// selection loads the saved payload.
func (w *Wizard) selection() (map[string]any, error) {
	state, err := w.wizards.Get(StorageWizardID)
	if err != nil {
		return nil, err
	}
	return state.Payload, nil
}

// MountReadiness reports one selection against live facts: registrable
// with the reason when not.
type MountReadiness struct {
	Selection MountSelection
	Ready     bool
	Detail    string
}

// AssignmentReadiness reports one class binding against live facts:
// approved plus suitable, with the reason when not.
type AssignmentReadiness struct {
	Selection ClassSelection
	Ready     bool
	Detail    string
}

// SetupPlan is the reviewable outcome: per-mount and per-class
// readiness. Callers apply from it, then re-check; the plan itself
// changes nothing.
type SetupPlan struct {
	Mounts      []MountReadiness
	Assignments []AssignmentReadiness
}

// Ready reports a fully actionable plan.
func (p SetupPlan) Ready() bool {
	for _, mount := range p.Mounts {
		if !mount.Ready {
			return false
		}
	}
	for _, assignment := range p.Assignments {
		if !assignment.Ready {
			return false
		}
	}
	return len(p.Mounts) > 0 && len(p.Assignments) > 0
}

// Plan previews the saved selections against discovered facts without
// writing anything: registrations that would succeed, assignments that
// would bind, each with reasons where not.
func (w *Wizard) Plan(discovered discover.Storage) (SetupPlan, error) {
	payload, err := w.selection()
	if err != nil {
		return SetupPlan{}, err
	}
	mounts, err := decodeMounts(payload)
	if err != nil {
		return SetupPlan{}, err
	}
	selections, err := decodeClasses(payload)
	if err != nil {
		return SetupPlan{}, err
	}
	byPoint := map[string]discover.Mount{}
	for _, mount := range discovered.Mounts {
		byPoint[mount.Mountpoint] = mount
	}
	plan := SetupPlan{}
	for _, selection := range mounts {
		mount, found := byPoint[selection.Mountpoint]
		plan.Mounts = append(plan.Mounts, mountReadiness(selection, mount, found))
	}
	registered := map[string]bool{}
	for _, existing := range mustListRegistrations(w) {
		registered[existing.Mountpoint] = true
	}
	for _, selection := range mounts {
		registered[selection.Mountpoint] = true
	}
	for _, selection := range selections {
		plan.Assignments = append(plan.Assignments, assignmentReadiness(selection, registered, discovered))
	}
	return plan, nil
}

// mustListRegistrations tolerates store errors with an empty set: Plan
// previews, and Apply surfaces real failures.
func mustListRegistrations(w *Wizard) []Registration {
	listed, err := w.registry.List()
	if err != nil {
		return nil
	}
	return listed
}

// mountReadiness judges one selection against its discovered facts.
func mountReadiness(selection MountSelection, mount discover.Mount, found bool) MountReadiness {
	readiness := MountReadiness{Selection: selection}
	if !found {
		readiness.Detail = "not a discovered mount"
		return readiness
	}
	if !mount.Accessible {
		readiness.Detail = "serves no usage data"
		return readiness
	}
	if mount.ReadOnly {
		readiness.Detail = "is read-only"
		return readiness
	}
	if pseudoFilesystems[mount.FSType] {
		readiness.Detail = "is a pseudo-filesystem"
		return readiness
	}
	readiness.Ready = true
	readiness.Detail = "registrable"
	return readiness
}

// assignmentReadiness judges one class binding: approved (already
// registered or registering in this plan) plus suitable under live
// facts, with minimum free space from the selection.
func assignmentReadiness(selection ClassSelection, registered map[string]bool, discovered discover.Storage) AssignmentReadiness {
	readiness := AssignmentReadiness{Selection: selection}
	if !registered[selection.Mountpoint] {
		readiness.Detail = "mountpoint not registered"
		return readiness
	}
	suitable, err := SuitableMounts(selection.Class, discovered.Mounts, selection.MinFreeBytes)
	if err != nil {
		readiness.Detail = err.Error()
		return readiness
	}
	for _, mount := range suitable {
		if mount.Mountpoint == selection.Mountpoint {
			readiness.Ready = true
			readiness.Detail = "bindable"
			return readiness
		}
	}
	readiness.Detail = "mountpoint not suitable for class"
	return readiness
}

// ApplyMounts registers every saved mount, failing fast with the
// offending mountpoint named. Actor rides the call, never the payload.
func (w *Wizard) ApplyMounts(actor string, discovered discover.Storage) ([]Registration, error) {
	payload, err := w.selection()
	if err != nil {
		return nil, err
	}
	mounts, err := decodeMounts(payload)
	if err != nil {
		return nil, err
	}
	var out []Registration
	for i := range mounts {
		registration, err := w.registry.Register(mounts[i].Mountpoint, mounts[i].Label, actor, discovered)
		if err != nil {
			return nil, fmt.Errorf("mount #%d: %w", i, err)
		}
		out = append(out, registration)
	}
	return out, nil
}

// ApplyAssignments binds every saved class, failing fast with the
// offending class named.
func (w *Wizard) ApplyAssignments(actor string, discovered discover.Storage) ([]Assignment, error) {
	payload, err := w.selection()
	if err != nil {
		return nil, err
	}
	selections, err := decodeClasses(payload)
	if err != nil {
		return nil, err
	}
	var out []Assignment
	for i := range selections {
		assignment, err := w.assignments.Assign(selections[i].Class, selections[i].Mountpoint, actor, w.registry, discovered, selections[i].MinFreeBytes)
		if err != nil {
			return nil, fmt.Errorf("assignment #%d: %w", i, err)
		}
		out = append(out, assignment)
	}
	return out, nil
}

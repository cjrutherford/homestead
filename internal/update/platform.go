package update

import (
	"fmt"
	"sort"
)

// Platform components naming the Homestead software itself, as
// distinct from catalog applications. The dashboard is not a
// component: its assets embed in the daemon binary at build time
// (make dashboard-embed, -tags embeddash), so dashboard updates ride
// daemon releases by construction and a dashboard-only update is
// indescribable here. Application update flows must never accept
// these names — see IsPlatformComponent — because the compose-based
// application applier cannot replace the daemon out from under
// itself.
const (
	ComponentDaemon = "homestead-server"
	ComponentCLI    = "homesteadctl"
)

// platformComponents is the closed set: unknown names refuse rather
// than plan against components nothing knows how to replace.
var platformComponents = map[string]bool{
	ComponentDaemon: true,
	ComponentCLI:    true,
}

// IsPlatformComponent reports whether a name addresses the Homestead
// platform instead of a catalog application. Both namespaces share
// the lowercase-hyphen shape, so membership — not syntax — draws the
// boundary.
func IsPlatformComponent(name string) bool {
	return platformComponents[name]
}

// PlatformRelease is one desired component version: owner-declared,
// never fetched. Homestead has no hosted release feed by
// customer-ownership rule, so targets arrive explicitly with the
// release the owner obtained; a later distribution slice owns
// acquisition, and this model only compares.
type PlatformRelease struct {
	Component string
	Version   string
}

// Validate checks release shape: a known component and a parseable
// version. Parseability matters because unparseable versions cannot
// order, and unordered targets would approve blind jumps.
func (r PlatformRelease) Validate() error {
	if !IsPlatformComponent(r.Component) {
		return fmt.Errorf("update: unknown platform component %q", r.Component)
	}
	if _, _, ok := splitTag(r.Version); !ok {
		return fmt.Errorf("update: unparseable platform version %q", r.Version)
	}
	return nil
}

// ComponentUpdate is one component's version move: current running
// version and approved target.
type ComponentUpdate struct {
	Component string
	Current   string
	Target    string
}

// PlatformPlan is the reviewable change set for the platform,
// deliberately shaped apart from UpdatePlan: no services, no image
// moves, no compose applier. Backup always precedes platform moves —
// the daemon owns SQLite and YAML state, and binary replacement
// without a state backup strands recovery.
type PlatformPlan struct {
	Components []ComponentUpdate
	Backup     bool
	BackupWhy  string
	Warnings   []string
}

// platformNewer orders platform versions: greater numeric prefixes
// move forward, and equal numerics with differing suffixes move
// forward as rebuilds (0.0.0-s002 to 0.0.0-s003) — suffixes do not
// order, so differing suffixes read as a new build rather than a
// ranked step, and desired versions stay owner-visible in review.
// Smaller numerics and identical strings do not move. Downgrades
// refuse at plan time; explicit rollback belongs to a later slice.
func platformNewer(current, target string) bool {
	if current == target {
		return false
	}
	currentNumbers, currentSuffix, ok := splitTag(current)
	if !ok {
		return false
	}
	targetNumbers, targetSuffix, ok := splitTag(target)
	if !ok {
		return false
	}
	for i := 0; i < len(currentNumbers) && i < len(targetNumbers); i++ {
		if targetNumbers[i] != currentNumbers[i] {
			return targetNumbers[i] > currentNumbers[i]
		}
	}
	if len(targetNumbers) != len(currentNumbers) {
		return len(targetNumbers) > len(currentNumbers)
	}
	return targetSuffix != currentSuffix
}

// platformMajorBump reports major version changes between bare
// versions: a differing first numeric component with the target
// ahead. Unparseable versions never claim majorness.
func platformMajorBump(current, target string) bool {
	currentNumbers, _, ok := splitTag(current)
	if !ok || len(currentNumbers) == 0 {
		return false
	}
	targetNumbers, _, ok := splitTag(target)
	if !ok || len(targetNumbers) == 0 {
		return false
	}
	return targetNumbers[0] > currentNumbers[0]
}

// PlanPlatform produces the platform plan from running versions by
// component and desired releases: validated moves only, current
// components staying out, downgrades and strangers refused. Empty
// moves plan cleanly — nothing to do reads as nothing, never as
// failure. Desired versions are explicit owner input; nothing here
// discovers upstream.
func PlanPlatform(running map[string]string, desired []PlatformRelease) (PlatformPlan, error) {
	var plan PlatformPlan
	seen := map[string]bool{}
	for _, release := range desired {
		if err := release.Validate(); err != nil {
			return PlatformPlan{}, err
		}
		if seen[release.Component] {
			return PlatformPlan{}, fmt.Errorf("update: duplicate platform component %q", release.Component)
		}
		seen[release.Component] = true
		current, ok := running[release.Component]
		if !ok {
			return PlatformPlan{}, fmt.Errorf("update: unknown running version for %q", release.Component)
		}
		if _, _, ok := splitTag(current); !ok {
			return PlatformPlan{}, fmt.Errorf("update: unparseable running version %q", current)
		}
		if current == release.Version {
			continue
		}
		if !platformNewer(current, release.Version) {
			return PlatformPlan{}, fmt.Errorf("update: %s downgrade %s -> %s refused", release.Component, current, release.Version)
		}
		plan.Components = append(plan.Components, ComponentUpdate{
			Component: release.Component, Current: current, Target: release.Version,
		})
		if platformMajorBump(current, release.Version) {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("%s jumps major versions (%s -> %s); expect migrations",
					release.Component, current, release.Version))
		}
	}
	sort.Slice(plan.Components, func(i, j int) bool { return plan.Components[i].Component < plan.Components[j].Component })
	if len(plan.Components) > 0 {
		plan.Backup = true
		plan.BackupWhy = "daemon owns SQLite and YAML state; back up before replacing binaries"
		for _, move := range plan.Components {
			if move.Component == ComponentDaemon {
				plan.Warnings = append(plan.Warnings,
					"daemon restarts to apply; API and dashboard pause during restart")
				break
			}
		}
	}
	return plan, nil
}

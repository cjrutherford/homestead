package update

import (
	"fmt"
	"sort"
	"strings"

	"homestead/internal/catalog"
)

// ServiceUpdate is one service's version move: current running image,
// target image, and where the target came from. Targets prefer the
// newest registry listing, falling back to manifest pins when
// registries stay silent — curated intent outranks silence, never
// staleness.
type ServiceUpdate struct {
	Service string
	Current string
	Target  string
	Source  string
}

// UpdatePlan is the reviewable change set for one package: per-service
// moves, whether backups must run first with the reason, affected
// services to restart, and warnings worth reading before approving —
// notably major version jumps, which routinely carry migrations.
type UpdatePlan struct {
	Package   string
	Services  []ServiceUpdate
	Backup    bool
	BackupWhy string
	Affected  []string
	Warnings  []string
}

// PlanUpdate produces the update plan from a manifest, running image
// tags by service, and registry-newer tags by service: registry
// maxima win where present, manifest pins cover the rest, and
// services already current stay out. Backup flags whenever the
// manifest declares database exports or backup paths; affected lists
// updated services. Empty moves plan cleanly — nothing to do reads as
// nothing, never as failure.
func PlanUpdate(manifest catalog.Manifest, running map[string]string, registry map[string][]string) (UpdatePlan, error) {
	if err := manifest.Validate(); err != nil {
		return UpdatePlan{}, err
	}
	images := map[string]string{}
	for _, service := range manifest.Services {
		images[service.Name] = service.Image
	}
	plan := UpdatePlan{Package: manifest.Name}
	for _, service := range manifest.Services {
		current, ok := running[service.Name]
		if !ok {
			continue
		}
		target := images[service.Name]
		source := SourceManifest
		if newer := NewerTags(tagOf(target), registry[service.Name]); len(newer) > 0 {
			target = imageWithTag(target, newer[0])
			source = SourceRegistry
		}
		if current == target {
			continue
		}
		plan.Services = append(plan.Services, ServiceUpdate{
			Service: service.Name, Current: current, Target: target, Source: source,
		})
		if majorBump(current, target) {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("%s jumps major versions (%s -> %s); expect migrations",
					service.Name, tagOf(current), tagOf(target)))
		}
	}
	sort.Slice(plan.Services, func(i, j int) bool { return plan.Services[i].Service < plan.Services[j].Service })
	if manifest.Database != nil || len(manifest.BackupPaths) > 0 {
		plan.Backup = true
		plan.BackupWhy = backupReason(manifest)
	}
	for _, update := range plan.Services {
		plan.Affected = append(plan.Affected, update.Service)
	}
	return plan, nil
}

// tagOf extracts the tag from a pinned image reference.
func tagOf(image string) string {
	if index := strings.LastIndex(image, ":"); index >= 0 {
		return image[index+1:]
	}
	return ""
}

// imageWithTag swaps an image reference's tag, preserving registry
// paths and digests left of the final colon.
func imageWithTag(image, tag string) string {
	if index := strings.LastIndex(image, ":"); index >= 0 {
		return image[:index+1] + tag
	}
	return image + ":" + tag
}

// majorBump reports major version changes between image tags: the
// first numeric component differing with an upgrade on the left.
// Unparseable tags never claim majorness — unknown means cautious,
// not alarming.
func majorBump(current, target string) bool {
	currentTag := tagOf(current)
	targetTag := tagOf(target)
	currentNumbers, _, ok := splitTag(currentTag)
	if !ok || len(currentNumbers) == 0 {
		return false
	}
	targetNumbers, _, ok := splitTag(targetTag)
	if !ok || len(targetNumbers) == 0 {
		return false
	}
	if currentNumbers[0] == targetNumbers[0] {
		return false
	}
	for i := 0; i < len(currentNumbers) && i < len(targetNumbers); i++ {
		if targetNumbers[i] != currentNumbers[i] {
			return targetNumbers[i] > currentNumbers[i]
		}
	}
	return len(targetNumbers) > len(currentNumbers)
}

// backupReason names why backups precede an update: database exports
// first (consistency matters most), then declared paths.
func backupReason(manifest catalog.Manifest) string {
	if manifest.Database != nil {
		return "database exports declared"
	}
	return "backup paths declared"
}

package storage

import (
	"fmt"

	"homestead/internal/discover"
)

// Health grades one registered mount: healthy serves reads and
// writes with room to spare, degraded serves with caveats
// (read-only, unreadable usage, or low space), and missing vanished
// from discovery — mount loss, the headline case this unit exists to
// catch. Device-identity drift (same mountpoint, swapped disk) stays
// out: registrations key mountpoints, and re-resolving identity
// belongs to a future that tracks UUIDs alongside approvals.
const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthMissing  = "missing"
)

// SpaceWarnPercent marks low space: use at or above this percentage
// degrades an otherwise healthy mount, since full filesystems fail
// writes in confusing ways long before they report errors.
const SpaceWarnPercent = 90

// MountHealth is the observed state of one registered mountpoint with
// the evidence attached: presence, writability, and space numbers.
type MountHealth struct {
	Mountpoint string
	Status     string
	FreeBytes  uint64
	TotalBytes uint64
	UsePercent int
	Detail     string
}

// CheckMount grades one mountpoint against discovered facts. Unknown
// mountpoints report missing — loss and never-existed share the
// verdict deliberately, since both mean data is not where expected.
func CheckMount(mountpoint string, discovered discover.Storage) MountHealth {
	health := MountHealth{Mountpoint: mountpoint}
	for _, mount := range discovered.Mounts {
		if mount.Mountpoint != mountpoint {
			continue
		}
		health.FreeBytes = mount.FreeBytes
		health.TotalBytes = mount.TotalBytes
		if mount.TotalBytes > 0 {
			health.UsePercent = int((mount.TotalBytes - mount.FreeBytes) * 100 / mount.TotalBytes)
		}
		switch {
		case !mount.Accessible:
			health.Status = HealthDegraded
			health.Detail = "serves no usage data; mount may be stale"
		case mount.ReadOnly:
			health.Status = HealthDegraded
			health.Detail = "mounted read-only; writes will fail"
		case health.UsePercent >= SpaceWarnPercent:
			health.Status = HealthDegraded
			health.Detail = fmt.Sprintf("only %d%% free; writes risk failure", 100-health.UsePercent)
		default:
			health.Status = HealthHealthy
			health.Detail = "serving reads and writes"
		}
		return health
	}
	health.Status = HealthMissing
	health.Detail = "absent from discovery; mount lost or never present"
	return health
}

// Summary rolls a health set up: counts per status with overall
// mirroring the worst — missing over degraded over healthy — so one
// glance tells owners whether storage needs them now.
type Summary struct {
	Healthy  int
	Degraded int
	Missing  int
	Overall  string
}

// CheckAll grades every registered mountpoint in registry order of
// the provided list. Callers pass live registrations; the unit never
// reads stores itself, keeping checks pure against facts.
func CheckAll(registrations []Registration, discovered discover.Storage) ([]MountHealth, Summary) {
	var out []MountHealth
	summary := Summary{Overall: HealthHealthy}
	for _, registration := range registrations {
		health := CheckMount(registration.Mountpoint, discovered)
		out = append(out, health)
		switch health.Status {
		case HealthMissing:
			summary.Missing++
			summary.Overall = HealthMissing
		case HealthDegraded:
			summary.Degraded++
			if summary.Overall == HealthHealthy {
				summary.Overall = HealthDegraded
			}
		default:
			summary.Healthy++
		}
	}
	return out, summary
}

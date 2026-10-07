package storage

import (
	"fmt"
)

// Class names what stored data is for. Classes steer mount selection
// (S141), assignment (S142), provisioning (S143), and backup defaults
// — never partitioning or formatting, which ADR-013 defers. New
// classes arrive with the slice that needs them, never speculatively.
type Class string

const (
	// ClassApplication holds general application data: volumes and
	// state that is neither latency-critical nor disposable.
	ClassApplication Class = "application"
	// ClassDatabase holds latency-sensitive structured data. Locking
	// hazards keep databases off network filesystems, so this class
	// alone requires local mounts.
	ClassDatabase Class = "database"
	// ClassBulk holds large media libraries: throughput matters, size
	// dominates, and backups default off until owners opt in.
	ClassBulk Class = "bulk"
	// ClassBackup holds backup targets themselves: written by backup
	// flows, never themselves backed up.
	ClassBackup Class = "backup"
	// ClassCache holds regenerable data: safe to evict under pressure
	// and excluded from backups.
	ClassCache Class = "cache"
)

// classes orders the vocabulary for UIs and iteration.
var classes = []Class{
	ClassApplication, ClassDatabase, ClassBulk, ClassBackup, ClassCache,
}

// Classes returns the vocabulary in stable order.
func Classes() []Class {
	out := make([]Class, len(classes))
	copy(out, classes)
	return out
}

// Valid reports a known class.
func (c Class) Valid() bool {
	for _, known := range classes {
		if c == known {
			return true
		}
	}
	return false
}

// Profile describes one class's handling contract: where it may live,
// whether eviction is safe, and whether backups take it by default.
// Media-type awareness (SSD versus spinning) stays out: discovery
// reports space, identity, and writability only, and profiles must
// never promise what facts cannot match.
type Profile struct {
	Class         Class
	Label         string
	Description   string
	LocalOnly     bool
	Ephemeral     bool
	BackupDefault bool
}

// profiles carries the five contracts: only databases demand local
// mounts, only caches evict, and bulk, backup targets, and caches
// stay out of default backups for size, recursion, and futility.
var profiles = map[Class]Profile{
	ClassApplication: {
		Class: ClassApplication, Label: "Application data",
		Description:   "General application volumes and state.",
		BackupDefault: true,
	},
	ClassDatabase: {
		Class: ClassDatabase, Label: "Databases",
		Description:   "Latency-sensitive structured data; local mounts only.",
		LocalOnly:     true,
		BackupDefault: true,
	},
	ClassBulk: {
		Class: ClassBulk, Label: "Bulk media",
		Description: "Large libraries where size dominates; backups opt-in.",
	},
	ClassBackup: {
		Class: ClassBackup, Label: "Backup targets",
		Description: "Destinations written by backup flows, never themselves backed up.",
	},
	ClassCache: {
		Class: ClassCache, Label: "Cache",
		Description: "Regenerable data, safe to evict and excluded from backups.",
		Ephemeral:   true,
	},
}

// ProfileFor returns the handling contract for a class. Unknown
// classes fail instead of returning a permissive zero profile that
// would read as "store anywhere".
func ProfileFor(class Class) (Profile, error) {
	profile, ok := profiles[class]
	if !ok {
		return Profile{}, fmt.Errorf("storage: unknown class %q", class)
	}
	return profile, nil
}

package storage

import (
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/discover"
)

// Mount kinds (S293). Discovery lists every mounted filesystem; the kind
// tells an owner which ones are worth a look.
const (
	// KindData is a separate disk that can be registered: the usual home
	// for app data, media and backups.
	KindData = "data"
	// KindSystem can be registered but sits on the appliance's own system
	// disk (the root filesystem, or a path bound from it, such as the
	// daemon's own state directories).
	KindSystem = "system"
	// KindUnusable can never be registered (see Registrable).
	KindUnusable = "unusable"
)

// systemPrefixes are paths that belong to the operating system or to
// Homestead itself, whatever device backs them.
var systemPrefixes = []string{"/boot", "/etc", "/proc", "/sys", "/dev", "/run", "/usr", "/var", "/opt/homestead", "/snap"}

// MountKind classifies one discovered mount against the whole list: a
// registrable filesystem on a block device other than the root
// filesystem's, outside system paths, is a data disk.
func MountKind(m discover.Mount, all []discover.Mount) string {
	if Registrable(m) != nil {
		return KindUnusable
	}
	if m.Mountpoint == "/" || !strings.HasPrefix(m.Device, "/dev/") {
		return KindSystem
	}
	for _, prefix := range systemPrefixes {
		if m.Mountpoint == prefix || strings.HasPrefix(m.Mountpoint, prefix+"/") {
			return KindSystem
		}
	}
	for _, other := range all {
		if other.Mountpoint == "/" && other.Device == m.Device {
			return KindSystem
		}
	}
	return KindData
}

// nameHints map words in a mount's label or path to the classes they
// suggest. The first match wins, in this order.
var nameHints = []struct {
	words   []string
	classes []Class
}{
	{[]string{"backup", "backups"}, []Class{ClassBackup}},
	{[]string{"bulk", "media", "library", "movies", "photos", "files"}, []Class{ClassBulk}},
	{[]string{"app", "apps", "application", "applications", "db", "database", "ssd", "fast"}, []Class{ClassApplication, ClassDatabase}},
}

func hasClass(classes []Class, c Class) bool {
	for _, have := range classes {
		if have == c {
			return true
		}
	}
	return false
}

func hinted(m discover.Mount) []Class {
	names := strings.FieldsFunc(strings.ToLower(m.Label+" "+filepath.Base(m.Mountpoint)), func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	})
	for _, hint := range nameHints {
		for _, word := range hint.words {
			for _, name := range names {
				if name == word {
					return hint.classes
				}
			}
		}
	}
	return nil
}

// Suggest proposes a class layout over the data disks (S293). It never
// moves an assigned class: only unassigned classes are suggested. A
// disk's label or path names it first (backup, media, apps); the rest
// go by size: application and database on the smallest remaining disk,
// bulk on the largest, backup on a third if there is one. A single disk
// gets application, database and bulk; backup is never suggested on a
// disk that holds other classes, since a backup next to its data is not
// a backup. Cache is left to the owner. The result maps mountpoint to
// classes; a suggestion is a proposal, nothing is registered or assigned.
func Suggest(all []discover.Mount, assigned map[Class]string) map[string][]Class {
	var disks []discover.Mount
	seen := map[string]bool{}
	for _, m := range all {
		if !seen[m.Mountpoint] && MountKind(m, all) == KindData {
			seen[m.Mountpoint] = true
			disks = append(disks, m)
		}
	}
	out := map[string][]Class{}
	open := func(c Class) bool {
		if _, ok := assigned[c]; ok {
			return false
		}
		for _, classes := range out {
			if hasClass(classes, c) {
				return false
			}
		}
		return true
	}
	give := func(mountpoint string, classes ...Class) {
		for _, c := range classes {
			if open(c) {
				out[mountpoint] = append(out[mountpoint], c)
			}
		}
	}
	if len(disks) == 1 && !hasClass(hinted(disks[0]), ClassBackup) {
		// One disk holds everything but backups, whatever its name.
		give(disks[0].Mountpoint, ClassApplication, ClassDatabase, ClassBulk)
		disks = nil
	}
	var rest []discover.Mount
	for _, m := range disks {
		if classes := hinted(m); classes != nil {
			give(m.Mountpoint, classes...)
			continue
		}
		rest = append(rest, m)
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].TotalBytes < rest[j].TotalBytes })
	switch len(rest) {
	case 0:
	case 1:
		give(rest[0].Mountpoint, ClassApplication, ClassDatabase, ClassBulk)
	default:
		give(rest[0].Mountpoint, ClassApplication, ClassDatabase)
		give(rest[len(rest)-1].Mountpoint, ClassBulk)
		if len(rest) > 2 {
			give(rest[1].Mountpoint, ClassBackup)
		}
	}
	for mountpoint, classes := range out {
		if len(classes) == 0 {
			delete(out, mountpoint)
		}
	}
	return out
}

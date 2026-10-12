package catalog

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"homestead/internal/storage"
)

// defaultDataMode is the directory mode declarations get when they
// name none: owner and group only, matching provisioning's floor.
const defaultDataMode = "0750"

// DataPath declares one container path an image writes (S276): where
// it lives in the container and where Homestead places it on mounted
// storage. Persistent paths bind to <class mount>/<package>/<subpath>;
// Ephemeral paths are deliberately not persisted and render as tmpfs,
// so an image VOLUME never falls back to an anonymous Docker volume.
// Every image path must be declared one way or the other — a test
// compares declarations against each image's known VOLUMEs.
type DataPath struct {
	// Target is the absolute container path.
	Target string
	// Class is the storage class (application or bulk) holding the
	// path; empty only for ephemeral paths.
	Class storage.Class
	// Subpath is relative to <class mount>/<package>.
	Subpath string
	// Database marks paths holding database files, which file-level
	// copies cannot snapshot consistently (exports cover them).
	Database bool
	// Ephemeral marks a path deliberately not persisted; Reason must
	// justify it.
	Ephemeral bool
	Reason    string
	// SizeMB caps an ephemeral tmpfs (default 256).
	SizeMB int
	// Mode, UID, and GID are what the image needs on the host
	// directory: octal mode string (default 0750), and numeric owner
	// (unset leaves ownership untouched).
	Mode string
	UID  *int
	GID  *int
}

// dataClassValid limits declarations to application data and bulk
// libraries. Database/cache/backup classes stay out until a slice
// assigns them semantics for catalog apps.
func dataClassValid(class storage.Class) bool {
	return class == storage.ClassApplication || class == storage.ClassBulk
}

// DirMode parses the declared (or default) mode.
func (d DataPath) DirMode() (os.FileMode, error) {
	text := d.Mode
	if text == "" {
		text = defaultDataMode
	}
	parsed, err := strconv.ParseUint(text, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("catalog: data mode %q must be octal", text)
	}
	return os.FileMode(parsed), nil
}

// Owner resolves the declared ownership, unset ids as storage.UnsetID.
func (d DataPath) Owner() (uid, gid int) {
	uid, gid = storage.UnsetID, storage.UnsetID
	if d.UID != nil {
		uid = *d.UID
	}
	if d.GID != nil {
		gid = *d.GID
	}
	return uid, gid
}

// Validate checks one declaration against the classes the manifest
// requires (an install must be able to evaluate the class it uses).
func (d DataPath) Validate(required map[storage.Class]bool) error {
	if d.Target == "" || d.Target[0] != '/' || path.Clean(d.Target) != d.Target || d.Target == "/" {
		return fmt.Errorf("catalog: data target %q must be an absolute clean path", d.Target)
	}
	if d.Ephemeral {
		if d.Reason == "" {
			return fmt.Errorf("catalog: ephemeral data %q needs a reason", d.Target)
		}
		if d.SizeMB < 0 || d.SizeMB > 4096 {
			return fmt.Errorf("catalog: ephemeral data %q size must be 0-4096 MB", d.Target)
		}
		if d.Class != "" || d.Subpath != "" || d.Database || d.Mode != "" || d.UID != nil || d.GID != nil {
			return fmt.Errorf("catalog: ephemeral data %q takes no storage fields", d.Target)
		}
		return nil
	}
	if d.SizeMB != 0 {
		return fmt.Errorf("catalog: data %q: size applies to ephemeral paths only", d.Target)
	}
	if d.Reason != "" {
		return fmt.Errorf("catalog: data %q: reason applies to ephemeral paths only", d.Target)
	}
	if !dataClassValid(d.Class) {
		return fmt.Errorf("catalog: data %q class must be application or bulk", d.Target)
	}
	if !required[d.Class] {
		return fmt.Errorf("catalog: data %q uses class %s the manifest does not require", d.Target, d.Class)
	}
	if err := validateSubpath(d.Subpath); err != nil {
		return fmt.Errorf("catalog: data %q: %w", d.Target, err)
	}
	mode, err := d.DirMode()
	if err != nil {
		return err
	}
	uid, gid := d.Owner()
	if err := (storage.Directory{Path: "x", Mode: mode, Uid: uid, Gid: gid}).Validate(); err != nil {
		return fmt.Errorf("catalog: data %q: %w", d.Target, err)
	}
	return nil
}

// validateData checks every service's declarations: valid, unique
// targets per service, and agreement wherever services share a host
// directory (one directory cannot carry two owners).
func validateData(services []Service, requirements Requirements) error {
	required := map[storage.Class]bool{}
	for _, need := range requirements.Storage {
		required[need.Class] = true
	}
	type placement struct {
		mode os.FileMode
		uid  int
		gid  int
	}
	shared := map[string]placement{}
	for _, service := range services {
		seen := map[string]bool{}
		for i := range service.Data {
			data := service.Data[i]
			if err := data.Validate(required); err != nil {
				return fmt.Errorf("catalog: service %q: %w", service.Name, err)
			}
			if seen[data.Target] {
				return fmt.Errorf("catalog: service %q declares data %q twice", service.Name, data.Target)
			}
			seen[data.Target] = true
			if data.Ephemeral {
				continue
			}
			mode, _ := data.DirMode()
			uid, gid := data.Owner()
			key := string(data.Class) + ":" + path.Clean(data.Subpath)
			current := placement{mode: mode, uid: uid, gid: gid}
			if previous, ok := shared[key]; ok && previous != current {
				return fmt.Errorf("catalog: service %q data %q shares %s with conflicting mode or owner", service.Name, data.Target, key)
			}
			shared[key] = current
		}
	}
	return nil
}

// PlannedMount is one resolved container mount in a plan: the
// catalog-declared ones and owner extras alike, with the absolute host
// source Compose receives (empty for tmpfs). Plans list these so the
// owner reviews exactly where data will live before anything writes.
type PlannedMount struct {
	Service   string
	Target    string
	Source    string
	Class     storage.Class
	Database  bool
	Ephemeral bool
	// TmpfsMB is the ephemeral size cap in megabytes.
	TmpfsMB  int
	ReadOnly bool
	// Declared distinguishes catalog declarations from owner extras.
	Declared bool
}

// ResolveData resolves every declaration in a manifest against class
// mountpoints: planned mounts in service order plus the host
// directories each persistent path needs (deduplicated where services
// share one). A class without a mountpoint is a requirements failure.
func ResolveData(manifest Manifest, mountpoints map[storage.Class]string) ([]PlannedMount, []ProvisionDir, error) {
	var mounts []PlannedMount
	var dirs []ProvisionDir
	seen := map[string]bool{}
	for _, service := range manifest.Services {
		for _, data := range service.Data {
			if data.Ephemeral {
				size := data.SizeMB
				if size == 0 {
					size = int(defaultTmpfsBytes >> 20)
				}
				mounts = append(mounts, PlannedMount{
					Service: service.Name, Target: data.Target, Ephemeral: true, TmpfsMB: size, Declared: true,
				})
				continue
			}
			mountpoint := mountpoints[data.Class]
			if mountpoint == "" {
				return nil, nil, fmt.Errorf("catalog: requirements unmet for %q: storage class %s has no mountpoint (needed by %s %s)",
					manifest.Name, data.Class, service.Name, data.Target)
			}
			relative := manifest.Name + "/" + data.Subpath
			mounts = append(mounts, PlannedMount{
				Service: service.Name, Target: data.Target, Source: mountpoint + "/" + relative,
				Class: data.Class, Database: data.Database, Declared: true,
			})
			mode, err := data.DirMode()
			if err != nil {
				return nil, nil, err
			}
			key := mountpoint + "\x00" + relative
			if seen[key] {
				continue
			}
			seen[key] = true
			uid, gid := data.Owner()
			dirs = append(dirs, ProvisionDir{
				Mountpoint: mountpoint,
				Directory:  storage.Directory{Path: relative, Mode: mode, Uid: uid, Gid: gid},
			})
		}
	}
	dirs = append(dirs, nestedStubs(manifest, mountpoints, seen)...)
	return mounts, dirs, nil
}

// nestedStubs provisions mountpoint directories for declarations
// nested inside another persistent mount of the same service (the
// Nextcloud data directory inside html). Without them the container
// runtime creates a root-owned stub inside the parent's host
// directory; provisioning them with the nested path's own mode and
// owner keeps the parent writable by the app user.
func nestedStubs(manifest Manifest, mountpoints map[storage.Class]string, seen map[string]bool) []ProvisionDir {
	var out []ProvisionDir
	for _, service := range manifest.Services {
		for _, nested := range service.Data {
			if nested.Ephemeral {
				continue
			}
			for _, parent := range service.Data {
				if parent.Ephemeral || parent.Target == nested.Target ||
					!strings.HasPrefix(nested.Target, parent.Target+"/") {
					continue
				}
				mountpoint := mountpoints[parent.Class]
				relative := manifest.Name + "/" + parent.Subpath + strings.TrimPrefix(nested.Target, parent.Target)
				key := mountpoint + "\x00" + relative
				if seen[key] {
					continue
				}
				seen[key] = true
				mode, _ := nested.DirMode()
				uid, gid := nested.Owner()
				out = append(out, ProvisionDir{
					Mountpoint: mountpoint,
					Directory:  storage.Directory{Path: relative, Mode: mode, Uid: uid, Gid: gid},
				})
			}
		}
	}
	return out
}

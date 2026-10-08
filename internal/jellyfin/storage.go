package jellyfin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"homestead/internal/catalog"
	"homestead/internal/storage"
)

var validLibraryName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// MediaDir is one owner library: a name for the container path plus
// the host directory holding it. Host directories carry owner data, so
// planning validates them against live state instead of assuming.
type MediaDir struct {
	Name     string
	HostPath string
}

// StoragePlan maps Jellyfin's storage needs to concrete directories:
// configuration under the application assignment (provisioned by
// Homestead) and media under the bulk assignment (referenced, never
// created — libraries predate installs and outlive them).
type StoragePlan struct {
	ConfigMountpoint string
	ConfigPath       string
	Media            []MediaBinding
}

// MediaBinding is one library resolved to its mount, relative path,
// and container target.
type MediaBinding struct {
	Mountpoint string
	Subpath    string
	Target     string
}

// configTarget is the container configuration directory.
const configTarget = "/config"

// PlanStorage maps the manifest's storage needs to directories: every
// required class needs an assignment, media libraries must exist as
// directories under the bulk mount, and names stay unique for stable
// container paths. Transcode cache rides inside configuration —
// Jellyfin defaults there — so no cache class participates.
func PlanStorage(manifest catalog.Manifest, assignments map[storage.Class]string, media []MediaDir) (StoragePlan, error) {
	if err := manifest.Validate(); err != nil {
		return StoragePlan{}, err
	}
	needed := map[storage.Class]bool{}
	for _, need := range manifest.Requirements.Storage {
		needed[need.Class] = true
	}
	for class := range needed {
		if assignments[class] == "" {
			return StoragePlan{}, fmt.Errorf("jellyfin: class %s has no mountpoint", class)
		}
	}
	bulk := assignments[storage.ClassBulk]
	plan := StoragePlan{
		ConfigMountpoint: assignments[storage.ClassApplication],
		ConfigPath:       manifest.Name + "/config",
	}
	seen := map[string]bool{}
	for i := range media {
		if !validLibraryName.MatchString(media[i].Name) {
			return StoragePlan{}, fmt.Errorf("jellyfin: library #%d name must match [a-z][a-z0-9-]*", i)
		}
		if seen[media[i].Name] {
			return StoragePlan{}, fmt.Errorf("jellyfin: duplicate library %q", media[i].Name)
		}
		seen[media[i].Name] = true
		if !filepath.IsAbs(media[i].HostPath) {
			return StoragePlan{}, fmt.Errorf("jellyfin: library %q path must be absolute", media[i].Name)
		}
		cleaned := filepath.Clean(media[i].HostPath)
		if cleaned != bulk && !strings.HasPrefix(cleaned, bulk+string(os.PathSeparator)) {
			return StoragePlan{}, fmt.Errorf("jellyfin: library %q lives outside the bulk mount", media[i].Name)
		}
		info, err := os.Stat(cleaned)
		if err != nil {
			return StoragePlan{}, fmt.Errorf("jellyfin: library %q: %w", media[i].Name, err)
		}
		if !info.IsDir() {
			return StoragePlan{}, fmt.Errorf("jellyfin: library %q is not a directory", media[i].Name)
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(cleaned, bulk), string(os.PathSeparator))
		plan.Media = append(plan.Media, MediaBinding{
			Mountpoint: bulk,
			Subpath:    relative,
			Target:     "/media/" + media[i].Name,
		})
	}
	return plan, nil
}

// Volumes renders installer volume specs for one service: configuration
// writable, media writable by default (libraries keep artwork beside
// media unless owners say otherwise in later slices).
func (p StoragePlan) Volumes(service string) []catalog.VolumeSpec {
	out := []catalog.VolumeSpec{
		{Service: service, Class: storage.ClassApplication, Subpath: "config", Target: configTarget},
	}
	for _, library := range p.Media {
		out = append(out, catalog.VolumeSpec{
			Service: service, Class: storage.ClassBulk,
			Subpath: library.Subpath, Target: library.Target,
		})
	}
	return out
}

// ProvisionDirs renders the directories Homestead converges: the
// configuration subtree only. Media libraries are referenced, never
// provisioned — creating owner data directories would mask typos with
// empty libraries.
func (p StoragePlan) ProvisionDirs() []catalog.ProvisionDir {
	return []catalog.ProvisionDir{{
		Mountpoint: p.ConfigMountpoint,
		Directory: storage.Directory{
			Path: p.ConfigPath, Mode: 0o750, Uid: storage.UnsetID, Gid: storage.UnsetID,
		},
	}}
}

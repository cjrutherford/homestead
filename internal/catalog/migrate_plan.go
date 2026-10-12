package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/docker"
	"homestead/internal/storage"
)

// ErrInsufficientSpace marks plans refused for lack of free space on a
// target mount.
var ErrInsufficientSpace = errors.New("catalog: not enough free space for the migration")

// Planning constants. The downtime figure is an estimate the plan
// labels as such: copy throughput of a spinning disk plus restart
// overhead per service.
const (
	// copyBytesPerSecond is the throughput assumed for the copy.
	copyBytesPerSecond int64 = 100 << 20
	// restartBaseSeconds and restartPerServiceSeconds cover stop, start,
	// and health stabilization.
	restartBaseSeconds       = 30
	restartPerServiceSeconds = 10
	// spaceMarginPercent keeps headroom over the measured source size.
	spaceMarginPercent = 5
)

// MigrationItem is one directory copy: a volume (or a sub-path of one)
// onto its declared host path.
type MigrationItem struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	Volume  string `json:"volume"`
	// Source is the host directory copied from (the volume's data
	// directory, or a sub-directory of it for nested declarations).
	Source string `json:"source"`
	// Dest is the declared host path copied onto.
	Dest       string        `json:"dest"`
	Mountpoint string        `json:"mountpoint"`
	Class      storage.Class `json:"class"`
	// Excludes are source-relative paths another item copies (the
	// Nextcloud data directory inside the html volume).
	Excludes []string `json:"excludes,omitempty"`
	// Nested marks a path inside a parent volume: its source may be
	// legitimately absent (an empty path), unlike a volume root.
	Nested    bool  `json:"nested,omitempty"`
	SizeBytes int64 `json:"size_bytes"`
	Database  bool  `json:"database,omitempty"`
}

// OldVolume is a Docker volume the migration leaves behind. Removal is
// a separate owner action.
type OldVolume struct {
	Name      string `json:"name"`
	Service   string `json:"service"`
	Target    string `json:"target"`
	Source    string `json:"source"`
	SizeBytes int64  `json:"size_bytes"`
}

// TargetSpace is the space verdict for one target mount.
type TargetSpace struct {
	Mountpoint  string `json:"mountpoint"`
	NeededBytes int64  `json:"needed_bytes"`
	FreeBytes   uint64 `json:"free_bytes"`
	OK          bool   `json:"ok"`
}

// MigrationPlan is the reviewable change set: copies, resulting
// mounts, target space, the volumes left behind, and the downtime to
// expect. Nothing runs from a plan until the owner approves its digest.
type MigrationPlan struct {
	Package string `json:"package"`
	// Digest identifies the plan's structure (not sizes, which drift as
	// the app writes): approval must quote it, so the owner approves
	// exactly what was previewed.
	Digest      string          `json:"digest"`
	Items       []MigrationItem `json:"items"`
	OldVolumes  []OldVolume     `json:"old_volumes"`
	Directories []ProvisionDir  `json:"directories"`
	Mounts      []PlannedMount  `json:"mounts"`
	Space       []TargetSpace   `json:"space"`
	// DatabaseServices hold database files: copied only with the stack
	// stopped and verified by starting them first.
	DatabaseServices         []string `json:"database_services"`
	TotalBytes               int64    `json:"total_bytes"`
	EstimatedDowntimeSeconds int      `json:"estimated_downtime_seconds"`
	Notes                    []string `json:"notes"`
}

// MigrationRequest is the planner input: everything positional is
// passed in, so planning stays a pure function of observed state.
type MigrationRequest struct {
	Manifest    Manifest
	Containers  []docker.Container
	Mountpoints map[storage.Class]string
	// FreeBytes is free space per mountpoint (live discovery).
	FreeBytes map[string]uint64
	// SizeOf measures a directory; nil measures the real tree.
	SizeOf func(path string) (int64, error)
	// DestNonEmpty reports a destination directory already holding
	// data; nil reads the real file system.
	DestNonEmpty func(path string) (bool, error)
}

// DirSize sums regular file sizes under root (apparent size).
func DirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// dirHasEntries reports whether a directory holds any non-directory
// entry. Empty directories (provisioned nested mountpoint stubs, or
// leftovers of a rolled-back attempt) do not count as data.
func dirHasEntries(path string) (bool, error) {
	found := false
	err := filepath.WalkDir(path, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return found, err
}

// PlanMigration builds the migration plan for one package, refusing
// outright when the app is not in the needs-migration state, a target
// already holds data, two volumes would share a destination, or a
// target mount lacks the space (plus margin).
func PlanMigration(req MigrationRequest) (MigrationPlan, error) {
	if err := req.Manifest.Validate(); err != nil {
		return MigrationPlan{}, err
	}
	sizeOf := req.SizeOf
	if sizeOf == nil {
		sizeOf = DirSize
	}
	nonEmpty := req.DestNonEmpty
	if nonEmpty == nil {
		nonEmpty = dirHasEntries
	}
	status := DetectData(req.Manifest, req.Containers, req.Mountpoints)
	switch status.State {
	case DataPlaced:
		return MigrationPlan{}, fmt.Errorf("catalog: %q already keeps its data on mounted storage; nothing to migrate", req.Manifest.Name)
	case DataUnknown:
		return MigrationPlan{}, fmt.Errorf("catalog: %q cannot be migrated automatically: %s", req.Manifest.Name, status.Reason)
	}
	mounts, dirs, err := ResolveData(req.Manifest, req.Mountpoints)
	if err != nil {
		return MigrationPlan{}, err
	}
	plan := MigrationPlan{Package: req.Manifest.Name, Mounts: mounts, Directories: dirs}
	declared := map[string]DataPath{}
	for _, service := range req.Manifest.Services {
		for _, data := range service.Data {
			declared[service.Name+"\x00"+data.Target] = data
		}
	}
	services := map[string]bool{}
	byDest := map[string]int{}
	oldSeen := map[string]bool{}
	var layers []string
	for _, finding := range status.Findings {
		data := declared[finding.Service+"\x00"+finding.Target]
		services[finding.Service] = true
		if finding.Volume != "" && !oldSeen[finding.Volume] {
			oldSeen[finding.Volume] = true
			size, err := sizeOf(finding.VolumeSource)
			if err != nil {
				return MigrationPlan{}, fmt.Errorf("catalog: measure volume %s: %w", finding.Volume, err)
			}
			plan.OldVolumes = append(plan.OldVolumes, OldVolume{
				Name: finding.Volume, Service: finding.Service, Target: finding.Target,
				Source: finding.VolumeSource, SizeBytes: size,
			})
		}
		if finding.State != DataNeedsMigration {
			continue
		}
		if finding.Kind == KindLayer {
			if !data.Ephemeral {
				layers = append(layers, finding.Service+" "+finding.Target)
			}
			continue
		}
		if data.Ephemeral {
			continue
		}
		source := cleanJoin(finding.VolumeSource, finding.Within)
		if existing, ok := byDest[finding.Expected]; ok {
			if plan.Items[existing].Source != source {
				return MigrationPlan{}, fmt.Errorf("catalog: %s and %s hold separate volumes for the shared path %s; merge them by hand before migrating",
					plan.Items[existing].Service, finding.Service, finding.Expected)
			}
			continue
		}
		size, err := sizeOf(source)
		if err != nil {
			// A nested path missing from its volume is simply empty.
			if !(finding.Within != "" && errors.Is(err, fs.ErrNotExist)) {
				return MigrationPlan{}, fmt.Errorf("catalog: measure %s: %w", source, err)
			}
			size = 0
		}
		byDest[finding.Expected] = len(plan.Items)
		plan.Items = append(plan.Items, MigrationItem{
			Service: finding.Service, Target: finding.Target, Volume: finding.Volume,
			Source: source, Dest: finding.Expected, Class: data.Class,
			Mountpoint: req.Mountpoints[data.Class], SizeBytes: size, Database: data.Database,
			Nested: finding.Within != "",
		})
		if data.Database {
			plan.DatabaseServices = append(plan.DatabaseServices, finding.Service)
		}
	}
	if len(plan.Items) == 0 && len(layers) == 0 {
		return MigrationPlan{}, fmt.Errorf("catalog: %q has nothing to copy", req.Manifest.Name)
	}
	assignExcludes(&plan)
	plan.DatabaseServices = uniqueSorted(plan.DatabaseServices)
	for _, item := range plan.Items {
		busy, err := nonEmpty(item.Dest)
		if err != nil {
			return MigrationPlan{}, fmt.Errorf("catalog: inspect %s: %w", item.Dest, err)
		}
		if busy {
			return MigrationPlan{}, fmt.Errorf("catalog: target %s already holds data; migrate onto an empty directory (move it aside after checking what it is)", item.Dest)
		}
		plan.TotalBytes += item.SizeBytes
	}
	if err := planSpace(&plan, req.FreeBytes); err != nil {
		return plan, err
	}
	// The cold backup copies every old volume while the stack is down,
	// so it counts toward downtime too (it is skipped only when the
	// owner acknowledged none is possible).
	var volumeBytes int64
	for _, volume := range plan.OldVolumes {
		volumeBytes += volume.SizeBytes
	}
	seconds := int((plan.TotalBytes+volumeBytes)/copyBytesPerSecond) + restartBaseSeconds + restartPerServiceSeconds*len(req.Manifest.Services)
	plan.EstimatedDowntimeSeconds = seconds
	plan.Notes = append(plan.Notes,
		fmt.Sprintf("the whole stack stops while the cold backup and the copy run (estimate %ds including the backup; throughput assumed %d MB/s)", seconds, copyBytesPerSecond>>20),
		"old volumes are kept after a successful migration; removing them is a separate action",
	)
	for _, layer := range layers {
		plan.Notes = append(plan.Notes, layer+" has no persistent mount today: its container-layer contents are not carried over (a new empty directory is mounted)")
	}
	for _, item := range plan.Items {
		if item.Database {
			plan.Notes = append(plan.Notes, item.Service+" holds database files: copied only while stopped, then started alone to verify")
		}
	}
	sort.Slice(plan.OldVolumes, func(i, j int) bool { return plan.OldVolumes[i].Name < plan.OldVolumes[j].Name })
	plan.Digest = planDigest(plan)
	return plan, nil
}

// assignExcludes makes a volume's copy skip sub-paths copied elsewhere
// and subtracts their size from the parent.
func assignExcludes(plan *MigrationPlan) {
	for i := range plan.Items {
		parent := &plan.Items[i]
		for j := range plan.Items {
			child := plan.Items[j]
			if i == j || child.Service != parent.Service || child.Volume != parent.Volume ||
				!strings.HasPrefix(child.Target, parent.Target+"/") {
				continue
			}
			relative := strings.TrimPrefix(child.Target, parent.Target+"/")
			parent.Excludes = append(parent.Excludes, relative)
			parent.SizeBytes -= child.SizeBytes
			if parent.SizeBytes < 0 {
				parent.SizeBytes = 0
			}
		}
		sort.Strings(parent.Excludes)
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// planSpace totals needs per target mount and refuses when any mount's
// free space (less margin) falls short or is unknown.
func planSpace(plan *MigrationPlan, free map[string]uint64) error {
	needed := map[string]int64{}
	for _, item := range plan.Items {
		needed[item.Mountpoint] += item.SizeBytes
	}
	var points []string
	for point := range needed {
		points = append(points, point)
	}
	sort.Strings(points)
	var short []string
	for _, point := range points {
		available, known := free[point]
		need := needed[point]
		required := need + need*spaceMarginPercent/100
		space := TargetSpace{Mountpoint: point, NeededBytes: need, FreeBytes: available, OK: known && available >= uint64(required)}
		plan.Space = append(plan.Space, space)
		switch {
		case !known:
			short = append(short, fmt.Sprintf("%s (free space unknown)", point))
		case !space.OK:
			short = append(short, fmt.Sprintf("%s needs %d bytes (with %d%% margin %d) but has %d free", point, need, spaceMarginPercent, required, available))
		}
	}
	if len(short) > 0 {
		return fmt.Errorf("%w: %s", ErrInsufficientSpace, strings.Join(short, "; "))
	}
	return nil
}

// planDigest hashes the plan's structure: what is copied where and
// which volumes are left behind. Sizes and free space are excluded —
// they move while the owner reads.
func planDigest(plan MigrationPlan) string {
	type item struct {
		Service, Target, Volume, Source, Dest string
		Excludes                              []string
	}
	payload := struct {
		Package string
		Items   []item
		Old     []string
		Mounts  []string
	}{Package: plan.Package}
	for _, entry := range plan.Items {
		payload.Items = append(payload.Items, item{entry.Service, entry.Target, entry.Volume, entry.Source, entry.Dest, entry.Excludes})
	}
	for _, volume := range plan.OldVolumes {
		payload.Old = append(payload.Old, volume.Name)
	}
	for _, mount := range plan.Mounts {
		payload.Mounts = append(payload.Mounts, mount.Service+" "+mount.Target+" "+mount.Source)
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

package catalog

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"homestead/internal/docker"
	"homestead/internal/storage"
)

// App data placement states (S277). They compare what an installed
// app's containers actually mount with what the S276 catalog
// declarations say, so they describe the running truth rather than a
// recorded intention.
const (
	// DataPlaced: every declared path is mounted where the catalog says.
	DataPlaced = "placed"
	// DataNeedsMigration: at least one declared path lives in an
	// anonymous or named Docker volume (or the container layer) and
	// nothing blocks moving it.
	DataNeedsMigration = "needs-migration"
	// DataUnknown: placement cannot be established or something blocks
	// an automatic move (no containers, a bind mount elsewhere, a
	// volume the catalog does not declare, an unassigned class).
	DataUnknown = "unknown"
)

// Finding kinds for one declared path.
const (
	// KindBind: a host bind mount at the declared target.
	KindBind = "bind"
	// KindTmpfs: a memory mount at the declared target.
	KindTmpfs = "tmpfs"
	// KindVolume: a Docker volume mounted exactly at the target.
	KindVolume = "volume"
	// KindNestedVolume: no mount at the target; the path lives inside a
	// volume mounted at an ancestor target (Nextcloud's data directory
	// inside the html volume).
	KindNestedVolume = "nested-volume"
	// KindLayer: nothing mounted covers the path, so its contents sit in
	// the container's writable layer (lost on any recreation).
	KindLayer = "layer"
)

// DataFinding is one declared path's observed placement.
type DataFinding struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	State   string `json:"state"`
	Kind    string `json:"kind"`
	// Expected is the host path the declaration resolves to (empty for
	// ephemeral paths or an unassigned class).
	Expected string `json:"expected,omitempty"`
	// Volume names the Docker volume holding the data (volume kinds).
	Volume string `json:"volume,omitempty"`
	// VolumeSource is the host directory of that volume's data.
	VolumeSource string `json:"volume_source,omitempty"`
	// Within is the sub-path inside the volume (nested-volume kind).
	Within string `json:"within,omitempty"`
	// Ephemeral marks declared tmpfs paths: nothing is copied for them.
	Ephemeral bool   `json:"ephemeral,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// AppDataStatus is one installed app's placement verdict.
type AppDataStatus struct {
	Package  string        `json:"package"`
	State    string        `json:"state"`
	Reason   string        `json:"reason,omitempty"`
	Findings []DataFinding `json:"findings"`
}

// ProjectContainers filters engine containers to one Compose project.
func ProjectContainers(all []docker.Container, project string) []docker.Container {
	var out []docker.Container
	for _, container := range all {
		if container.Labels[docker.LabelProject] == project {
			out = append(out, container)
		}
	}
	return out
}

// DetectData compares one installed package's containers with the
// manifest's data declarations. mountpoints resolves classes to the
// assigned mounts (a missing class makes the affected paths unknown).
// A package with no containers at all reads as unknown: there is
// nothing to inspect, so claiming placement would guess.
func DetectData(manifest Manifest, containers []docker.Container, mountpoints map[storage.Class]string) AppDataStatus {
	status := AppDataStatus{Package: manifest.Name, Findings: []DataFinding{}}
	if len(containers) == 0 {
		status.State = DataUnknown
		status.Reason = "no containers found for this stack; start it so its mounts can be inspected"
		return status
	}
	byService := map[string]docker.Container{}
	for _, container := range containers {
		service := container.Labels[docker.LabelService]
		if _, ok := byService[service]; !ok && service != "" {
			byService[service] = container
		}
	}
	expected := map[string]string{}
	if mounts, _, err := ResolveData(manifest, mountpoints); err == nil {
		for _, mount := range mounts {
			expected[mount.Service+"\x00"+mount.Target] = mount.Source
		}
	}
	for _, service := range manifest.Services {
		container, ok := byService[service.Name]
		if !ok {
			if len(service.Data) > 0 {
				status.Findings = append(status.Findings, DataFinding{
					Service: service.Name, Target: "", State: DataUnknown, Kind: "",
					Reason: "service has no container; start the stack so its mounts can be inspected",
				})
			}
			continue
		}
		declared := map[string]bool{}
		for _, data := range service.Data {
			declared[data.Target] = true
		}
		for _, data := range service.Data {
			finding := classify(service.Name, data, container.Mounts, expected[service.Name+"\x00"+data.Target])
			if !data.Ephemeral && finding.Expected == "" && finding.State != DataUnknown {
				finding.State = DataUnknown
				finding.Reason = fmt.Sprintf("storage class %s has no assigned mount, so the declared placement cannot be resolved", data.Class)
			}
			status.Findings = append(status.Findings, finding)
		}
		for _, mount := range container.Mounts {
			if mount.Type != "volume" || declared[mount.Destination] || coveredBy(mount.Destination, declared) {
				continue
			}
			status.Findings = append(status.Findings, DataFinding{
				Service: service.Name, Target: mount.Destination, State: DataUnknown, Kind: KindVolume,
				Volume: mount.Name, VolumeSource: mount.Source,
				Reason: "volume at a path the catalog does not declare; migrating would orphan it",
			})
		}
	}
	sort.SliceStable(status.Findings, func(i, j int) bool {
		if status.Findings[i].Service != status.Findings[j].Service {
			return status.Findings[i].Service < status.Findings[j].Service
		}
		return status.Findings[i].Target < status.Findings[j].Target
	})
	status.State = rollup(status.Findings)
	if status.State == DataUnknown {
		for _, finding := range status.Findings {
			if finding.State == DataUnknown {
				status.Reason = finding.Service + " " + finding.Target + ": " + finding.Reason
				break
			}
		}
	}
	return status
}

// coveredBy reports a volume mounted above declared targets: it holds
// them as nested paths, so the declarations account for it.
func coveredBy(mountDestination string, declared map[string]bool) bool {
	for target := range declared {
		if strings.HasPrefix(target, mountDestination+"/") {
			return true
		}
	}
	return false
}

// rollup collapses findings: any unknown blocks, else any
// needs-migration, else placed.
func rollup(findings []DataFinding) string {
	state := DataPlaced
	for _, finding := range findings {
		switch finding.State {
		case DataUnknown:
			return DataUnknown
		case DataNeedsMigration:
			state = DataNeedsMigration
		}
	}
	return state
}

// classify observes one declared path against its container's mounts.
func classify(service string, data DataPath, mounts []docker.Mount, expected string) DataFinding {
	finding := DataFinding{Service: service, Target: data.Target, Expected: expected, Ephemeral: data.Ephemeral}
	for _, mount := range mounts {
		if mount.Destination != data.Target {
			continue
		}
		switch mount.Type {
		case "bind":
			finding.Kind = KindBind
			switch {
			case data.Ephemeral:
				finding.State = DataUnknown
				finding.Reason = "bind mount at a path the catalog declares ephemeral"
			case expected != "" && filepath.Clean(mount.Source) == filepath.Clean(expected):
				finding.State = DataPlaced
			default:
				finding.State = DataUnknown
				finding.Reason = fmt.Sprintf("bind mount %s differs from the declared %s; owner-relocated data is never moved automatically", mount.Source, expected)
			}
		case "tmpfs":
			finding.Kind = KindTmpfs
			if data.Ephemeral {
				finding.State = DataPlaced
			} else {
				finding.State = DataUnknown
				finding.Reason = "tmpfs mount at a path the catalog declares persistent"
			}
		case "volume":
			finding.Kind = KindVolume
			finding.State = DataNeedsMigration
			finding.Volume = mount.Name
			finding.VolumeSource = mount.Source
		default:
			finding.Kind = mount.Type
			finding.State = DataUnknown
			finding.Reason = "unsupported mount type " + mount.Type
		}
		return finding
	}
	// No exact mount: look for the nearest ancestor mount.
	var parent *docker.Mount
	for i := range mounts {
		destination := mounts[i].Destination
		if strings.HasPrefix(data.Target, destination+"/") && (parent == nil || len(destination) > len(parent.Destination)) {
			parent = &mounts[i]
		}
	}
	if parent != nil {
		if parent.Type != "volume" {
			finding.Kind = KindLayer
			finding.State = DataUnknown
			finding.Reason = fmt.Sprintf("path lies inside %s mount %s, which is not a Docker volume", parent.Type, parent.Destination)
			return finding
		}
		finding.Kind = KindNestedVolume
		finding.State = DataNeedsMigration
		finding.Volume = parent.Name
		finding.VolumeSource = parent.Source
		finding.Within = strings.TrimPrefix(data.Target, parent.Destination+"/")
		return finding
	}
	finding.Kind = KindLayer
	finding.State = DataNeedsMigration
	return finding
}

// NeedsMigration lists the apps whose state asks for migration.
func NeedsMigration(statuses []AppDataStatus) []string {
	var out []string
	for _, status := range statuses {
		if status.State == DataNeedsMigration {
			out = append(out, status.Package)
		}
	}
	return out
}

// cleanJoin joins a host directory and a relative sub-path.
func cleanJoin(base, within string) string {
	if within == "" {
		return base
	}
	return path.Join(base, within)
}

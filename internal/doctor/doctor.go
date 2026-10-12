// Package doctor aggregates host discovery into explainable diagnostics
// (S044). Each check reports a stable name, a status, and a detail string
// saying what was found and what to do about it. Thresholds below are S044
// baselines, documented in docs/DEVELOPMENT.md — later slices tune them
// with real fleet experience, never silently.
package doctor

import (
	"fmt"
	"os"
	"strings"

	"homestead/internal/discover"
	"homestead/internal/layout"
)

// Statuses, ordered by severity.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Baselines: fail below FailMemory/RootFree, warn below WarnMemory/RootFree
// or under WarnCPUs cores.
const (
	FailMemoryBytes int64 = 1 << 30
	WarnMemoryBytes int64 = 2 << 30
	WarnCPUs              = 2
	FailRootBytes   int64 = 5 << 30
	WarnRootBytes   int64 = 10 << 30
)

// Check is one diagnostic with an explanation.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report is a full diagnostic run.
type Report struct {
	Overall string  `json:"overall"`
	Checks  []Check `json:"checks"`
}

// Run executes every diagnostic against the live host. Discovery failures
// become a failing check, never a crash: the doctor always reports.
func Run() Report {
	osInfo, osErr := discover.DiscoverOS()
	hardware, hwErr := discover.DiscoverHardware()
	network, netErr := discover.DiscoverNetwork()
	storage, storageErr := discover.DiscoverStorage()

	checks := []Check{}
	if osErr != nil {
		return Report{Overall: StatusFail, Checks: []Check{{
			Name:   "discovery",
			Status: StatusFail,
			Detail: fmt.Sprintf("host discovery failed: %v", osErr),
		}}}
	}
	checks = append(checks, checkDistro(osInfo))
	checks = append(checks, checkMemory(osInfo))
	if hwErr != nil {
		checks = append(checks, Check{Name: "hardware", Status: StatusFail,
			Detail: fmt.Sprintf("hardware discovery failed: %v", hwErr)})
	} else {
		checks = append(checks, checkCPU(osInfo, hardware))
	}
	if netErr != nil {
		checks = append(checks, Check{Name: "network", Status: StatusFail,
			Detail: fmt.Sprintf("network discovery failed: %v", netErr)})
	} else {
		checks = append(checks, checkGateway(network))
		checks = append(checks, checkResolvers(network))
	}
	if storageErr != nil {
		checks = append(checks, Check{Name: "storage", Status: StatusFail,
			Detail: fmt.Sprintf("storage discovery failed: %v", storageErr)})
	} else {
		checks = append(checks, checkRootDisk(storage))
		checks = append(checks, checkStaleMounts(storage))
	}
	checks = append(checks, checkStateDirs())
	return Report{Overall: overall(checks), Checks: checks}
}

// Add appends checks and recomputes the overall verdict.
func (r *Report) Add(checks ...Check) {
	r.Checks = append(r.Checks, checks...)
	r.Overall = overall(r.Checks)
}

func overall(checks []Check) string {
	result := StatusOK
	for _, check := range checks {
		if check.Status == StatusFail {
			return StatusFail
		}
		if check.Status == StatusWarn {
			result = StatusWarn
		}
	}
	return result
}

func checkDistro(osInfo discover.OS) Check {
	name := fmt.Sprintf("%s %s (id=%s)", osInfo.DistroName, osInfo.DistroVersion, osInfo.DistroID)
	switch osInfo.DistroID {
	case "debian", "ubuntu":
		return Check{Name: "os-distro", Status: StatusOK, Detail: name}
	default:
		return Check{Name: "os-distro", Status: StatusFail,
			Detail: fmt.Sprintf("%s is not Debian or Ubuntu; Homestead targets Debian/Ubuntu Server", name)}
	}
}

func checkMemory(osInfo discover.OS) Check {
	detail := fmt.Sprintf("%d MiB total", osInfo.MemoryBytes>>20)
	switch {
	case int64(osInfo.MemoryBytes) < FailMemoryBytes:
		return Check{Name: "memory", Status: StatusFail, Detail: detail + " (below 1 GiB floor)"}
	case int64(osInfo.MemoryBytes) < WarnMemoryBytes:
		return Check{Name: "memory", Status: StatusWarn, Detail: detail + " (below 2 GiB comfort)"}
	default:
		return Check{Name: "memory", Status: StatusOK, Detail: detail}
	}
}

func checkCPU(osInfo discover.OS, hardware discover.Hardware) Check {
	detail := fmt.Sprintf("%d cores (%s), %d GPUs", osInfo.CPUCount, osInfo.CPUModel, len(hardware.GPUs))
	if osInfo.CPUCount < WarnCPUs {
		return Check{Name: "cpu", Status: StatusWarn, Detail: detail + " (fewer than 2 cores will feel slow)"}
	}
	return Check{Name: "cpu", Status: StatusOK, Detail: detail}
}

func checkGateway(network discover.Network) Check {
	if network.DefaultGateway == "" {
		return Check{Name: "default-gateway", Status: StatusWarn,
			Detail: "no default route; local-only operation (add a gateway for updates and remote access)"}
	}
	return Check{Name: "default-gateway", Status: StatusOK,
		Detail: fmt.Sprintf("%s via %s", network.DefaultGateway, network.DefaultInterface)}
}

func checkResolvers(network discover.Network) Check {
	if len(network.Resolvers) == 0 {
		return Check{Name: "dns-resolvers", Status: StatusWarn,
			Detail: "no resolvers configured; name resolution for updates and catalog will fail"}
	}
	return Check{Name: "dns-resolvers", Status: StatusOK,
		Detail: fmt.Sprintf("%d configured (%s)", len(network.Resolvers), network.Resolvers[0])}
}

func checkRootDisk(storage discover.Storage) Check {
	root, ok := storage.FindMount("/")
	if !ok {
		return Check{Name: "root-disk", Status: StatusFail, Detail: "no / mount discovered"}
	}
	if !root.Accessible {
		return Check{Name: "root-disk", Status: StatusFail, Detail: "/ is not accessible"}
	}
	detail := fmt.Sprintf("%d GiB available on /", root.AvailBytes>>30)
	switch {
	case int64(root.AvailBytes) < FailRootBytes:
		return Check{Name: "root-disk", Status: StatusFail, Detail: detail + " (below 5 GiB floor)"}
	case int64(root.AvailBytes) < WarnRootBytes:
		return Check{Name: "root-disk", Status: StatusWarn, Detail: detail + " (below 10 GiB comfort)"}
	default:
		return Check{Name: "root-disk", Status: StatusOK, Detail: detail}
	}
}

func checkStaleMounts(storage discover.Storage) Check {
	var stale []string
	for _, mount := range storage.Mounts {
		if mount.Accessible || !isStorageFS(mount.FSType) {
			continue
		}
		stale = append(stale, mount.Mountpoint)
	}
	if len(stale) > 0 {
		shown := stale
		suffix := ""
		if len(stale) > 10 {
			shown = stale[:10]
			suffix = fmt.Sprintf(", and %d more", len(stale)-10)
		}
		return Check{Name: "stale-mounts", Status: StatusWarn,
			Detail: fmt.Sprintf("%d inaccessible: %s%s", len(stale), strings.Join(shown, ", "), suffix)}
	}
	return Check{Name: "stale-mounts", Status: StatusOK,
		Detail: fmt.Sprintf("%d mounts accessible", len(storage.Mounts))}
}

// isStorageFS reports filesystems whose inaccessibility means broken
// storage. Container plumbing (namespaces, cgroup, overlay layers,
// tmpfs mounts) is noise by design: it fails to stat from outside its
// namespace on any docker host, healthy or not.
func isStorageFS(fstype string) bool {
	switch fstype {
	case "ext4", "xfs", "btrfs", "zfs", "nfs", "nfs4", "cifs",
		"fuseblk", "exfat", "vfat", "ntfs", "fuse.sshfs":
		return true
	default:
		return false
	}
}

// stateDirs are the persistent layout directories. The runtime dir is
// service-managed on tmpfs and excluded: absence outside service runtime
// is normal, not a failure.
func stateDirs() []string {
	return []string{
		layout.EtcDir.Path,
		layout.LibDir.Path,
		layout.LogDir.Path,
		layout.StacksDir.Path,
	}
}

func checkStateDirs() Check {
	var missing []string
	for _, dir := range stateDirs() {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			missing = append(missing, dir)
		}
	}
	if len(missing) > 0 {
		return Check{Name: "state-dirs", Status: StatusFail,
			Detail: fmt.Sprintf("missing %s; provision with the installer (sudo make install)", strings.Join(missing, ", "))}
	}
	return Check{Name: "state-dirs", Status: StatusOK, Detail: "layout directories present"}
}

package catalog

import (
	"fmt"
	"strings"

	"homestead/internal/storage"
)

// HostFacts is the evaluated host: platform architecture, memory and
// CPU capacity, present GPUs by name, free bytes per assigned storage
// class, and bound host ports. Later slices wire discovery (hardware,
// storage health, socket tables) into these facts; the engine stays
// pure so prerequisites evaluate identically in tests and plans.
type HostFacts struct {
	Arch      string
	MemoryMB  uint64
	CPUCount  int
	GPUs      []string
	FreeSpace map[storage.Class]uint64
	UsedPorts map[int]bool
}

// RequirementCheck is one prerequisite verdict with the evidence
// attached: what was needed, what the host offers, and whether they
// meet.
type RequirementCheck struct {
	Name   string
	OK     bool
	Detail string
}

// RequirementReport grades a manifest against host facts: ordered
// checks from platform to ports, passing only when every one holds.
// Missing data fails its check — unevaluated prerequisites never read
// as satisfied.
type RequirementReport struct {
	Package string
	Checks  []RequirementCheck
	Passed  bool
}

// platformArchitectures bounds supported appliance platforms. Guests
// beyond these never evaluate further: Homestead ships these targets,
// and anything else is a port, not a plan.
var platformArchitectures = map[string]bool{"amd64": true, "arm64": true}

// Evaluate grades manifest requirements against host facts without
// touching the host: architecture, CPU, memory, GPU, per-class
// storage floors, and port conflicts, each reported with both sides
// of the comparison.
func Evaluate(manifest Manifest, host HostFacts) (RequirementReport, error) {
	if err := manifest.Validate(); err != nil {
		return RequirementReport{}, err
	}
	report := RequirementReport{Package: manifest.Name}
	report.Checks = append(report.Checks, checkArch(manifest, host))
	report.Checks = append(report.Checks, checkCPU(manifest, host))
	report.Checks = append(report.Checks, checkMemory(manifest, host))
	report.Checks = append(report.Checks, checkGPU(manifest, host))
	report.Checks = append(report.Checks, checkStorage(manifest, host)...)
	report.Checks = append(report.Checks, checkPorts(manifest, host)...)
	report.Passed = true
	for _, check := range report.Checks {
		if !check.OK {
			report.Passed = false
			break
		}
	}
	return report, nil
}

func checkArch(manifest Manifest, host HostFacts) RequirementCheck {
	check := RequirementCheck{Name: "architecture"}
	if !platformArchitectures[host.Arch] {
		check.Detail = fmt.Sprintf("host architecture %q is not a supported platform", host.Arch)
		return check
	}
	for _, arch := range manifest.Requirements.Architectures {
		if arch == host.Arch {
			check.OK = true
			check.Detail = fmt.Sprintf("package supports %s", host.Arch)
			return check
		}
	}
	if len(manifest.Requirements.Architectures) > 0 {
		check.Detail = fmt.Sprintf("package supports %s, host runs %s",
			strings.Join(manifest.Requirements.Architectures, ", "), host.Arch)
		return check
	}
	check.OK = true
	check.Detail = fmt.Sprintf("no architecture floor; host runs supported %s", host.Arch)
	return check
}

func checkCPU(manifest Manifest, host HostFacts) RequirementCheck {
	check := RequirementCheck{Name: "cpu"}
	need := manifest.Requirements.CPUCount
	if need == 0 {
		check.OK = true
		check.Detail = "no CPU floor declared"
		return check
	}
	if host.CPUCount >= need {
		check.OK = true
		check.Detail = fmt.Sprintf("host offers %d cores for %d required", host.CPUCount, need)
		return check
	}
	check.Detail = fmt.Sprintf("host offers %d cores for %d required", host.CPUCount, need)
	return check
}

func checkMemory(manifest Manifest, host HostFacts) RequirementCheck {
	check := RequirementCheck{Name: "memory"}
	need := manifest.Requirements.MemoryMB
	if host.MemoryMB >= need {
		check.OK = true
		check.Detail = fmt.Sprintf("host offers %d MB for %d required", host.MemoryMB, need)
		return check
	}
	check.Detail = fmt.Sprintf("host offers %d MB for %d required", host.MemoryMB, need)
	return check
}

func checkGPU(manifest Manifest, host HostFacts) RequirementCheck {
	check := RequirementCheck{Name: "gpu"}
	if !manifest.Requirements.NeedsGPU {
		check.OK = true
		check.Detail = "no GPU required"
		return check
	}
	if len(host.GPUs) > 0 {
		check.OK = true
		check.Detail = fmt.Sprintf("host offers %s", strings.Join(host.GPUs, ", "))
		return check
	}
	check.Detail = "package needs a GPU the host lacks"
	return check
}

func checkStorage(manifest Manifest, host HostFacts) []RequirementCheck {
	var out []RequirementCheck
	for _, need := range manifest.Requirements.Storage {
		check := RequirementCheck{Name: "storage:" + string(need.Class)}
		free, known := host.FreeSpace[need.Class]
		if !known {
			check.Detail = fmt.Sprintf("no space data for %s storage", need.Class)
			out = append(out, check)
			continue
		}
		if free >= need.MinBytes {
			check.OK = true
			check.Detail = fmt.Sprintf("%s offers %d bytes for %d required", need.Class, free, need.MinBytes)
		} else {
			check.Detail = fmt.Sprintf("%s offers %d bytes for %d required", need.Class, free, need.MinBytes)
		}
		out = append(out, check)
	}
	return out
}

func checkPorts(manifest Manifest, host HostFacts) []RequirementCheck {
	var out []RequirementCheck
	for _, port := range manifest.Requirements.Ports {
		check := RequirementCheck{Name: fmt.Sprintf("port:%d", port)}
		if host.UsedPorts[port] {
			check.Detail = fmt.Sprintf("port %d already bound on the host", port)
		} else {
			check.OK = true
			check.Detail = fmt.Sprintf("port %d free", port)
		}
		out = append(out, check)
	}
	return out
}

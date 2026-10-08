package catalog

import (
	"fmt"
	"strings"
)

// RecommendedSuite is the curated starting bundle in install order:
// media first (the vertical slice proven end to end), then files,
// then photos. Order matters for owners following along — later
// entries assume nothing about earlier ones technically, but the
// narrative reads front to back.
var RecommendedSuite = []string{"jellyfin", "nextcloud", "immich"}

// LoadSuite resolves the recommended bundle from a loaded catalog:
// every member present and valid, in curated order. Curators add
// packages here deliberately — never by directory listing — so the
// recommended set stays a decision, not an accident of files.
func LoadSuite(cat Catalog) ([]Manifest, error) {
	var out []Manifest
	for _, name := range RecommendedSuite {
		manifest, err := cat.Get(name)
		if err != nil {
			return nil, fmt.Errorf("catalog: suite missing %q: %w", name, err)
		}
		if err := manifest.Validate(); err != nil {
			return nil, fmt.Errorf("catalog: suite member %q: %w", name, err)
		}
		out = append(out, manifest)
	}
	return out, nil
}

// SuiteReport grades the whole bundle on one host: per-package
// verdicts plus the two properties only visible jointly — combined
// memory footprint against host capacity, and host-port conflicts
// across members. Individually valid manifests can still collide,
// which is exactly what this unit exists to catch.
type SuiteReport struct {
	Packages    []RequirementReport
	TotalMemory uint64
	HostMemory  uint64
	Passed      bool
	Detail      string
}

// CheckSuite evaluates every member and the joint properties: each
// package must pass alone, host ports must stay unique across the
// bundle, and summed memory must fit. Storage combines differently —
// per-class floors evaluate per package against shared mounts — so
// totals stay memory-only by design.
func CheckSuite(manifests []Manifest, host HostFacts) (SuiteReport, error) {
	report := SuiteReport{HostMemory: host.MemoryMB}
	seenPorts := map[int]string{}
	for _, manifest := range manifests {
		graded, err := Evaluate(manifest, host)
		if err != nil {
			return SuiteReport{}, err
		}
		report.Packages = append(report.Packages, graded)
		report.TotalMemory += manifest.Requirements.MemoryMB
		for _, port := range manifest.Requirements.Ports {
			if owner, taken := seenPorts[port]; taken {
				return SuiteReport{}, fmt.Errorf(
					"catalog: port %d claimed by both %q and %q", port, owner, manifest.Name)
			}
			seenPorts[port] = manifest.Name
		}
	}
	var failures []string
	for _, graded := range report.Packages {
		if !graded.Passed {
			failures = append(failures, graded.Package)
		}
	}
	if report.TotalMemory > host.MemoryMB {
		failures = append(failures, fmt.Sprintf("combined memory %d MB exceeds host %d MB",
			report.TotalMemory, host.MemoryMB))
	}
	if len(failures) > 0 {
		report.Detail = "suite blocked: " + strings.Join(failures, "; ")
		return report, nil
	}
	report.Passed = true
	report.Detail = fmt.Sprintf("suite fits: %d packages, %d MB of %d MB",
		len(manifests), report.TotalMemory, host.MemoryMB)
	return report, nil
}

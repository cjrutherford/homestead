package exposure

import (
	"fmt"
	"regexp"

	"homestead/internal/docker"
)

var validPackageName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// rank orders exposures from narrowest to widest so ceilings compare.
// Overlay sits above local (leaves the LAN) and below public (stays
// off the internet).
func rank(exposure docker.Exposure) int {
	switch exposure {
	case docker.ExposureLocal:
		return 0
	case docker.ExposureOverlay:
		return 1
	case docker.ExposurePublic:
		return 2
	default:
		return -1
	}
}

// EndpointExposure is one catalog endpoint's declared posture: the
// recommended origin class, whether owners may widen beyond it, and
// package-authored cautions shown at plan time. A closed ceiling
// (AllowWider false) marks endpoints that must never face wider
// origins — unauthenticated admin surfaces, raw data ports — and later
// slices refuse widening them no matter who asks.
type EndpointExposure struct {
	Endpoint    string
	Recommended docker.Exposure
	AllowWider  bool
	Warnings    []string
}

// Validate checks the declaration shape.
func (e EndpointExposure) Validate() error {
	if len(e.Endpoint) == 0 || len(e.Endpoint) > 64 {
		return fmt.Errorf("exposure: endpoint must be 1-64 characters")
	}
	for _, r := range e.Endpoint {
		if r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("exposure: invalid endpoint %q", e.Endpoint)
		}
	}
	if rank(e.Recommended) < 0 {
		return fmt.Errorf("exposure: recommended must be local, overlay, or public")
	}
	for i, warning := range e.Warnings {
		if len(warning) == 0 || len(warning) > 500 {
			return fmt.Errorf("exposure: warning #%d must be 1-500 characters", i)
		}
	}
	return nil
}

// Allows reports whether an exposure stays within the declaration:
// at or below the recommendation always, wider only when opened.
func (e EndpointExposure) Allows(exposure docker.Exposure) bool {
	if rank(exposure) < 0 {
		return false
	}
	if rank(exposure) <= rank(e.Recommended) {
		return true
	}
	return e.AllowWider
}

// PackageExposure is a catalog package's full exposure declaration:
// which endpoints serve what, how wide each may go, and what their
// authors want owners to know first. Catalog install (phase 14) binds
// endpoints to routes; until then this declares, never applies.
type PackageExposure struct {
	Package   string
	Endpoints []EndpointExposure
}

// Validate checks the declaration: package naming plus unique,
// well-formed endpoints.
func (p PackageExposure) Validate() error {
	if !validPackageName.MatchString(p.Package) {
		return fmt.Errorf("exposure: package must match [a-z][a-z0-9-]*")
	}
	if len(p.Endpoints) == 0 {
		return fmt.Errorf("exposure: package %q declares no endpoints", p.Package)
	}
	seen := map[string]bool{}
	for i := range p.Endpoints {
		if err := p.Endpoints[i].Validate(); err != nil {
			return fmt.Errorf("endpoint #%d: %w", i, err)
		}
		if seen[p.Endpoints[i].Endpoint] {
			return fmt.Errorf("duplicate endpoint %q", p.Endpoints[i].Endpoint)
		}
		seen[p.Endpoints[i].Endpoint] = true
	}
	return nil
}

// Find returns the endpoint declaration, if any.
func (p PackageExposure) Find(endpoint string) (EndpointExposure, bool) {
	for _, declared := range p.Endpoints {
		if declared.Endpoint == endpoint {
			return declared, true
		}
	}
	return EndpointExposure{}, false
}

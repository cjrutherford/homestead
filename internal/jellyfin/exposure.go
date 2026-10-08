package jellyfin

import (
	"fmt"

	"homestead/internal/docker"
	"homestead/internal/exposure"
)

// Decision is the enforced media posture: the route with selected
// exposure and optional policy, the engine assessment of that state,
// displayed recommendations, and the override when one carried the
// widening. Callers reconcile the route; readers review the rest.
type Decision struct {
	Route           docker.Route
	Assessment      docker.Assessment
	Recommendations []exposure.Recommendation
	Override        *exposure.Override
}

// Decide selects and enforces route exposure: the owner names an
// origin class, metadata ceilings hold unless a matching live
// override names the same widening, private names never go public,
// and an optional policy narrows the proxy. Assessment and
// recommendations ride along so enforcement never travels without
// its reasoning. Stale overrides for other targets ignore quietly —
// they constrain nothing about this decision either way.
func Decide(route docker.Route, want docker.Exposure, endpoint exposure.EndpointExposure, policy *docker.Policy, override *exposure.Override) (Decision, error) {
	if err := route.Validate(); err != nil {
		return Decision{}, fmt.Errorf("jellyfin: %w", err)
	}
	if want != docker.ExposureLocal && want != docker.ExposureOverlay && want != docker.ExposurePublic {
		return Decision{}, fmt.Errorf("jellyfin: exposure must be local, overlay, or public")
	}
	if err := endpoint.Validate(); err != nil {
		return Decision{}, err
	}
	if want == docker.ExposurePublic && docker.IsPrivateName(route.Host) {
		return Decision{}, fmt.Errorf("jellyfin: %q is private and cannot go public", route.Host)
	}
	var applied *exposure.Override
	if !endpoint.Allows(want) {
		if override == nil || override.To != want || override.Host != route.Host {
			return Decision{}, fmt.Errorf("jellyfin: %s exceeds the package ceiling; record an override first", want)
		}
		applied = override
	}
	decided := route
	decided.Exposure = want
	decided.Policy = policy
	if err := decided.Validate(); err != nil {
		return Decision{}, fmt.Errorf("jellyfin: decided route: %w", err)
	}
	assessment, err := docker.Assess(decided)
	if err != nil {
		return Decision{}, err
	}
	recommendations, err := exposure.Recommend(decided, &endpoint, nil)
	if err != nil {
		return Decision{}, err
	}
	return Decision{
		Route: decided, Assessment: assessment,
		Recommendations: recommendations, Override: applied,
	}, nil
}

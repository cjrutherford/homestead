package catalog

import (
	"fmt"

	"homestead/internal/docker"
	"homestead/internal/exposure"
)

// RouteRecommendation is one endpoint's route default: which service
// answers, the recommended origin class, the TLS mode for that class,
// and the package warnings to show alongside. Hostnames and upstream
// ports arrive later with owner input (S155) — recommendations carry
// policy, never guesses about names.
type RouteRecommendation struct {
	Endpoint    string
	Service     string
	Recommended docker.Exposure
	TLS         docker.TLSMode
	Warnings    []string
}

// tlsHint grades TLS by exposure without a hostname: local and overlay
// terminate internally, public automates. Plain HTTP never recommends
// (S101), so hints stay two-valued by construction.
func tlsHint(recommended docker.Exposure) docker.TLSMode {
	if recommended == docker.ExposurePublic {
		return docker.TLSAuto
	}
	return docker.TLSInternal
}

// RecommendRoutes materializes per-endpoint route defaults from
// catalog metadata: service linkage from the manifest, exposure and
// warnings from the package declaration, TLS hints by class. Invalid
// manifests fail instead of recommending from broken metadata.
func RecommendRoutes(manifest Manifest) ([]RouteRecommendation, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	byEndpoint := map[string]string{}
	for _, service := range manifest.Services {
		byEndpoint[service.Exposes] = service.Name
	}
	var out []RouteRecommendation
	for _, endpoint := range manifest.Exposure.Endpoints {
		service, ok := byEndpoint[endpoint.Endpoint]
		if !ok {
			return nil, fmt.Errorf("catalog: endpoint %q has no serving service", endpoint.Endpoint)
		}
		out = append(out, RouteRecommendation{
			Endpoint:    endpoint.Endpoint,
			Service:     service,
			Recommended: endpoint.Recommended,
			TLS:         tlsHint(endpoint.Recommended),
			Warnings:    append([]string(nil), endpoint.Warnings...),
		})
	}
	return out, nil
}

// BuildRoute concretizes one recommendation with owner inputs: the
// served hostname and the upstream host and port behind it. Exposure
// and TLS ride the recommendation unchanged — owners widen through
// the exposure ceremony (S123), never by editing generated routes.
func BuildRoute(recommendation RouteRecommendation, host docker.Hostname, targetHost string, targetPort int) (docker.Route, error) {
	route := docker.Route{
		Host:     host,
		Target:   docker.Target{Host: targetHost, Port: targetPort},
		Exposure: recommendation.Recommended,
		TLS:      recommendation.TLS,
	}
	if err := route.Validate(); err != nil {
		return docker.Route{}, err
	}
	return route, nil
}

// ExposureFor exposes the metadata ceiling lookup S155 plans against:
// the endpoint declaration for a service, if the manifest serves it.
func ExposureFor(manifest Manifest, service string) (exposure.EndpointExposure, error) {
	if err := manifest.Validate(); err != nil {
		return exposure.EndpointExposure{}, err
	}
	for _, declared := range manifest.Exposure.Endpoints {
		for _, svc := range manifest.Services {
			if svc.Name == service && svc.Exposes == declared.Endpoint {
				return declared, nil
			}
		}
	}
	return exposure.EndpointExposure{}, fmt.Errorf("catalog: service %q serves no endpoint", service)
}

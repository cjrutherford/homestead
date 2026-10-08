package jellyfin

import (
	"fmt"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// HealthPath probes Jellyfin through the proxy without credentials:
// the public system endpoint answers 200 for serving instances and
// stays stable across versions by design (it feeds the login screen).
const HealthPath = "/System/Info/Public"

// HealthIntervalSeconds spaces active probes at the shared default.
const HealthIntervalSeconds = 30

// DefaultHostname names the media route under a base domain:
// jellyfin.<domain>, matching the install planner's first-endpoint
// convention (S155). Owners override freely; defaults only start.
func DefaultHostname(domain string) (docker.Hostname, error) {
	host := docker.Hostname("jellyfin." + domain)
	if err := host.Validate(); err != nil {
		return "", fmt.Errorf("jellyfin: default hostname: %w", err)
	}
	return host, nil
}

// RouteFor builds Jellyfin's proxy route from catalog metadata plus
// owner inputs: the web endpoint's recommended exposure, internal TLS
// for local serving (the S154 hint rule, shared rather than
// duplicated in spirit), the public-info health probe, and the given
// upstream. Exposure widening stays in the ceremony (S123) — this
// constructor serves metadata, never overrides it.
func RouteFor(manifest catalog.Manifest, host docker.Hostname, targetHost string, targetPort int) (docker.Route, error) {
	if err := manifest.Validate(); err != nil {
		return docker.Route{}, err
	}
	declared, err := catalog.ExposureFor(manifest, "jellyfin")
	if err != nil {
		return docker.Route{}, fmt.Errorf("jellyfin: %w", err)
	}
	tls := docker.TLSInternal
	if declared.Recommended == docker.ExposurePublic {
		tls = docker.TLSAuto
	}
	route := docker.Route{
		Host:     host,
		Target:   docker.Target{Host: targetHost, Port: targetPort},
		Exposure: declared.Recommended,
		TLS:      tls,
		Health:   docker.HealthCheck{Path: HealthPath, IntervalSeconds: HealthIntervalSeconds},
	}
	if err := route.Validate(); err != nil {
		return docker.Route{}, fmt.Errorf("jellyfin: route: %w", err)
	}
	return route, nil
}

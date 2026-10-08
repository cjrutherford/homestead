package catalog

import (
	"fmt"
	"net/netip"
	"strings"

	"homestead/internal/cert"
	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/firewall"
	"homestead/internal/storage"
)

// VolumeSpec binds one service path to class storage: the class mount
// resolves the source root, subpath scopes the package directory, and
// target names the container path. Sources compute as
// <mount>/<package>/<subpath> — deterministic, inspectable, and
// provisionable by construction.
type VolumeSpec struct {
	Service  string
	Class    storage.Class
	Subpath  string
	Target   string
	ReadOnly bool
}

// InstallRequest scopes one install plan: the manifest, host facts,
// base domain for default hostnames, per-endpoint hostname overrides,
// container ports per routed service, class mountpoints, volume
// bindings, networks, and optional public DNS inputs. Everything
// positional arrives explicitly — the planner never guesses names,
// ports, or addresses.
type InstallRequest struct {
	Manifest       Manifest
	Host           HostFacts
	Domain         string
	Hostnames      map[string]string
	ContainerPorts map[string]int
	Storage        map[storage.Class]string
	Volumes        []VolumeSpec
	Networks       []NetworkAttachment
	Zone           string
	PublicIP       netip.Addr
}

// TLSNeed names the certificate strategy per host: internal or public
// automation, chosen by the shared eligibility boundary (S101).
// Full issuers build later (S106); planning records direction only.
type TLSNeed struct {
	Host   string
	Issuer cert.IssuerType
}

// ProvisionDir pairs a mountpoint with one directory to converge on it.
type ProvisionDir struct {
	Mountpoint string
	Directory  storage.Directory
}

// InstallPlan is the reviewable whole: requirements verdict,
// recommendations, routes, compose, DNS, TLS strategies, firewall
// policies, directories, and the lifecycle verbs S156 executes
// (provision then up). Sections stay desired state for this install
// only — callers merge tables before applying.
type InstallPlan struct {
	Package      string
	Requirements RequirementReport
	Routes       []docker.Route
	Compose      StackFiles
	DNS          []dns.Record
	TLS          []TLSNeed
	Firewall     []firewall.ServicePolicy
	Directories  []ProvisionDir
	Actions      []string
}

// PlanInstall produces the install change set, refusing unplannable
// installs outright: failing requirements, unknown hostnames, missing
// ports or mounts, and public routes without DNS inputs all fail
// before anything writes.
func PlanInstall(req InstallRequest) (InstallPlan, error) {
	if err := req.Manifest.Validate(); err != nil {
		return InstallPlan{}, err
	}
	if req.Domain == "" {
		return InstallPlan{}, fmt.Errorf("catalog: domain must not be empty")
	}
	report, err := Evaluate(req.Manifest, req.Host)
	if err != nil {
		return InstallPlan{}, err
	}
	if !report.Passed {
		return InstallPlan{}, fmt.Errorf("catalog: requirements unmet for %q", req.Manifest.Name)
	}
	recommendations, err := RecommendRoutes(req.Manifest)
	if err != nil {
		return InstallPlan{}, err
	}
	plan := InstallPlan{Package: req.Manifest.Name, Requirements: report}
	if err := planRoutes(&plan, req, recommendations); err != nil {
		return InstallPlan{}, err
	}
	if err := planCompose(&plan, req); err != nil {
		return InstallPlan{}, err
	}
	if err := planDNS(&plan, req); err != nil {
		return InstallPlan{}, err
	}
	planTLS(&plan)
	planFirewall(&plan)
	return plan, nil
}

// defaultHostname names endpoints deterministically: the package base
// for the first endpoint, endpoint-qualified for the rest. Owners
// override per endpoint through Hostnames.
func defaultHostname(manifest Manifest, endpoint string, domain string, first bool) string {
	if first {
		return manifest.Name + "." + domain
	}
	return endpoint + "-" + manifest.Name + "." + domain
}

// planRoutes concretizes one route per recommendation with owner
// hostnames and upstream targets.
func planRoutes(plan *InstallPlan, req InstallRequest, recommendations []RouteRecommendation) error {
	for i, recommendation := range recommendations {
		host := req.Hostnames[recommendation.Endpoint]
		if host == "" {
			host = defaultHostname(req.Manifest, recommendation.Endpoint, req.Domain, i == 0)
		}
		port, ok := req.ContainerPorts[recommendation.Service]
		if !ok {
			return fmt.Errorf("catalog: no container port for service %q", recommendation.Service)
		}
		route, err := BuildRoute(recommendation, docker.Hostname(host), recommendation.Service, port)
		if err != nil {
			return fmt.Errorf("catalog: route %q: %w", recommendation.Endpoint, err)
		}
		plan.Routes = append(plan.Routes, route)
	}
	return nil
}

// planCompose renders the stack with caller-positioned volumes
// (sources joined from class mounts), ports unpublished by default —
// local-first serving goes through the proxy, never host ports — and
// the requested networks. Volume services must exist in the manifest;
// targets validate absolute at generation.
func planCompose(plan *InstallPlan, req InstallRequest) error {
	byService := map[string][]VolumeMapping{}
	for _, volume := range req.Volumes {
		mountpoint, ok := req.Storage[volume.Class]
		if !ok {
			return fmt.Errorf("catalog: class %s has no mountpoint", volume.Class)
		}
		if err := validateSubpath(volume.Subpath); err != nil {
			return err
		}
		if volume.Target == "" || volume.Target[0] != '/' {
			return fmt.Errorf("catalog: volume target %q must be absolute", volume.Target)
		}
		byService[volume.Service] = append(byService[volume.Service], VolumeMapping{
			Source:   mountpoint + "/" + req.Manifest.Name + "/" + volume.Subpath,
			Target:   volume.Target,
			ReadOnly: volume.ReadOnly,
		})
	}
	var specs []ServiceGeneration
	for _, service := range req.Manifest.Services {
		specs = append(specs, ServiceGeneration{Service: service.Name, Volumes: byService[service.Name]})
	}
	files, err := Generate(req.Manifest, Generation{Networks: req.Networks, Services: specs})
	if err != nil {
		return err
	}
	plan.Compose = files
	for _, volume := range req.Volumes {
		plan.Directories = append(plan.Directories, ProvisionDir{
			Mountpoint: req.Storage[volume.Class],
			Directory: storage.Directory{
				Path: req.Manifest.Name + "/" + volume.Subpath,
				Mode: 0o750, Uid: storage.UnsetID, Gid: storage.UnsetID,
			},
		})
	}
	plan.Actions = append(plan.Actions, "provision", "up")
	return nil
}

// validateSubpath checks volume subpaths: relative and contained,
// mirroring provisioning without touching stores.
func validateSubpath(subpath string) error {
	if subpath == "" || subpath[0] == '/' || subpath == "." ||
		strings.HasPrefix(subpath, "../") || strings.Contains(subpath, "/../") {
		return fmt.Errorf("catalog: subpath %q must be relative and contained", subpath)
	}
	return nil
}

// planDNS records public address records under the zone origin for
// public routes only; anything less needs no DNS work. The zone and
// address ride the request because only live inputs know them, and
// public routes without them refuse instead of guessing records.
func planDNS(plan *InstallPlan, req InstallRequest) error {
	var public []docker.Route
	for _, route := range plan.Routes {
		if route.Exposure == docker.ExposurePublic {
			public = append(public, route)
		}
	}
	if len(public) == 0 {
		return nil
	}
	if req.Zone == "" || !req.PublicIP.IsValid() || req.PublicIP.IsUnspecified() {
		return fmt.Errorf("catalog: public routes need zone and public address")
	}
	for _, route := range public {
		host := string(route.Host)
		var relative string
		switch {
		case host == req.Zone:
			relative = "@"
		case strings.HasSuffix(host, "."+req.Zone):
			relative = strings.TrimSuffix(host, "."+req.Zone)
		default:
			return fmt.Errorf("catalog: %q is outside zone %q", host, req.Zone)
		}
		typ := dns.RecordA
		if req.PublicIP.Is6() {
			typ = dns.RecordAAAA
		}
		record := dns.Record{Name: relative, Type: typ, Value: req.PublicIP.String()}
		if err := record.Validate(); err != nil {
			return fmt.Errorf("catalog: dns: %w", err)
		}
		plan.DNS = append(plan.DNS, record)
	}
	return nil
}

// planTLS grades TLS strategy per route through the shared eligibility
// boundary: internal where direct challenges cannot work, public
// automation where they can. Full issuers build later; planning
// records direction only.
func planTLS(plan *InstallPlan) {
	for _, route := range plan.Routes {
		issuer := cert.IssuerInternal
		if docker.RecommendedTLS(route.Host, route.Exposure) == docker.TLSAuto {
			issuer = cert.IssuerACME
		}
		plan.TLS = append(plan.TLS, TLSNeed{Host: string(route.Host), Issuer: issuer})
	}
}

// planFirewall desires web serving scoped to the widest route
// exposure: narrower routes ride inside the wider rule, so one policy
// pair covers the install without per-route sprawl.
func planFirewall(plan *InstallPlan) {
	widest := docker.ExposureLocal
	for _, route := range plan.Routes {
		if exposureRank(route.Exposure) > exposureRank(widest) {
			widest = route.Exposure
		}
	}
	zone := firewall.Zone(widest)
	for _, service := range []firewall.Service{
		{Name: "web-http", Protocol: firewall.ProtocolTCP, Port: 80},
		{Name: "web-https", Protocol: firewall.ProtocolTCP, Port: 443},
	} {
		plan.Firewall = append(plan.Firewall, firewall.ServicePolicy{
			Service: service,
			Sources: []firewall.Source{{Zone: zone}},
		})
	}
}

// exposureRank orders exposures narrow to wide for widest-wins logic.
func exposureRank(exposure docker.Exposure) int {
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

package catalog

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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
// provisionable by construction. HostPath instead references an
// owner-chosen existing directory under the class mount (media
// libraries): never provisioned, never created, and Subpath stays empty.
type VolumeSpec struct {
	Service  string
	Class    storage.Class
	Subpath  string
	HostPath string
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
	// ApplianceNames are the appliance's own local names, the dashboard
	// route's (homestead.lan, homestead.local): a package served by path
	// answers under them (ADR-044). Empty means the defaults.
	ApplianceNames []string
}

// DefaultApplianceNames are the dashboard's names before the owner sets
// one (ADR-035): homestead.lan and its mDNS name.
var DefaultApplianceNames = []string{string(docker.DefaultDashboardName), "homestead.local"}

func (r InstallRequest) applianceNames() []string {
	if len(r.ApplianceNames) == 0 {
		return DefaultApplianceNames
	}
	return r.ApplianceNames
}

// PlannedSeed is one resolved first-start file: its absolute host path
// and the content written there when nothing is.
type PlannedSeed struct {
	Service string
	Target  string
	Source  string
	Content string
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
	// Mounts lists every resolved container mount with its absolute
	// host source, declared and owner-supplied alike (S276).
	Mounts []PlannedMount
	// BrowserAdvisories repeats the manifest's browser requirements
	// (S252). They never block: the appliance cannot see the browser.
	BrowserAdvisories []BrowserRequirement
	// Seeds are first-start files written before the app starts, only
	// where absent (ADR-044: Jellyfin's Base URL).
	Seeds []PlannedSeed
	// EdgeNetwork is true when routed services join the external edge
	// network; install creates that network if it is missing.
	EdgeNetwork bool
	Notes       []string
	Actions     []string
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
		// Name every failed check so the owner (and the dashboard's preflight
		// step) can say what is short and how to fix it.
		var failed []string
		for _, check := range report.Checks {
			if !check.OK {
				failed = append(failed, check.Name+": "+check.Detail)
			}
		}
		return InstallPlan{}, fmt.Errorf("catalog: requirements unmet for %q: %s", req.Manifest.Name, strings.Join(failed, "; "))
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
	planBrowser(&plan, req.Manifest)
	plan.emptyNotNil()
	return plan, nil
}

// emptyNotNil turns nil sections into empty slices so the JSON API
// always emits arrays: stateless local packages have no DNS records or
// directories, and clients must not have to guard against null.
func (p *InstallPlan) emptyNotNil() {
	if p.Routes == nil {
		p.Routes = []docker.Route{}
	}
	if p.DNS == nil {
		p.DNS = []dns.Record{}
	}
	if p.TLS == nil {
		p.TLS = []TLSNeed{}
	}
	if p.Firewall == nil {
		p.Firewall = []firewall.ServicePolicy{}
	}
	if p.Directories == nil {
		p.Directories = []ProvisionDir{}
	}
	if p.Mounts == nil {
		p.Mounts = []PlannedMount{}
	}
	if p.BrowserAdvisories == nil {
		p.BrowserAdvisories = []BrowserRequirement{}
	}
	if p.Seeds == nil {
		p.Seeds = []PlannedSeed{}
	}
	if p.Notes == nil {
		p.Notes = []string{}
	}
	if p.Actions == nil {
		p.Actions = []string{}
	}
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
		if req.Manifest.ServedByPath() {
			// The record keeps the app's install name as its key; the
			// edge serves it only at its path (ADR-044).
			route.Path = req.Manifest.AppPath()
			route.StripPrefix = req.Manifest.StripPrefix
			route.Redirects = append([]docker.PathRedirect(nil), req.Manifest.Redirects...)
			if err := route.Validate(); err != nil {
				return fmt.Errorf("catalog: route %q: %w", recommendation.Endpoint, err)
			}
		}
		plan.Routes = append(plan.Routes, route)
	}
	return nil
}

// planCompose renders the stack with catalog-declared data placement
// (S276) plus owner extras: every declared path resolves to
// <class mount>/<package>/<subpath> from the storage assignments — a
// missing assignment is a requirements failure, never a silent
// default — and ephemeral paths render as tmpfs. Owner volumes may
// add mounts but never replace a declaration: a colliding target is
// ignored and noted in the plan. Ports stay unpublished by default —
// local-first serving goes through the proxy, never host ports.
func planCompose(plan *InstallPlan, req InstallRequest) error {
	byService := map[string][]VolumeMapping{}
	declared := map[string]bool{}
	seenDirs := map[string]bool{}
	addDir := func(mountpoint string, dir storage.Directory) {
		key := mountpoint + "\x00" + dir.Path
		if seenDirs[key] {
			return
		}
		seenDirs[key] = true
		plan.Directories = append(plan.Directories, ProvisionDir{Mountpoint: mountpoint, Directory: dir})
	}
	mounts, dirs, err := ResolveData(req.Manifest, req.Storage)
	if err != nil {
		return err
	}
	for _, mount := range mounts {
		declared[mount.Service+"\x00"+mount.Target] = true
		byService[mount.Service] = append(byService[mount.Service], VolumeMapping{
			Source: mount.Source, Target: mount.Target, Tmpfs: mount.Ephemeral,
			TmpfsBytes: int64(mount.TmpfsMB) << 20,
		})
	}
	plan.Mounts = append(plan.Mounts, mounts...)
	for _, dir := range dirs {
		addDir(dir.Mountpoint, dir.Directory)
	}
	known := map[string]bool{}
	for _, service := range req.Manifest.Services {
		known[service.Name] = true
	}
	// Statelessness derives from the manifest (ADR-028), and backups,
	// schedules and update backups all trust it. An owner mount on a
	// stateless package would hold data that is never backed up, so it
	// is refused rather than silently unprotected.
	if len(req.Volumes) > 0 && req.Manifest.Stateless() {
		return fmt.Errorf("catalog: %s is stateless and keeps nothing on the appliance, so it takes no owner volumes (they would never be backed up)", req.Manifest.Name)
	}
	for _, volume := range req.Volumes {
		if !known[volume.Service] {
			return fmt.Errorf("catalog: volume names unknown service %q", volume.Service)
		}
		if declared[volume.Service+"\x00"+volume.Target] {
			return fmt.Errorf("catalog: volume %s %s collides with a path the catalog declares; declarations cannot be overridden",
				volume.Service, volume.Target)
		}
		mountpoint, ok := req.Storage[volume.Class]
		if !ok {
			return fmt.Errorf("catalog: class %s has no mountpoint", volume.Class)
		}
		if volume.Target == "" || volume.Target[0] != '/' {
			return fmt.Errorf("catalog: volume target %q must be absolute", volume.Target)
		}
		if volume.HostPath != "" {
			source, err := validateHostPath(mountpoint, volume)
			if err != nil {
				return err
			}
			byService[volume.Service] = append(byService[volume.Service], VolumeMapping{
				Source: source, Target: volume.Target, ReadOnly: volume.ReadOnly,
			})
			plan.Mounts = append(plan.Mounts, PlannedMount{
				Service: volume.Service, Target: volume.Target, Source: source,
				Class: volume.Class, ReadOnly: volume.ReadOnly,
			})
			continue
		}
		if err := validateSubpath(volume.Subpath); err != nil {
			return err
		}
		source := mountpoint + "/" + req.Manifest.Name + "/" + volume.Subpath
		byService[volume.Service] = append(byService[volume.Service], VolumeMapping{
			Source: source, Target: volume.Target, ReadOnly: volume.ReadOnly,
		})
		plan.Mounts = append(plan.Mounts, PlannedMount{
			Service: volume.Service, Target: volume.Target, Source: source,
			Class: volume.Class, ReadOnly: volume.ReadOnly,
		})
		relative := req.Manifest.Name + "/" + volume.Subpath
		addDir(mountpoint, storage.Directory{
			Path: relative, Mode: 0o750, Uid: storage.UnsetID, Gid: storage.UnsetID,
		})
	}
	var specs []ServiceGeneration
	for _, service := range req.Manifest.Services {
		specs = append(specs, ServiceGeneration{Service: service.Name, Volumes: byService[service.Name]})
	}
	networks := req.Networks
	var edgeServices map[string]bool
	if len(plan.Routes) > 0 {
		networks = withEdgeNetwork(req.Networks)
	}
	plan.EdgeNetwork = len(plan.Routes) > 0
	if plan.EdgeNetwork {
		plan.Notes = append(plan.Notes, fmt.Sprintf("install creates the %s network if it is missing so routed services can attach; this does not deploy the edge proxy, so routes will not serve until the proxy is set up on the Edge page", docker.NetworkEdge))
	}
	if len(networks) != len(req.Networks) {
		// Only the routed services join the edge network the planner
		// added; databases and caches stay on the package network.
		edgeServices = map[string]bool{}
		for _, route := range plan.Routes {
			edgeServices[route.Target.Host] = true
		}
	}
	routeHosts := map[string]string{}
	routeNames := map[string][]string{}
	routePaths := map[string]string{}
	for _, route := range plan.Routes {
		if route.IsPath() {
			// Served under the appliance's names (ADR-044).
			names := req.applianceNames()
			routeHosts[route.Target.Host] = names[0]
			routeNames[route.Target.Host] = append(routeNames[route.Target.Host], names...)
			routePaths[route.Target.Host] = strings.TrimSuffix(route.Path, "/")
			continue
		}
		routeHosts[route.Target.Host] = string(route.Host)
		for _, n := range route.Names() {
			routeNames[route.Target.Host] = append(routeNames[route.Target.Host], string(n))
		}
	}
	files, err := Generate(req.Manifest, Generation{Networks: networks, EdgeServices: edgeServices, Services: specs,
		RouteHosts: routeHosts, RouteNames: routeNames, RoutePaths: routePaths})
	if err != nil {
		return err
	}
	plan.Compose = files
	for _, seed := range req.Manifest.Seeds {
		data, _ := seedMount(req.Manifest, seed)
		var source string
		for _, mount := range plan.Mounts {
			if mount.Service == seed.Service && mount.Target == data.Target {
				source = mount.Source + strings.TrimPrefix(seed.Target, strings.TrimSuffix(data.Target, "/"))
			}
		}
		if source == "" {
			return fmt.Errorf("catalog: seed %q has no mounted data path", seed.Target)
		}
		content := strings.ReplaceAll(seed.Content, RoutePathToken, routePaths[seed.Service])
		plan.Seeds = append(plan.Seeds, PlannedSeed{Service: seed.Service, Target: seed.Target, Source: source, Content: content})
	}
	for _, image := range craftImages(files.Images) {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%s is built locally from a signed craft manifest (never pulled): run homesteadctl craft build first; install refuses until the recorded local image matches", image))
	}
	plan.Actions = append(plan.Actions, "provision", "up")
	return nil
}

// withEdgeNetwork guarantees the stack joins the edge network the proxy
// reaches applications on (S249). Every install plans at least one
// route, and a route upstream is a service name that only resolves on a
// network shared with the proxy: a caller that listed other networks (the
// CLI and acceptance flows) or none at all produced a stack the edge
// answered nothing for (the S279 finding). An owner-supplied edge entry
// is kept as given. With no networks listed the project default network
// is kept too, so services still reach each other as before.
func withEdgeNetwork(networks []NetworkAttachment) []NetworkAttachment {
	for _, network := range networks {
		if network.Name == docker.NetworkEdge {
			return networks
		}
	}
	out := append([]NetworkAttachment(nil), networks...)
	if len(out) == 0 {
		out = append(out, NetworkAttachment{Name: "default"})
	}
	return append(out, NetworkAttachment{Name: docker.NetworkEdge, External: true})
}

// validateHostPath checks an owner-chosen library path: absolute,
// inside the class mount, and an existing directory. Libraries predate
// installs and outlive them, so planning never creates them — a typo
// must fail instead of mounting an empty directory.
func validateHostPath(mountpoint string, volume VolumeSpec) (string, error) {
	if volume.Subpath != "" {
		return "", fmt.Errorf("catalog: volume %s %s takes a host path or a subpath, not both", volume.Service, volume.Target)
	}
	if !filepath.IsAbs(volume.HostPath) {
		return "", fmt.Errorf("catalog: host path %q must be absolute", volume.HostPath)
	}
	cleaned := filepath.Clean(volume.HostPath)
	root := filepath.Clean(mountpoint)
	if cleaned != root && !strings.HasPrefix(cleaned, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("catalog: host path %q lives outside the %s mount %q", volume.HostPath, volume.Class, mountpoint)
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("catalog: host path %q: %w", volume.HostPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("catalog: host path %q is not a directory", volume.HostPath)
	}
	return cleaned, nil
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
		if route.IsPath() {
			// Served on the appliance's names, under their certificate.
			continue
		}
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

// planBrowser copies the manifest's browser requirements into the plan
// with the standing caveat (S252): advisory only, never a refusal.
func planBrowser(plan *InstallPlan, manifest Manifest) {
	if len(manifest.Browser) == 0 {
		return
	}
	plan.BrowserAdvisories = append([]BrowserRequirement(nil), manifest.Browser...)
	plan.Notes = append(plan.Notes, "browser capabilities are advisory: the appliance cannot see your browser, so install is not blocked; check the listed capabilities on the device you will use")
}

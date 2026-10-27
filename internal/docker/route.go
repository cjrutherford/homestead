package docker

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Exposure names which origin may reach a route. It mirrors firewall.Zone
// in words (ADR-006) so route intent and filter intent stay in one
// language; the route generator (S083) and firewall planning reconcile
// the two. The type lives here rather than importing the firewall package
// so the edge-proxy model stays dependency-free.
type Exposure string

const (
	// ExposureLocal serves LAN origins behind the gateway.
	ExposureLocal Exposure = "local"
	// ExposureOverlay serves private overlay origins (Tailscale/Headscale).
	ExposureOverlay Exposure = "overlay"
	// ExposurePublic serves internet origins past the gateway. Public
	// exposure stays explicit, planned, previewed, and auditable; the
	// model records intent, later slices enforce the ceremony.
	ExposurePublic Exposure = "public"
)

// Valid reports a known exposure.
func (e Exposure) Valid() bool {
	return e == ExposureLocal || e == ExposureOverlay || e == ExposurePublic
}

// TLSMode selects how a route terminates TLS. Modes stay provider-abstract
// (ADR-010): issuance mechanics — Caddy automation, DNS-01 orchestration,
// internal CA, imported/manual paths — attach in the TLS slices, never as
// new modes here without a superseding ADR.
type TLSMode string

const (
	// TLSOff serves plain HTTP.
	TLSOff TLSMode = "off"
	// TLSInternal issues from a local CA for private names.
	TLSInternal TLSMode = "internal"
	// TLSAuto automates issuance for the route hostname.
	TLSAuto TLSMode = "auto"
)

// Valid reports a known TLS mode.
func (m TLSMode) Valid() bool {
	return m == TLSOff || m == TLSInternal || m == TLSAuto
}

var (
	validHostnameLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	validCraftApp      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
)

// Hostname is a canonical route name: lowercase DNS, single-label LAN
// names included. Lowercase is enforced so desired state has one spelling
// per name (DNS itself is case-insensitive). Wildcards stay out: they
// need DNS-01 issuance, a TLS-slice concern.
type Hostname string

// Validate checks canonical shape.
func (h Hostname) Validate() error {
	name := string(h)
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("route.host: must be 1-253 characters")
	}
	if strings.ToLower(name) != name {
		return fmt.Errorf("route.host: must be lowercase")
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return fmt.Errorf("route.host: must not have empty labels")
	}
	for _, label := range strings.Split(name, ".") {
		if !validHostnameLabel.MatchString(label) {
			return fmt.Errorf("route.host: invalid label %q", label)
		}
	}
	return nil
}

// Target is the upstream a route proxies to: a container or service name
// on the edge network, or an IP — plus port. Validation stays lenient on
// the host (naming belongs to the runtime) but rejects URL pastes and
// whitespace so mis-shaped values fail at desired state, not at render.
type Target struct {
	Host string
	Port int
}

// Validate checks the upstream shape.
func (t Target) Validate() error {
	if t.Host == "" || len(t.Host) > 253 {
		return fmt.Errorf("route.target.host: must be 1-253 characters")
	}
	if strings.ContainsAny(t.Host, " \t\n/") || strings.Contains(t.Host, "://") {
		return fmt.Errorf("route.target.host: must be a host, not a URL")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("route.target.port: must be 1-65535")
	}
	return nil
}

// HealthCheck configures active upstream probing. Empty Path disables
// active checks (Caddy still fails over passively); IntervalSeconds 0
// means the generator default.
type HealthCheck struct {
	Path            string
	IntervalSeconds int
}

// Validate checks the probe shape.
func (h HealthCheck) Validate() error {
	if h.Path != "" && !strings.HasPrefix(h.Path, "/") {
		return fmt.Errorf("route.health.path: must start with /")
	}
	if h.IntervalSeconds != 0 && (h.IntervalSeconds < 5 || h.IntervalSeconds > 300) {
		return fmt.Errorf("route.health.interval: must be 0 or 5-300 seconds")
	}
	return nil
}

// Route is one desired edge-proxy entry: the hostname served, the
// upstream answering, who may reach it, how TLS terminates, and how the
// upstream is probed. An optional exposure policy (S116) narrows the
// origin zone at the proxy; nil means zone-wide exposure governed by
// the firewall alone. The model validates shape and enum membership
// only; cross-field policy (for example public exposure wanting TLS)
// belongs to the plan/preview layers, where the owner can see and
// override recommendations.
type Route struct {
	Host     Hostname
	Target   Target
	Exposure Exposure
	TLS      TLSMode
	Health   HealthCheck
	Policy   *Policy
	// CraftApp names the craft app (S254, ADR-032) this route serves. A
	// host route that names one also forwards CraftFilesPrefix to the
	// daemon's craft-file socket, ahead of the upstream; a path route
	// forwards everything under <path>_homestead/ (ADR-045). Empty for
	// every other route.
	CraftApp string `json:",omitempty"`
	// Dashboard marks the one route that fronts the daemon's own dashboard
	// and API over the dashboard unix socket (S289, ADR-035). Its Target is
	// unused. It is local or overlay only, with internal TLS, and never
	// public: the tunnel is the public path.
	Dashboard bool `json:",omitempty"`
	// Aliases are extra names the dashboard route answers on the same
	// leaf (the appliance's mDNS .local name, ADR-035 amendment).
	Aliases []Hostname `json:",omitempty"`
	// Tunnel are public names the Cloudflare tunnel delivers to this route
	// (ADR-037): served only on the loopback tunnel entry, never on the LAN
	// listeners, and owned by the tunnel setup and teardown.
	Tunnel []Hostname `json:",omitempty"`
	// Path, when set, makes this an app route served under the
	// appliance's own names (ADR-044): the dashboard route's site block
	// carries a handle at Path for it, and the edge never serves it on
	// Host. Host then only names the record (the app's install name), so
	// the route keeps one key in the table. Paths live under AppsPrefix.
	Path string `json:",omitempty"`
	// StripPrefix removes Path before the request reaches the upstream,
	// for apps whose base-path setting only rewrites the links they
	// generate (Nextcloud's overwritewebroot). Apps that serve under the
	// prefix themselves (Authentik, Jellyfin) keep it.
	StripPrefix bool `json:",omitempty"`
	// Redirects send fixed root paths on the appliance's names into the
	// app's path (Nextcloud's /.well-known/caldav and carddav).
	Redirects []PathRedirect `json:",omitempty"`
	// Published makes the path also answer on the dashboard route's
	// tunnel names (ADR-044 §3). Until then the public names answer 404
	// for it. Set and cleared only by the approved public-address plan.
	Published bool `json:",omitempty"`
	// paths are the path routes the dashboard route carries when it is
	// rendered (render time only, never stored).
	paths []Route
}

// AppsPrefix is the reserved path prefix app paths live under
// (ADR-044). The dashboard serves nothing below it, so an app path
// never shadows /api/ or the dashboard's assets.
const AppsPrefix = "/apps/"

var (
	validAppPath      = regexp.MustCompile(`^/apps/[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?/$`)
	validRedirectFrom = regexp.MustCompile(`^/\.well-known/[a-z0-9-]+$`)
	validRedirectTo   = regexp.MustCompile(`^[A-Za-z0-9._~/-]*$`)
)

// PathRedirect redirects From (a /.well-known/ path on the appliance's
// names) to To, relative to the app's Path.
type PathRedirect struct {
	From string
	To   string
}

// AppPathFor is the default path of an app (ADR-044): /apps/<name>/.
func AppPathFor(name string) string { return AppsPrefix + name + "/" }

// ValidateAppPath checks an app path: /apps/<label>/ exactly.
func ValidateAppPath(path string) error {
	if !validAppPath.MatchString(path) {
		return fmt.Errorf("route.path: must be %s<name>/ with a lowercase name (the reserved prefix keeps apps off /api/ and the dashboard's assets)", AppsPrefix)
	}
	return nil
}

// IsPath reports a path route (ADR-044).
func (r Route) IsPath() bool { return r.Path != "" }

// PathURL is the app's address on one appliance name, with a trailing
// slash: https://homestead.lan/apps/nextcloud/.
func PathURL(host Hostname, path string) string {
	return "https://" + string(host) + path
}

// ErrDashboardPublic explains the refusal of a public dashboard route.
const ErrDashboardPublic = "the dashboard route is local or overlay only with internal TLS and is never public through the edge: the Cloudflare tunnel is the public path"

// Names lists every name the route answers on: the host, then aliases.
func (r Route) Names() []Hostname {
	return append([]Hostname{r.Host}, r.ServedAliases()...)
}

// AllNames is Names plus the tunnel's public names: every Host the route
// accepts, on any listener.
func (r Route) AllNames() []Hostname {
	return append(r.Names(), r.Tunnel...)
}

// localSuffix and mdnsSuffix are the local name an app is installed under
// and the name it is announced as over mDNS (ADR-039).
const (
	localSuffix = ".lan"
	mdnsSuffix  = ".local"
)

// mdnsReserved is the dashboard's own mDNS label (ADR-035), never derived
// for an app.
const mdnsReserved = "homestead"

// LocalAlias is the mDNS name an app route also answers on (ADR-039):
// <label>.local for a local or overlay app route whose host is
// <label>.lan. It is derived, never stored, so it follows the route.
func (r Route) LocalAlias() (Hostname, bool) {
	if r.Dashboard || r.IsPath() || r.Exposure == ExposurePublic || r.TLS == TLSAuto {
		return "", false
	}
	host := strings.ToLower(string(r.Host))
	label, ok := strings.CutSuffix(host, localSuffix)
	if !ok || label == "" || strings.Contains(label, ".") || label == mdnsReserved {
		return "", false
	}
	return Hostname(label + mdnsSuffix), true
}

// ServedAliases lists the route's extra names: the stored aliases (the
// dashboard's) and the derived mDNS name of an app route.
func (r Route) ServedAliases() []Hostname {
	out := append([]Hostname(nil), r.Aliases...)
	if alias, ok := r.LocalAlias(); ok {
		out = append(out, alias)
	}
	return out
}

// MDNSLabels lists the single labels the appliance announces for app
// routes (<label>.local, ADR-039), sorted and without duplicates.
func (s RouteSet) MDNSLabels() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range s.Routes {
		if alias, ok := r.LocalAlias(); ok {
			label := strings.TrimSuffix(string(alias), mdnsSuffix)
			if !seen[label] {
				seen[label] = true
				out = append(out, label)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Validate checks a route end to end.
func (r Route) Validate() error {
	if err := r.Host.Validate(); err != nil {
		return err
	}
	if r.Dashboard {
		if err := r.validateDashboard(); err != nil {
			return err
		}
	} else if len(r.Aliases) > 0 {
		return fmt.Errorf("route.aliases: only the dashboard route has aliases")
	} else if err := r.Target.Validate(); err != nil {
		return err
	}
	if !r.Exposure.Valid() {
		return fmt.Errorf("route.exposure: must be local, overlay, or public")
	}
	if !r.TLS.Valid() {
		return fmt.Errorf("route.tls: must be off, internal, or auto")
	}
	if r.CraftApp != "" && !validCraftApp.MatchString(r.CraftApp) {
		return fmt.Errorf("route.craftapp: must be a lowercase craft app name")
	}
	if err := r.validateTunnel(); err != nil {
		return err
	}
	if err := r.validatePath(); err != nil {
		return err
	}
	if r.Policy != nil {
		if err := r.Policy.Validate(); err != nil {
			return err
		}
	}
	if err := r.Health.Validate(); err != nil {
		return err
	}
	return nil
}

// validateTunnel: tunnel names are public DNS names, distinct from each
// other and from the route's local names, on a route that is not itself
// public (a public route already has its own listener and certificate).
func (r Route) validateTunnel() error {
	if len(r.Tunnel) == 0 {
		return nil
	}
	if r.Exposure == ExposurePublic {
		return fmt.Errorf("route.tunnel: a public route is not also delivered by the tunnel")
	}
	if r.CraftApp != "" {
		// A craft app is published under /apps/ on the dashboard's public
		// name (ADR-045); one on a name of its own moves there first.
		return fmt.Errorf("route.tunnel: a craft app is published at its /apps/ path; move it there first (apps move)")
	}
	seen := map[Hostname]bool{}
	for _, n := range r.Names() {
		seen[n] = true
	}
	for _, n := range r.Tunnel {
		if err := n.Validate(); err != nil {
			return err
		}
		if IsPrivateName(n) {
			return fmt.Errorf("route.tunnel: %q is a local name; the tunnel carries public names", n)
		}
		if seen[n] {
			return fmt.Errorf("route.tunnel: duplicate name %q", n)
		}
		seen[n] = true
	}
	return nil
}

// validatePath: a path route is an app route under /apps/, local or
// overlay (the public names come through the tunnel when published),
// without names of its own on the edge.
func (r Route) validatePath() error {
	if !r.IsPath() {
		if r.StripPrefix || len(r.Redirects) > 0 || r.Published {
			return fmt.Errorf("route.path: strip, redirects and published apply to path routes only")
		}
		return nil
	}
	if err := ValidateAppPath(r.Path); err != nil {
		return err
	}
	switch {
	case r.Dashboard:
		return fmt.Errorf("route.path: the dashboard route has no path")
	case r.CraftApp != "" && !r.StripPrefix:
		return fmt.Errorf("route.path: a craft app route strips its path (ADR-045)")
	case len(r.Tunnel) > 0:
		return fmt.Errorf("route.path: a path route is published on the appliance's public name, not by tunnel names")
	case r.Exposure == ExposurePublic:
		return fmt.Errorf("route.path: a path route is local or overlay; publish it through the tunnel")
	}
	for _, rd := range r.Redirects {
		if !validRedirectFrom.MatchString(rd.From) || !validRedirectTo.MatchString(rd.To) ||
			strings.HasPrefix(rd.To, "/") || strings.Contains(rd.To, "..") {
			return fmt.Errorf("route.redirects: %q must be a /.well-known/ path into the app's path", rd.From)
		}
	}
	return nil
}

func (r Route) validateDashboard() error {
	if r.Exposure == ExposurePublic || r.TLS == TLSAuto {
		return fmt.Errorf("route.dashboard: %s", ErrDashboardPublic)
	}
	if r.TLS != TLSInternal {
		return fmt.Errorf("route.dashboard: TLS must be internal")
	}
	if r.CraftApp != "" {
		return fmt.Errorf("route.dashboard: cannot also be a craft route")
	}
	seen := map[Hostname]bool{r.Host: true}
	for _, a := range r.Aliases {
		if err := a.Validate(); err != nil {
			return err
		}
		if seen[a] {
			return fmt.Errorf("route.aliases: duplicate name %q", a)
		}
		seen[a] = true
	}
	return nil
}

// RouteSet is the desired route table the generator (S083) reconciles.
type RouteSet struct {
	Routes []Route
}

// Validate checks every route and hostname uniqueness.
func (s RouteSet) Validate() error {
	seen := map[Hostname]bool{}
	dashboards := 0
	for i := range s.Routes {
		if err := s.Routes[i].Validate(); err != nil {
			return fmt.Errorf("route #%d: %w", i, err)
		}
		if s.Routes[i].Dashboard {
			dashboards++
		}
		for _, n := range s.Routes[i].AllNames() {
			if seen[n] {
				return fmt.Errorf("duplicate route host %q", n)
			}
			seen[n] = true
		}
	}
	if dashboards > 1 {
		return fmt.Errorf("only one dashboard route is allowed")
	}
	paths := map[string]bool{}
	redirects := map[string]bool{}
	for _, r := range s.Routes {
		if !r.IsPath() {
			continue
		}
		if dashboards == 0 {
			return fmt.Errorf("route %q: a path route is served under the dashboard's names, and there is no dashboard route; set the dashboard address first", r.Host)
		}
		if paths[r.Path] {
			return fmt.Errorf("duplicate route path %q", r.Path)
		}
		paths[r.Path] = true
		for _, rd := range r.Redirects {
			if redirects[rd.From] {
				return fmt.Errorf("duplicate route redirect %q", rd.From)
			}
			redirects[rd.From] = true
		}
	}
	return nil
}

// withPaths returns the routes with the path routes handed to the
// dashboard route for rendering (ADR-044).
func withPaths(routes []Route) []Route {
	paths := RouteSet{Routes: routes}.PathRoutes()
	out := append([]Route(nil), routes...)
	for i := range out {
		if out[i].Dashboard {
			out[i].paths = paths
		}
	}
	return out
}

// PathRoutes lists the set's path routes, sorted by path.
func (s RouteSet) PathRoutes() []Route {
	var out []Route
	for _, r := range s.Routes {
		if r.IsPath() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Sorted returns routes ordered by hostname for deterministic rendering.
func (s RouteSet) Sorted() []Route {
	out := append([]Route(nil), s.Routes...)
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// Find returns the route serving host, if any.
func (s RouteSet) Find(host Hostname) (Route, bool) {
	for _, r := range s.Routes {
		if r.Host == host {
			return r, true
		}
	}
	return Route{}, false
}

// LANNames lists every `.lan` name a non-public route answers on (the
// dashboard's homestead.lan and each app's <label>.lan), lower-case,
// sorted and without duplicates: the names the appliance resolver
// answers (ADR-039).
func (s RouteSet) LANNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range s.Routes {
		if r.Exposure == ExposurePublic || r.IsPath() {
			continue
		}
		for _, n := range r.Names() {
			name := strings.ToLower(string(n))
			if strings.HasSuffix(name, localSuffix) && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ServedRoutes gives each path route the names it is actually served on
// (ADR-044): the dashboard route's host and aliases, and its tunnel
// names once the path is published. Host routes come back unchanged, as
// does a path route when all holds no dashboard route. The path stays,
// so a browser origin for the app is a name plus its path.
func ServedRoutes(all, routes []Route) []Route {
	dash := -1
	for i, r := range all {
		if r.Dashboard {
			dash = i
			break
		}
	}
	out := make([]Route, 0, len(routes))
	for _, r := range routes {
		if r.IsPath() && dash >= 0 {
			d := all[dash]
			r.Host, r.Aliases, r.Tunnel = d.Host, append([]Hostname(nil), d.Aliases...), nil
			if r.Published {
				r.Tunnel = append([]Hostname(nil), d.Tunnel...)
			}
		}
		out = append(out, r)
	}
	return out
}

// PublicServerName is the dashboard route's first tunnel name, which
// published path apps answer on (ADR-044), or "" when it has none.
func PublicServerName(all []Route) Hostname {
	for _, r := range all {
		if r.Dashboard && len(r.Tunnel) > 0 {
			return r.Tunnel[0]
		}
	}
	return ""
}

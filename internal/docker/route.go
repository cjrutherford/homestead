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
	// CraftApp names the craft app (S254, ADR-032) this host serves. A
	// route that names one also forwards CraftFilesPrefix to the daemon's
	// craft-file socket, ahead of the upstream. Empty for every other route.
	CraftApp string `json:",omitempty"`
}

// Validate checks a route end to end.
func (r Route) Validate() error {
	if err := r.Host.Validate(); err != nil {
		return err
	}
	if err := r.Target.Validate(); err != nil {
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

// RouteSet is the desired route table the generator (S083) reconciles.
type RouteSet struct {
	Routes []Route
}

// Validate checks every route and hostname uniqueness.
func (s RouteSet) Validate() error {
	seen := map[Hostname]bool{}
	for i := range s.Routes {
		if err := s.Routes[i].Validate(); err != nil {
			return fmt.Errorf("route #%d: %w", i, err)
		}
		if seen[s.Routes[i].Host] {
			return fmt.Errorf("duplicate route host %q", s.Routes[i].Host)
		}
		seen[s.Routes[i].Host] = true
	}
	return nil
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

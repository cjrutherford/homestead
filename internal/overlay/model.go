package overlay

import (
	"fmt"
	"net/netip"
	"strings"
)

// Provider selects the overlay implementation (ADR-011): none for
// local-only operation, Headscale preferred open-source, Tailscale for
// existing Tailscale-based access. Providers attach in S111+, never as
// new values here without a superseding ADR.
type Provider string

const (
	// ProviderNone disables remote access: local-only operation.
	ProviderNone Provider = "none"
	// ProviderHeadscale uses Headscale, self-hosted or existing.
	ProviderHeadscale Provider = "headscale"
	// ProviderTailscale uses Tailscale with an owner account.
	ProviderTailscale Provider = "tailscale"
)

// Valid reports a known provider.
func (p Provider) Valid() bool {
	return p == ProviderNone || p == ProviderHeadscale || p == ProviderTailscale
}

// SubnetRoute is one LAN prefix the appliance offers to overlay peers.
// Enabled stages configuration without advertising: peers see only
// enabled routes. Default routes (exit nodes) are refused — exit-node
// operation needs its own slice, and silently becoming one would
// reroute peer traffic through the appliance.
type SubnetRoute struct {
	Destination netip.Prefix
	Enabled     bool
}

// Validate checks the route shape.
func (r SubnetRoute) Validate() error {
	if !r.Destination.IsValid() {
		return fmt.Errorf("overlay: route destination must be a valid prefix")
	}
	if r.Destination.Bits() == 0 {
		return fmt.Errorf("overlay: default routes need exit-node support")
	}
	if r.Destination.Addr().IsLoopback() || r.Destination.Addr().IsUnspecified() {
		return fmt.Errorf("overlay: route destination must be routable")
	}
	return nil
}

// DNSPreferences is the overlay DNS intent: provider DNS on or off, an
// optional search domain for short names, and optional explicit
// nameservers. Empty search and nameservers mean provider defaults.
type DNSPreferences struct {
	Enabled      bool
	SearchDomain string
	Nameservers  []netip.Addr
}

// Validate checks DNS intent shape.
func (d DNSPreferences) Validate() error {
	if d.SearchDomain != "" {
		if err := validateDNSName("overlay.dns.search", d.SearchDomain); err != nil {
			return err
		}
	}
	for _, server := range d.Nameservers {
		if !server.IsValid() || server.IsUnspecified() {
			return fmt.Errorf("overlay: nameserver must be a usable IP address")
		}
	}
	return nil
}

// validateDNSName checks canonical lowercase DNS for overlay names,
// shared in spelling with routes and certificates.
func validateDNSName(field, name string) error {
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("%s: must be 1-253 characters", field)
	}
	if strings.ToLower(name) != name {
		return fmt.Errorf("%s: must be lowercase", field)
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return fmt.Errorf("%s: must not have empty labels", field)
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("%s: invalid label %q", field, label)
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return fmt.Errorf("%s: invalid label %q", field, label)
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%s: invalid label %q", field, label)
		}
	}
	return nil
}

// validateHostname checks one overlay node name: a single lowercase
// label, the spelling every provider accepts for machine names.
func validateHostname(field, name string) error {
	if len(name) == 0 || len(name) > 63 {
		return fmt.Errorf("%s: must be 1-63 characters", field)
	}
	if strings.ToLower(name) != name {
		return fmt.Errorf("%s: must be lowercase", field)
	}
	for _, r := range name {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("%s: invalid hostname %q", field, name)
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("%s: invalid hostname %q", field, name)
	}
	return nil
}

// Config is the desired overlay state: which provider, under what node
// name, offering which LAN routes, accepting peer routes, with DNS
// intent. ProviderNone with any adornment is invalid — disabled means
// unconfigured, so stale settings cannot linger into re-enablement.
type Config struct {
	Provider        Provider
	Hostname        string
	AdvertiseRoutes []SubnetRoute
	AcceptRoutes    bool
	DNS             DNSPreferences
}

// Validate checks desired state end to end.
func (c Config) Validate() error {
	if !c.Provider.Valid() {
		return fmt.Errorf("overlay: provider must be none, headscale, or tailscale")
	}
	if c.Provider == ProviderNone {
		if c.Hostname != "" || len(c.AdvertiseRoutes) > 0 || c.AcceptRoutes || c.DNS.Enabled {
			return fmt.Errorf("overlay: disabled provider takes no settings")
		}
		return nil
	}
	if err := validateHostname("overlay.hostname", c.Hostname); err != nil {
		return err
	}
	seen := map[netip.Prefix]bool{}
	for i := range c.AdvertiseRoutes {
		if err := c.AdvertiseRoutes[i].Validate(); err != nil {
			return fmt.Errorf("route #%d: %w", i, err)
		}
		if seen[c.AdvertiseRoutes[i].Destination] {
			return fmt.Errorf("duplicate route %q", c.AdvertiseRoutes[i].Destination)
		}
		seen[c.AdvertiseRoutes[i].Destination] = true
	}
	if err := c.DNS.Validate(); err != nil {
		return err
	}
	return nil
}

// Node is one observed overlay peer (or the appliance itself): name,
// addresses, presence, and the routes it offers. Adapters (S111+)
// translate provider status into these; nothing here calls providers.
type Node struct {
	Name      string
	Addresses []netip.Addr
	Online    bool
	Offers    []netip.Prefix
}

// Validate checks observed node shape leniently: names exist, addresses
// and offered routes parse. Presence of data, not policy, is the bar.
func (n Node) Validate() error {
	if err := validateHostname("overlay.node", n.Name); err != nil {
		return err
	}
	for _, addr := range n.Addresses {
		if !addr.IsValid() || addr.IsUnspecified() {
			return fmt.Errorf("overlay: node address must be usable")
		}
	}
	for _, route := range n.Offers {
		if !route.IsValid() {
			return fmt.Errorf("overlay: offered route must be a valid prefix")
		}
	}
	return nil
}

// Network is the observed overlay: whether the appliance is up, its
// addresses, and visible peers. Empty peers with Online true means a
// lonely but working tailnet, not an error.
type Network struct {
	Online    bool
	Addresses []netip.Addr
	Nodes     []Node
}

// Validate checks observed network shape.
func (n Network) Validate() error {
	for _, addr := range n.Addresses {
		if !addr.IsValid() || addr.IsUnspecified() {
			return fmt.Errorf("overlay: address must be usable")
		}
	}
	seen := map[string]bool{}
	for i := range n.Nodes {
		if err := n.Nodes[i].Validate(); err != nil {
			return fmt.Errorf("node #%d: %w", i, err)
		}
		if seen[n.Nodes[i].Name] {
			return fmt.Errorf("duplicate node %q", n.Nodes[i].Name)
		}
		seen[n.Nodes[i].Name] = true
	}
	return nil
}

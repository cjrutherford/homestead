package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"

	"homestead/internal/operation"
)

// ErrTailscaleAbsent reports a missing tailscale binary: absence is
// data (the S070 rule), and callers treat it as unadopted rather than
// broken.
var ErrTailscaleAbsent = errors.New("overlay: tailscale not installed")

// TailscaleAuthKeySecret names the auth-key secret under the shared
// convention, so the wizard (S117) and this adapter meet at one name.
const TailscaleAuthKeySecret = "overlay_tailscale_authkey"

// tailscaleExec runs the tailscale CLI, stubbed in tests. The binary
// path stays bare PATH resolution: Homestead never vendors client
// binaries it does not own.
var tailscaleExec = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "tailscale", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("overlay: tailscale %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrTailscaleAbsent
		}
		return "", fmt.Errorf("overlay: tailscale: %w", err)
	}
	return string(out), nil
}

// Present reports whether the tailscale binary answers.
func Present(ctx context.Context) bool {
	_, err := tailscaleExec(ctx, "version")
	return err == nil
}

// tailscaleStatus is the `status --json` subset the adapter reads.
// Unknown fields ride along ignored so client upgrades never break
// observation.
type tailscaleStatus struct {
	BackendState string                   `json:"BackendState"`
	Self         tailscaleStatusSelf      `json:"Self"`
	Peer         map[string]tailscalePeer `json:"Peer"`
}

type tailscaleStatusSelf struct {
	HostName     string   `json:"HostName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
}

type tailscalePeer struct {
	HostName     string   `json:"HostName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	AllowedIPs   []string `json:"AllowedIPs"`
	Online       *bool    `json:"Online"`
}

// parseIPs parses address strings, skipping the invalid instead of
// failing observation: one odd address must not hide a whole tailnet.
func parseIPs(values []string) []netip.Addr {
	var out []netip.Addr
	for _, value := range values {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.IsUnspecified() {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// parsePrefixes parses CIDR strings, skipping the invalid for the same
// reason as addresses.
func parsePrefixes(values []string) []netip.Prefix {
	var out []netip.Prefix
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			continue
		}
		out = append(out, prefix)
	}
	return out
}

// Status adopts existing Tailscale access: it reads `tailscale status
// --json` and maps the tailnet to the neutral Network. Peers whose
// names violate the hostname spelling are skipped (documented data
// loss at the boundary, never silent elsewhere); absent Online flags
// default to present, since listed peers without explicit offline state
// are reachable.
func Status(ctx context.Context) (Network, error) {
	raw, err := tailscaleExec(ctx, "status", "--json")
	if err != nil {
		return Network{}, err
	}
	var status tailscaleStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return Network{}, fmt.Errorf("overlay: parse status: %w", err)
	}
	network := Network{Online: status.BackendState == "Running"}
	network.Addresses = parseIPs(status.Self.TailscaleIPs)
	var names []string
	peers := map[string]tailscalePeer{}
	for _, peer := range status.Peer {
		name := strings.ToLower(peer.HostName)
		if err := validateHostname("overlay.node", name); err != nil || name == "" {
			continue
		}
		if _, exists := peers[name]; exists {
			continue
		}
		peers[name] = peer
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		peer := peers[name]
		node := Node{Name: name, Online: true}
		if peer.Online != nil {
			node.Online = *peer.Online
		}
		node.Addresses = parseIPs(peer.TailscaleIPs)
		self := map[netip.Addr]bool{}
		for _, addr := range node.Addresses {
			self[addr] = true
		}
		for _, offer := range parsePrefixes(peer.AllowedIPs) {
			if single, ok := singleHost(offer); ok && self[single] {
				continue
			}
			node.Offers = append(node.Offers, offer)
		}
		network.Nodes = append(network.Nodes, node)
	}
	return network, nil
}

// singleHost reports /32 and /128 prefixes as their host address.
func singleHost(prefix netip.Prefix) (netip.Addr, bool) {
	if !prefix.IsValid() || !prefix.IsSingleIP() {
		return netip.Addr{}, false
	}
	return prefix.Addr(), true
}

// UpOptions carries `tailscale up` intent: an empty auth key reapplies
// settings on an existing login instead of authenticating. Only enabled
// routes advertise; DNS toggles provider DNS acceptance.
type UpOptions struct {
	AuthKey      string
	Hostname     string
	Routes       []SubnetRoute
	AcceptRoutes bool
	AcceptDNS    bool
}

// Validate checks up intent: hostname spelling and routable enabled
// routes. The auth key stays opaque — validated by the tailnet, never
// here.
func (o UpOptions) Validate() error {
	if err := validateHostname("overlay.hostname", o.Hostname); err != nil {
		return err
	}
	for i := range o.Routes {
		if err := o.Routes[i].Validate(); err != nil {
			return fmt.Errorf("route #%d: %w", i, err)
		}
	}
	return nil
}

// upArgs renders the CLI invocation for options.
func upArgs(options UpOptions) []string {
	args := []string{"up"}
	if options.AuthKey != "" {
		args = append(args, "--authkey="+options.AuthKey)
	}
	args = append(args, "--hostname="+options.Hostname)
	var advertised []string
	for _, route := range options.Routes {
		if route.Enabled {
			advertised = append(advertised, route.Destination.String())
		}
	}
	if len(advertised) > 0 {
		args = append(args, "--advertise-routes="+strings.Join(advertised, ","))
	}
	if options.AcceptRoutes {
		args = append(args, "--accept-routes")
	}
	if options.AcceptDNS {
		args = append(args, "--accept-dns")
	}
	return args
}

// UpDefinition plans `tailscale up` as a runner-backed operation: one
// step converging login and settings. Auth material arrives per call
// from the secret store at the caller's layer, never persisted here.
func UpDefinition(options UpOptions) (operation.Definition, error) {
	if err := options.Validate(); err != nil {
		return operation.Definition{}, err
	}
	args := upArgs(options)
	def := operation.Definition{Type: "tailscale.up"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			_, err := tailscaleExec(ctx, args...)
			return err
		},
	})
	return def, nil
}

// LogoutDefinition plans `tailscale logout`, disconnecting and dropping
// credentials. Reconnecting needs a fresh auth key; plain `down` (link
// down, login kept) stays out — logout is the unambiguous off state.
func LogoutDefinition() operation.Definition {
	def := operation.Definition{Type: "tailscale.logout"}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "logout",
		Run: func(ctx context.Context) error {
			_, err := tailscaleExec(ctx, "logout")
			return err
		},
	})
	return def
}

// DemoConfig is the preview profile: a fixed, clearly-labeled desired
// state for walking the remote-access UX without enrollment. Demo
// addresses use documentation space so previews never pass as live
// tailnets.
func DemoConfig() Config {
	return Config{
		Provider: ProviderTailscale,
		Hostname: "demo-appliance",
		AdvertiseRoutes: []SubnetRoute{
			{Destination: netip.MustParsePrefix("192.0.2.0/24"), Enabled: true},
		},
		AcceptRoutes: true,
		DNS:          DNSPreferences{Enabled: true, SearchDomain: "demo.invalid"},
	}
}

// DemoNetwork is the matching observed profile for the demo config.
func DemoNetwork() Network {
	return Network{
		Online:    true,
		Addresses: []netip.Addr{netip.MustParseAddr("100.64.99.99")},
		Nodes: []Node{
			{Name: "demo-laptop", Addresses: []netip.Addr{netip.MustParseAddr("100.64.99.98")}, Online: true},
		},
	}
}

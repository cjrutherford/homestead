package router

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

var validForwardName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Protocols for forwarded ports, mirroring the firewall spelling so
// router intent and filter intent stay in one language.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// Forward is one required NAT rule: the public port and protocol the
// internet reaches, serving the appliance behind it. External and
// internal ports match — remapped ports confuse diagnostics and buy
// nothing — so one port names both ends.
type Forward struct {
	Name     string
	Protocol string
	Port     int
}

// Validate checks the forward shape.
func (f Forward) Validate() error {
	if !validForwardName.MatchString(f.Name) {
		return fmt.Errorf("router: forward name must match [a-z][a-z0-9-]*")
	}
	if f.Protocol != ProtocolTCP && f.Protocol != ProtocolUDP {
		return fmt.Errorf("router: protocol must be tcp or udp")
	}
	if f.Port < 1 || f.Port > 65535 {
		return fmt.Errorf("router: port must be 1-65535")
	}
	return nil
}

// Purpose names a serving need with its required forwards.
type Purpose string

const (
	// PurposeWeb serves public routes: HTTP plus HTTPS.
	PurposeWeb Purpose = "web"
	// PurposeHeadscale serves the managed control plane.
	PurposeHeadscale Purpose = "headscale"
)

// RequiredForwards lists the NAT rules a purpose needs. Unknown
// purposes fail instead of guessing an empty set that would read as
// "nothing required".
func RequiredForwards(purpose Purpose) ([]Forward, error) {
	switch purpose {
	case PurposeWeb:
		return []Forward{
			{Name: "web-http", Protocol: ProtocolTCP, Port: 80},
			{Name: "web-https", Protocol: ProtocolTCP, Port: 443},
		}, nil
	case PurposeHeadscale:
		return []Forward{
			{Name: "headscale-control", Protocol: ProtocolTCP, Port: 8080},
		}, nil
	default:
		return nil, fmt.Errorf("router: unknown purpose %q", purpose)
	}
}

// RequiredSet merges purposes into a deduplicated, sorted forward set:
// shared ports appear once, so combined purposes never double-list.
func RequiredSet(purposes ...Purpose) ([]Forward, error) {
	seen := map[Forward]bool{}
	var out []Forward
	for _, purpose := range purposes {
		forwards, err := RequiredForwards(purpose)
		if err != nil {
			return nil, err
		}
		for _, forward := range forwards {
			if !seen[forward] {
				seen[forward] = true
				out = append(out, forward)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out, nil
}

// RenderInstructions generates the exact router configuration for the
// appliance at lanIP: one rule per forward with matched ports, plus
// the two steps owners always forget — a DHCP reservation so the
// target address survives reboots, and verification afterwards (S133).
// Carrier-grade NAT breaks inbound forwarding regardless of settings,
// so the notes name it instead of letting owners debug silence.
func RenderInstructions(lanIP netip.Addr, forwards []Forward) (string, error) {
	if !lanIP.IsValid() || lanIP.IsUnspecified() || lanIP.IsLoopback() || lanIP.IsMulticast() {
		return "", fmt.Errorf("router: appliance address must be usable")
	}
	if len(forwards) == 0 {
		return "", fmt.Errorf("router: no forwards to render")
	}
	seen := map[string]bool{}
	for i := range forwards {
		if err := forwards[i].Validate(); err != nil {
			return "", fmt.Errorf("forward #%d: %w", i, err)
		}
		key := forwards[i].Protocol + "/" + fmt.Sprint(forwards[i].Port)
		if seen[key] {
			return "", fmt.Errorf("duplicate forward %s", key)
		}
		seen[key] = true
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Port forwarding for Homestead at %s.\n", lanIP)
	body.WriteString("Create one rule per line in the router's port forwarding (NAT) section.\n\n")
	for _, forward := range forwards {
		fmt.Fprintf(&body, "%s: external %s %d -> internal %s %d\n",
			forward.Name, strings.ToUpper(forward.Protocol), forward.Port, lanIP, forward.Port)
	}
	body.WriteString(`
Steps:
1. Reserve a DHCP address (or set a static lease) so this appliance keeps its address.
2. Add each rule above with matching external and internal ports.
3. Save/apply, then re-check reachability (S133 verification).
Notes:
- ISP carrier-grade NAT blocks inbound forwarding no matter the settings; a public address or relay is the only fix.
- Never forward management ports beyond what the exposure plan names.
`)
	return body.String(), nil
}

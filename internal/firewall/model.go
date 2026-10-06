package firewall

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
)

// Zones name where traffic originates. They mirror the exposure vocabulary
// (local/overlay/public) so firewall intent and route exposure stay in one
// language (ADR-006, S116).
type Zone string

const (
	// ZoneLocal is LAN traffic behind the gateway.
	ZoneLocal Zone = "local"
	// ZoneOverlay is private overlay traffic (Tailscale/Headscale).
	ZoneOverlay Zone = "overlay"
	// ZonePublic is internet traffic past the gateway.
	ZonePublic Zone = "public"
)

// Valid reports a known zone.
func (z Zone) Valid() bool {
	return z == ZoneLocal || z == ZoneOverlay || z == ZonePublic
}

var (
	validName  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	validIface = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)
)

// Protocols carried by services.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// Service is a named destination: what listens, on which protocol and
// ports. EndPort 0 means a single port.
type Service struct {
	Name     string
	Protocol string
	Port     int
	EndPort  int
}

// Source is who may reach: a whole zone, optionally narrowed to a CIDR.
// Empty CIDR means the entire zone.
type Source struct {
	Zone Zone
	CIDR string
}

// Actions a rule takes. Reject (with its active response) stays out: the
// appliance filters silently, and S053 renders these two.
type Action string

const (
	// ActionAccept permits matching traffic.
	ActionAccept Action = "accept"
	// ActionDrop discards matching traffic without response.
	ActionDrop Action = "drop"
)

// Rule is one Homestead-owned filter rule: match on interfaces, source,
// and service, then act. Priority orders evaluation (lower first);
// Interfaces empty means all interfaces.
type Rule struct {
	Name       string
	Interfaces []string
	Source     Source
	Service    Service
	Action     Action
	Priority   int
}

// ServicePolicy is UX-level intent: a service reachable from sources.
// Empty Sources blocks the service everywhere (deny by default, explicit
// allow). The planner (S053) derives concrete rules from policies.
type ServicePolicy struct {
	Service Service
	Sources []Source
}

// RuleSet is the ordered desired ruleset Homestead manages.
type RuleSet struct {
	Rules []Rule
}

// fieldError describes one validation failure.
type fieldError struct {
	field  string
	reason string
}

func (e fieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.field, e.reason)
}

// Validate checks the service shape.
func (s Service) Validate() error {
	if !validName.MatchString(s.Name) {
		return fieldError{"service.name", "must match [a-z][a-z0-9-]*"}
	}
	if s.Protocol != ProtocolTCP && s.Protocol != ProtocolUDP {
		return fieldError{"service.protocol", "must be tcp or udp"}
	}
	if s.Port < 1 || s.Port > 65535 {
		return fieldError{"service.port", "must be 1-65535"}
	}
	if s.EndPort != 0 && (s.EndPort < s.Port || s.EndPort > 65535) {
		return fieldError{"service.end-port", "must be 0 or within port-65535"}
	}
	return nil
}

// Validate checks zone membership and CIDR shape.
func (s Source) Validate() error {
	if !s.Zone.Valid() {
		return fieldError{"source.zone", "must be local, overlay, or public"}
	}
	if s.CIDR != "" {
		if _, err := netip.ParsePrefix(s.CIDR); err != nil {
			return fieldError{"source.cidr", "must be CIDR notation"}
		}
	}
	return nil
}

// Validate checks a rule end to end.
func (r Rule) Validate() error {
	if !validName.MatchString(r.Name) {
		return fieldError{"rule.name", "must match [a-z][a-z0-9-]*"}
	}
	for _, iface := range r.Interfaces {
		if !validIface.MatchString(iface) || iface == "." || iface == ".." {
			return fieldError{"rule.interfaces", fmt.Sprintf("invalid interface %q", iface)}
		}
	}
	if err := r.Source.Validate(); err != nil {
		return err
	}
	if err := r.Service.Validate(); err != nil {
		return err
	}
	if r.Action != ActionAccept && r.Action != ActionDrop {
		return fieldError{"rule.action", "must be accept or drop"}
	}
	if r.Priority < 0 {
		return fieldError{"rule.priority", "must be >= 0"}
	}
	return nil
}

// Validate checks every rule and name uniqueness.
func (s RuleSet) Validate() error {
	seen := map[string]bool{}
	for i := range s.Rules {
		if err := s.Rules[i].Validate(); err != nil {
			return fmt.Errorf("rule #%d: %w", i, err)
		}
		if seen[s.Rules[i].Name] {
			return fmt.Errorf("duplicate rule name %q", s.Rules[i].Name)
		}
		seen[s.Rules[i].Name] = true
	}
	return nil
}

// Sorted returns rules in evaluation order: priority, then name.
func (s RuleSet) Sorted() []Rule {
	out := append([]Rule(nil), s.Rules...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Validate checks the policy shape: service plus distinct valid sources.
func (p ServicePolicy) Validate() error {
	if err := p.Service.Validate(); err != nil {
		return err
	}
	seen := map[Zone]bool{}
	for i := range p.Sources {
		if err := p.Sources[i].Validate(); err != nil {
			return fmt.Errorf("source #%d: %w", i, err)
		}
		if seen[p.Sources[i].Zone] {
			return fmt.Errorf("duplicate source zone %q", p.Sources[i].Zone)
		}
		seen[p.Sources[i].Zone] = true
	}
	return nil
}

package firewall

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"homestead/internal/discover"
)

// TableFamily is the nftables family of the Homestead table: inet covers
// both IPv4 and IPv6 with one ruleset.
const TableFamily = "inet"

// Expand derives concrete rules from service policies against live network
// facts. Policies sort by service name and rules take priorities in steps
// of 100, so output is deterministic for identical input.
//
// Zone resolution: an explicit CIDR is used verbatim; otherwise local
// resolves to the LAN subnets of up non-loopback interfaces, while overlay
// and public never resolve implicitly. An accept rule with no resolvable
// source is an error (accepting the world by default is never safe); a
// drop rule without source matches everything, which is exactly the
// public-deny shape. Overlay interface knowledge arrives with the
// remote-access slices (S110+); until then overlay accepts need explicit
// CIDRs, and S222 later narrows LAN selection.
func Expand(policies []ServicePolicy, network discover.Network) (RuleSet, error) {
	ordered := append([]ServicePolicy(nil), policies...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Service.Name < ordered[j].Service.Name
	})
	set := RuleSet{}
	priority := 0
	for _, policy := range ordered {
		if err := policy.Validate(); err != nil {
			return RuleSet{}, err
		}
		for _, source := range policy.Sources {
			cidrs := []string{}
			if source.CIDR != "" {
				cidrs = []string{source.CIDR}
			} else if source.Zone == ZoneLocal {
				cidrs = LocalSubnets(network)
			}
			if len(cidrs) == 0 {
				return RuleSet{}, fmt.Errorf("expand %s: zone %q has no addresses (add an explicit CIDR)", policy.Service.Name, source.Zone)
			}
			for n, cidr := range cidrs {
				priority += 100
				name := fmt.Sprintf("%s-%s", policy.Service.Name, source.Zone)
				if len(cidrs) > 1 {
					name = fmt.Sprintf("%s-%d", name, n+1)
				}
				set.Rules = append(set.Rules, Rule{
					Name:     name,
					Source:   Source{Zone: source.Zone, CIDR: cidr},
					Service:  policy.Service,
					Action:   ActionAccept,
					Priority: priority,
				})
			}
		}
	}
	if err := set.Validate(); err != nil {
		return RuleSet{}, err
	}
	return set, nil
}

// LocalSubnets returns the deduplicated IPv4 networks of up non-loopback
// interfaces, sorted. S222 narrows LAN identification later; expansion
// uses all of them so nothing routable is silently dropped.
func LocalSubnets(network discover.Network) []string {
	seen := map[string]bool{}
	var subnets []string
	for _, iface := range network.Interfaces {
		if !iface.Up || iface.Loopback {
			continue
		}
		for _, addr := range iface.Addresses {
			prefix, err := netip.ParsePrefix(addr.Network)
			if err != nil || !prefix.Addr().Is4() {
				continue
			}
			subnet := prefix.String()
			if !seen[subnet] {
				seen[subnet] = true
				subnets = append(subnets, subnet)
			}
		}
	}
	sort.Strings(subnets)
	return subnets
}

// ManagementBaseline protects the daemon port itself: loopback and LAN
// accepts, then a match-all public drop (public denied, ADR-006).
// Loopback has no interface restriction so local tools verify through the
// guard with unknown ingress interfaces. Overlay acceptance needs overlay
// resolution (S110+) and arrives later; until then remote administration
// rides explicit CIDRs via Expand.
func ManagementBaseline(daemonPort int, network discover.Network) (RuleSet, error) {
	service := Service{Name: "management", Protocol: ProtocolTCP, Port: daemonPort}
	if err := service.Validate(); err != nil {
		return RuleSet{}, err
	}
	set := RuleSet{Rules: []Rule{
		{
			Name:     "management-loopback",
			Source:   Source{Zone: ZoneLocal, CIDR: "127.0.0.0/8"},
			Service:  service,
			Action:   ActionAccept,
			Priority: 50,
		},
		{
			Name:     "management-loopback-v6",
			Source:   Source{Zone: ZoneLocal, CIDR: "::1/128"},
			Service:  service,
			Action:   ActionAccept,
			Priority: 60,
		},
	}}
	priority := 100
	subnets := LocalSubnets(network)
	for i, cidr := range subnets {
		priority += 100
		name := "management-local"
		if len(subnets) > 1 {
			name = fmt.Sprintf("management-local-%d", i+1)
		}
		set.Rules = append(set.Rules, Rule{
			Name:     name,
			Source:   Source{Zone: ZoneLocal, CIDR: cidr},
			Service:  service,
			Action:   ActionAccept,
			Priority: priority,
		})
	}
	set.Rules = append(set.Rules, Rule{
		Name:     "management-public-drop",
		Source:   Source{Zone: ZonePublic},
		Service:  service,
		Action:   ActionDrop,
		Priority: 900,
	})
	if err := set.Validate(); err != nil {
		return RuleSet{}, err
	}
	return set, nil
}

// Render produces the isolated nftables table for a validated ruleset.
// The table holds one base input chain with policy accept: Homestead's
// rules filter only what they match and change nothing else, so
// administrator tables compose safely (ADR-008). No default-drop exists
// here by design — that verdict would break unrelated policy.
func Render(set RuleSet) (string, error) {
	if err := set.Validate(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("# Generated by homestead-server: do not edit. Homestead manages\n")
	out.WriteString("# ONLY the \"homestead\" table below; all other tables are the\n")
	out.WriteString("# administrator's (ADR-008).\n")
	out.WriteString(fmt.Sprintf("table %s %s {\n", TableFamily, HomesteadTable))
	out.WriteString("    chain input {\n")
	out.WriteString("        type filter hook input priority filter; policy accept;\n")
	for _, rule := range set.Sorted() {
		nft, err := renderRule(rule)
		if err != nil {
			return "", err
		}
		out.WriteString(fmt.Sprintf("        # %s\n", rule.Name))
		out.WriteString(fmt.Sprintf("        %s\n", nft))
	}
	out.WriteString("    }\n")
	out.WriteString("}\n")
	return out.String(), nil
}

// renderRule renders one rule verdict line. Interfaces sort for canonical
// stability with live parsing (S053 diffs rendered text).
func renderRule(rule Rule) (string, error) {
	parts := []string{}
	if len(rule.Interfaces) > 0 {
		ifaces := append([]string(nil), rule.Interfaces...)
		sort.Strings(ifaces)
		if len(ifaces) == 1 {
			parts = append(parts, fmt.Sprintf("iifname %q", ifaces[0]))
		} else {
			quoted := make([]string, 0, len(ifaces))
			for _, iface := range ifaces {
				quoted = append(quoted, fmt.Sprintf("%q", iface))
			}
			parts = append(parts, fmt.Sprintf("iifname { %s }", strings.Join(quoted, ", ")))
		}
	}
	if rule.Source.CIDR != "" {
		prefix, err := netip.ParsePrefix(rule.Source.CIDR)
		if err != nil {
			return "", fmt.Errorf("render %s: %w", rule.Name, err)
		}
		family := "ip"
		if !prefix.Addr().Is4() {
			family = "ip6"
		}
		parts = append(parts, fmt.Sprintf("%s saddr %s", family, prefix.String()))
	}
	dport := fmt.Sprintf("%d", rule.Service.Port)
	if rule.Service.EndPort != 0 {
		dport = fmt.Sprintf("%d-%d", rule.Service.Port, rule.Service.EndPort)
	}
	parts = append(parts, fmt.Sprintf("%s dport %s", rule.Service.Protocol, dport))
	parts = append(parts, string(rule.Action))
	return strings.Join(parts, " "), nil
}

package firewall

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// GateTable is the second Homestead-owned nftables table (S421, ADR-055
// §5 amendment). It holds the output and forward drops that keep everything
// but the edge away from the containers of auth-off apps. It is separate
// from the "homestead" input table on purpose: that table is flushed and
// diffed as a whole by the management apply, and its live parser treats any
// rule outside the input grammar as foreign. ADR-008 still holds: Homestead
// manages only tables that carry its own name.
const GateTable = "homestead_gate"

// dockerBridges are the interface name patterns of Docker bridges: the
// default bridge and every user-defined network (br-<id>). They are
// separate rules because wildcards are not reliable inside anonymous sets.
var dockerBridges = []struct{ tag, pattern string }{
	{"docker0", "docker0"},
	{"br", "br-*"},
}

func gateRuleName(kind, tag string) string { return "gate-" + kind + "-" + tag }

// GateRules renders the whole transaction. Matching is by interface, not
// subnet, so it needs no tracking of container or network addresses and
// covers every compose network an app container is on, IPv4 and IPv6 alike
// (the table family is inet).
//
// Output (host-originated): a flow the kernel already DNATed to a published
// port is allowed first, so non-root users keep using published ports on the
// host address; any other packet leaving through a Docker bridge from a
// socket owned by a non-root user is dropped. Root (the daemon, the owner
// under sudo) is untouched, and packets with no socket owner never match.
//
// Forward (routed from outside): DNATed flows are allowed; a new flow
// leaving through a Docker bridge that did not arrive on a Docker bridge is
// dropped. Established flows and container egress are never matched.
func GateRules() string {
	var b strings.Builder
	fmt.Fprintf(&b, "add table %s %s\n", TableFamily, GateTable)
	fmt.Fprintf(&b, "flush table %s %s\n", TableFamily, GateTable)
	fmt.Fprintf(&b, "add chain %s %s output { type filter hook output priority filter; policy accept; }\n", TableFamily, GateTable)
	fmt.Fprintf(&b, "add chain %s %s forward { type filter hook forward priority filter; policy accept; }\n", TableFamily, GateTable)
	for _, br := range dockerBridges {
		fmt.Fprintf(&b, "add rule %s %s output oifname %q ct status dnat accept comment %q\n",
			TableFamily, GateTable, br.pattern, gateRuleName("output-dnat", br.tag))
	}
	for _, br := range dockerBridges {
		fmt.Fprintf(&b, "add rule %s %s output oifname %q meta skuid != 0 drop comment %q\n",
			TableFamily, GateTable, br.pattern, gateRuleName("output-nonroot", br.tag))
	}
	for _, br := range dockerBridges {
		fmt.Fprintf(&b, "add rule %s %s forward oifname %q ct status dnat accept comment %q\n",
			TableFamily, GateTable, br.pattern, gateRuleName("forward-dnat", br.tag))
	}
	for _, br := range dockerBridges {
		fmt.Fprintf(&b, "add rule %s %s forward oifname %q iifname != \"docker0\" iifname != \"br-*\" ct state new drop comment %q\n",
			TableFamily, GateTable, br.pattern, gateRuleName("forward-foreign", br.tag))
	}
	return b.String()
}

// ApplyGate installs the table in one atomic nft transaction, pre-checked,
// then verifies it reads back complete.
func ApplyGate() error {
	path, err := writeTempScript(GateRules())
	if err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := runCmd("nft", "--check", "--file", path); err != nil {
		return fmt.Errorf("firewall: gate rules check failed, nothing applied: %w", err)
	}
	if _, err := runCmd("nft", "--file", path); err != nil {
		return fmt.Errorf("firewall: gate rules apply failed: %w", err)
	}
	live, err := LiveGate()
	if err != nil {
		return fmt.Errorf("firewall: gate rules applied but unreadable: %w", err)
	}
	if !live.Complete {
		return fmt.Errorf("firewall: gate rules applied but live state differs")
	}
	return nil
}

// RemoveGate deletes the table; an absent table is already done.
func RemoveGate() error {
	if _, err := runCmd("nft", "delete", "table", TableFamily, GateTable); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			return nil
		}
		return fmt.Errorf("firewall: remove gate rules: %w", err)
	}
	return nil
}

// GateState is what the live table holds.
type GateState struct {
	Present  bool
	Complete bool
}

// LiveGate reads the live table. A missing table is Present=false;
// Complete means every rule exists with its interface and verdict.
func LiveGate() (GateState, error) {
	out, err := runCmd("nft", "-n", "list", "table", TableFamily, GateTable)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			return GateState{}, nil
		}
		return GateState{}, fmt.Errorf("firewall: list gate table: %w", err)
	}
	return ParseGate(out), nil
}

// nft prints ct status and ct state by name, or numerically under -n
// (dnat is 0x20, new is 0x8, possibly zero padded); both forms are read.
var (
	gateDNAT = regexp.MustCompile(`ct status (dnat|0x0*20)\b`)
	gateNew  = regexp.MustCompile(`ct state (new|0x0*8)\b`)
)

// ParseGate reads `nft list table` text, named or numeric. Each rule is
// found by its comment and must carry its interface match and verdict.
func ParseGate(out string) GateState {
	state := GateState{Present: true}
	lines := strings.Split(out, "\n")
	find := func(name string) string {
		for _, line := range lines {
			if strings.Contains(line, fmt.Sprintf("comment %q", name)) {
				return line
			}
		}
		return ""
	}
	state.Complete = true
	for _, br := range dockerBridges {
		oif := fmt.Sprintf("oifname %q", br.pattern)
		checks := []struct {
			name string
			ok   func(string) bool
		}{
			{gateRuleName("output-dnat", br.tag), func(l string) bool { return gateDNAT.MatchString(l) && strings.Contains(l, "accept") }},
			{gateRuleName("output-nonroot", br.tag), func(l string) bool { return strings.Contains(l, "meta skuid != 0") && strings.Contains(l, "drop") }},
			{gateRuleName("forward-dnat", br.tag), func(l string) bool { return gateDNAT.MatchString(l) && strings.Contains(l, "accept") }},
			{gateRuleName("forward-foreign", br.tag), func(l string) bool {
				return strings.Contains(l, `iifname != "docker0"`) && strings.Contains(l, `iifname != "br-*"`) &&
					gateNew.MatchString(l) && strings.Contains(l, "drop")
			}},
		}
		for _, c := range checks {
			line := find(c.name)
			if line == "" || !strings.Contains(line, oif) || !c.ok(line) {
				state.Complete = false
			}
		}
	}
	return state
}

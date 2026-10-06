package firewall

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Change actions a plan can propose. S054 executes them; S056 shows them.
const (
	// ChangeCreateTable builds the missing homestead table.
	ChangeCreateTable = "create-table"
	// ChangeAdd inserts a desired rule absent live.
	ChangeAdd = "add"
	// ChangeRemove drops a live rule with no desired counterpart,
	// including hand-added rules foreign to the model.
	ChangeRemove = "remove"
	// ChangeUpdate replaces a live rule whose content drifted.
	ChangeUpdate = "update"
)

// Change is one reviewed step of a plan.
type Change struct {
	Action string `json:"action"`
	Rule   string `json:"rule,omitempty"`
	Detail string `json:"detail"`
}

// Plan is the reviewed diff between desired rules and the live table,
// plus the rendered script S054 applies when not in sync.
type Plan struct {
	InSync  bool     `json:"in_sync"`
	Changes []Change `json:"changes"`
	Script  string   `json:"script"`
}

// LiveRule is one live homestead-table rule in canonical form: the same
// match grammar Render emits, so desired and live compare as strings.
// Name comes from the rule comment Render writes; comment-less rules
// (hand-added) become foreign entries proposed for removal.
type LiveRule struct {
	Name      string
	Canonical string
	Foreign   bool
}

// ErrNoTable reports a missing homestead table (fresh host or external
// firewall mode): the plan is create-table, not an error.
var ErrNoTable = errors.New("homestead table absent")

// LiveTable reads the live homestead table via nft. It needs privilege;
// callers without it get an error, never partial data.
func LiveTable() ([]LiveRule, error) {
	out, err := runCmd("nft", "--json", "list", "table", TableFamily, HomesteadTable)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			return nil, ErrNoTable
		}
		return nil, fmt.Errorf("firewall: list live table: %w", err)
	}
	return ParseLiveTable([]byte(out))
}

// Build diffs desired rules against live ones and renders the script S054
// applies when out of sync. A nil live set means the table is missing
// (create-table); an empty one means present but empty (adds).
func Build(desired RuleSet, live []LiveRule) (Plan, error) {
	if err := desired.Validate(); err != nil {
		return Plan{}, err
	}
	script, err := Render(desired)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Script: script}
	if live == nil {
		plan.Changes = []Change{{Action: ChangeCreateTable, Detail: fmt.Sprintf(
			"create table %s %s with %d rules", TableFamily, HomesteadTable, len(desired.Rules))}}
		return plan, nil
	}
	want := map[string]string{}
	for _, rule := range desired.Rules {
		canonical, err := renderRule(rule)
		if err != nil {
			return Plan{}, err
		}
		want[rule.Name] = canonical
	}
	have := map[string]LiveRule{}
	for _, rule := range live {
		if _, dup := have[rule.Name]; dup {
			continue
		}
		have[rule.Name] = rule
	}
	for _, name := range sortedKeys(want) {
		got, ok := have[name]
		switch {
		case !ok:
			plan.Changes = append(plan.Changes, Change{Action: ChangeAdd, Rule: name, Detail: want[name]})
		case got.Canonical != want[name]:
			plan.Changes = append(plan.Changes, Change{Action: ChangeUpdate, Rule: name,
				Detail: fmt.Sprintf("was %q, want %q", got.Canonical, want[name])})
		}
	}
	for _, name := range sortedKeys(have) {
		if _, ok := want[name]; !ok {
			plan.Changes = append(plan.Changes, Change{Action: ChangeRemove, Rule: name, Detail: have[name].Canonical})
		}
	}
	sort.Slice(plan.Changes, func(i, j int) bool {
		if plan.Changes[i].Action != plan.Changes[j].Action {
			return plan.Changes[i].Action < plan.Changes[j].Action
		}
		return plan.Changes[i].Rule < plan.Changes[j].Rule
	})
	plan.InSync = len(plan.Changes) == 0
	return plan, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// liveRuleJSON mirrors the nft rule object; handles and positions never
// enter the canonical form (they change across boots).
type liveRuleJSON struct {
	Family  string            `json:"family"`
	Table   string            `json:"table"`
	Chain   string            `json:"chain"`
	Comment string            `json:"comment"`
	Expr    []json.RawMessage `json:"expr"`
}

type matchJSON struct {
	Left  json.RawMessage `json:"left"`
	Op    string          `json:"op"`
	Right json.RawMessage `json:"right"`
}

// ParseLiveTable normalizes the live homestead table to canonical rules.
// Unknown shapes become foreign entries (proposed for removal) rather than
// errors: hand edits and newer nft versions must surface, never crash.
func ParseLiveTable(data []byte) ([]LiveRule, error) {
	var envelope nftablesJSON
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("firewall: parse live table: %w", err)
	}
	var rules []LiveRule
	for _, entry := range envelope.Nftables {
		raw, ok := entry["rule"]
		if !ok {
			continue
		}
		var rule liveRuleJSON
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, fmt.Errorf("firewall: parse live rule: %w", err)
		}
		canonical, ok := canonicalLiveRule(rule.Expr)
		name := rule.Comment
		foreign := false
		if !ok || name == "" {
			foreign = true
			name = "foreign-" + shortHash(stripped(raw))
			if !ok {
				canonical = string(stripped(raw))
			}
		}
		rules = append(rules, LiveRule{Name: name, Canonical: canonical, Foreign: foreign})
	}
	return rules, nil
}

// stripped removes volatile handle/index fields for stable hashing.
func stripped(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	delete(obj, "handle")
	delete(obj, "index")
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)[:8]
}

// canonicalLiveRule rebuilds the render grammar from rule expressions:
// iifname, saddr, proto dport, verdict — in render order. Anything else
// (counters and unknown shapes) yields ok=false.
func canonicalLiveRule(expr []json.RawMessage) (string, bool) {
	var ifaces []string
	var saddr, proto, dport, verdict string
	for _, raw := range expr {
		var stmt map[string]json.RawMessage
		if err := json.Unmarshal(raw, &stmt); err != nil {
			return "", false
		}
		if _, ok := stmt["accept"]; ok {
			verdict = string(ActionAccept)
			continue
		}
		if _, ok := stmt["drop"]; ok {
			verdict = string(ActionDrop)
			continue
		}
		matchRaw, ok := stmt["match"]
		if !ok {
			continue
		}
		var match matchJSON
		if err := json.Unmarshal(matchRaw, &match); err != nil || match.Op != "==" {
			return "", false
		}
		var left struct {
			Payload *struct {
				Protocol string `json:"protocol"`
				Field    string `json:"field"`
			} `json:"payload"`
			Meta *struct {
				Key string `json:"key"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(match.Left, &left); err != nil {
			return "", false
		}
		switch {
		case left.Meta != nil && left.Meta.Key == "iifname":
			names, ok := stringOrSet(match.Right)
			if !ok {
				return "", false
			}
			ifaces = names
		case left.Payload != nil && left.Payload.Field == "saddr" &&
			(left.Payload.Protocol == "ip" || left.Payload.Protocol == "ip6"):
			cidr, ok := parsePrefix(match.Right)
			if !ok {
				return "", false
			}
			saddr = cidr
		case left.Payload != nil && left.Payload.Field == "dport" &&
			(left.Payload.Protocol == "tcp" || left.Payload.Protocol == "udp"):
			proto = left.Payload.Protocol
			port, ok := parsePort(match.Right)
			if !ok {
				return "", false
			}
			dport = port
		default:
			return "", false
		}
	}
	if proto == "" || dport == "" || verdict == "" {
		return "", false
	}
	parts := []string{}
	if len(ifaces) > 0 {
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
	if saddr != "" {
		family := "ip"
		if strings.Contains(saddr, ":") {
			family = "ip6"
		}
		parts = append(parts, fmt.Sprintf("%s saddr %s", family, saddr))
	}
	parts = append(parts, fmt.Sprintf("%s dport %s", proto, dport))
	parts = append(parts, verdict)
	return strings.Join(parts, " "), true
}

func stringOrSet(raw json.RawMessage) ([]string, bool) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, true
	}
	var set struct {
		Set []string `json:"set"`
	}
	if err := json.Unmarshal(raw, &set); err != nil || len(set.Set) == 0 {
		return nil, false
	}
	return set.Set, true
}

func parsePrefix(raw json.RawMessage) (string, bool) {
	var nested struct {
		Prefix *struct {
			Addr string `json:"addr"`
			Len  int    `json:"len"`
		} `json:"prefix"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && nested.Prefix != nil {
		parsed, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", nested.Prefix.Addr, nested.Prefix.Len))
		if err != nil {
			return "", false
		}
		return parsed.String(), true
	}
	var bare string
	if err := json.Unmarshal(raw, &bare); err != nil {
		return "", false
	}
	addr, err := netip.ParseAddr(bare)
	if err != nil {
		return "", false
	}
	bits := 32
	if addr.Is6() {
		bits = 128
	}
	return netip.PrefixFrom(addr, bits).String(), true
}

func parsePort(raw json.RawMessage) (string, bool) {
	var port float64
	if err := json.Unmarshal(raw, &port); err == nil {
		if port != float64(int(port)) || port < 1 || port > 65535 {
			return "", false
		}
		return fmt.Sprintf("%d", int(port)), true
	}
	var span struct {
		Range []float64 `json:"range"`
	}
	if err := json.Unmarshal(raw, &span); err != nil || len(span.Range) != 2 {
		return "", false
	}
	lo, hi := span.Range[0], span.Range[1]
	if lo != float64(int(lo)) || hi != float64(int(hi)) || lo < 1 || hi > 65535 || lo >= hi {
		return "", false
	}
	return fmt.Sprintf("%d-%d", int(lo), int(hi)), true
}

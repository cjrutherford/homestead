package firewall

import (
	"strings"
	"testing"
)

const liveFixture = `{"nftables": [
  {"metainfo": {"version": "1.0.9"}},
  {"table": {"family": "inet", "name": "homestead"}},
  {"chain": {"family": "inet", "table": "homestead", "name": "input", "handle": 1}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 5,
    "comment": "mgmt-lan",
    "expr": [
      {"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "eth0"}},
      {"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}},
        "right": {"prefix": {"addr": "192.168.1.0", "len": 24}}}},
      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 8080}},
      {"accept": null}
    ]}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 6,
    "comment": "dns-lan",
    "expr": [
      {"counter": {"packets": 10, "bytes": 800}},
      {"match": {"op": "==", "left": {"payload": {"protocol": "udp", "field": "dport"}},
        "right": {"range": [8000, 8100]}}},
      {"accept": null}
    ]}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 7,
    "expr": [
      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 22}},
      {"drop": null}
    ]}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 8,
    "comment": "weird",
    "expr": [{"limit": {"rate": 5}}]}}
]}`

func TestParseLiveTable(t *testing.T) {
	rules, err := ParseLiveTable([]byte(liveFixture))
	if err != nil {
		t.Fatalf("ParseLiveTable: %v", err)
	}
	byName := map[string]LiveRule{}
	for _, rule := range rules {
		byName[rule.Name] = rule
	}
	mgmt, ok := byName["mgmt-lan"]
	if !ok || mgmt.Foreign {
		t.Fatalf("mgmt-lan = %+v, want parsed", mgmt)
	}
	want := `iifname "eth0" ip saddr 192.168.1.0/24 tcp dport 8080 accept`
	if mgmt.Canonical != want {
		t.Fatalf("canonical = %q, want %q", mgmt.Canonical, want)
	}
	dns, ok := byName["dns-lan"]
	if !ok || dns.Foreign {
		t.Fatalf("dns-lan = %+v, want parsed despite counter", dns)
	}
	if !strings.Contains(dns.Canonical, "udp dport 8000-8100 accept") {
		t.Fatalf("canonical = %q", dns.Canonical)
	}
	foreign := 0
	for _, rule := range rules {
		if rule.Foreign {
			foreign++
			if !strings.HasPrefix(rule.Name, "foreign-") {
				t.Fatalf("foreign name = %q", rule.Name)
			}
		}
	}
	if foreign != 2 {
		t.Fatalf("foreign = %d, want 2 (uncommented + unparsable)", foreign)
	}
	if _, err := ParseLiveTable([]byte("nope")); err == nil {
		t.Fatal("garbage accepted, want error")
	}
}

func desiredFixture() RuleSet {
	return RuleSet{Rules: []Rule{
		{
			Name:       "mgmt-lan",
			Interfaces: []string{"eth0"},
			Source:     Source{Zone: ZoneLocal, CIDR: "192.168.1.0/24"},
			Service:    Service{Name: "management", Protocol: ProtocolTCP, Port: 8080},
			Action:     ActionAccept,
			Priority:   100,
		},
		{
			Name:     "dns-lan",
			Source:   Source{Zone: ZoneLocal, CIDR: "10.0.0.0/8"},
			Service:  Service{Name: "dns", Protocol: ProtocolUDP, Port: 8000, EndPort: 8100},
			Action:   ActionAccept,
			Priority: 200,
		},
	}}
}

func TestBuildInSync(t *testing.T) {
	live, err := ParseLiveTable([]byte(`{"nftables": [
	  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 5,
	    "comment": "mgmt-lan",
	    "expr": [
	      {"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "eth0"}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}},
	        "right": {"prefix": {"addr": "192.168.1.0", "len": 24}}}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 8080}},
	      {"accept": null}
	    ]}},
	  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 6,
	    "comment": "dns-lan",
	    "expr": [
	      {"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}},
	        "right": {"prefix": {"addr": "10.0.0.0", "len": 8}}}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "udp", "field": "dport"}},
	        "right": {"range": [8000, 8100]}}},
	      {"accept": null}
	    ]}}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plan, err := Build(desiredFixture(), live)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.InSync || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v, want in sync", plan.Changes)
	}
	if !strings.Contains(plan.Script, "table inet homestead") {
		t.Fatal("script missing table")
	}
}

func TestBuildMissingTable(t *testing.T) {
	plan, err := Build(desiredFixture(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.InSync || len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeCreateTable {
		t.Fatalf("plan = %+v, want single create-table", plan.Changes)
	}
}

func TestBuildDiffs(t *testing.T) {
	live, err := ParseLiveTable([]byte(liveFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plan, err := Build(desiredFixture(), live)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.InSync {
		t.Fatal("plan in sync, want diffs")
	}
	actions := map[string]string{}
	for _, change := range plan.Changes {
		actions[change.Rule] = change.Action
	}
	// dns-lan differs (fixture /8 vs live range-only... actually live dns
	// lacks saddr while desired has 10.0.0.0/8) -> update.
	if actions["dns-lan"] != ChangeUpdate {
		t.Fatalf("dns-lan = %q, want update", actions["dns-lan"])
	}
	// Live port-22 drop and weird rule are foreign -> remove.
	removes := 0
	for _, change := range plan.Changes {
		if change.Action == ChangeRemove {
			removes++
		}
	}
	if removes != 2 {
		t.Fatalf("removes = %d (%v), want 2", removes, plan.Changes)
	}
}

func TestBuildRejectsInvalid(t *testing.T) {
	if _, err := Build(RuleSet{Rules: []Rule{{Name: "x"}}}, nil); err == nil {
		t.Fatal("invalid desired accepted, want error")
	}
}

func TestParseSetAndIPv6(t *testing.T) {
	rules, err := ParseLiveTable([]byte(`{"nftables": [
	  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 9,
	    "comment": "v6multi",
	    "expr": [
	      {"match": {"op": "==", "left": {"meta": {"key": "iifname"}},
	        "right": {"set": ["wlan0", "eth0"]}}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "ip6", "field": "saddr"}},
	        "right": {"prefix": {"addr": "2001:db8::", "len": 32}}}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 443}},
	      {"accept": null}
	    ]}}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rules) != 1 || rules[0].Foreign {
		t.Fatalf("rules = %+v", rules)
	}
	want := `iifname { "eth0", "wlan0" } ip6 saddr 2001:db8::/32 tcp dport 443 accept`
	if rules[0].Canonical != want {
		t.Fatalf("canonical = %q, want %q", rules[0].Canonical, want)
	}
}

func TestRenderedRulesRoundTrip(t *testing.T) {
	// Regression: rendered rules once carried no comment, so live
	// parses marked every own rule foreign and verification never
	// converged. Rendered output must name itself, and Build must
	// find a comment-carrying live table in sync.
	rule := Rule{
		Name:       "mgmt-lan",
		Interfaces: []string{"eth0"},
		Source:     Source{Zone: ZoneLocal, CIDR: "192.168.1.0/24"},
		Service:    Service{Name: "management", Protocol: ProtocolTCP, Port: 8080},
		Action:     ActionAccept,
		Priority:   100,
	}
	rendered, err := renderRule(rule)
	if err != nil {
		t.Fatalf("renderRule: %v", err)
	}
	if !strings.HasSuffix(rendered, `comment "mgmt-lan"`) {
		t.Fatalf("rendered = %q, want name comment", rendered)
	}
	live, err := ParseLiveTable([]byte(`{"nftables": [
	  {"rule": {"family": "inet", "table": "homestead", "chain": "input",
	    "comment": "mgmt-lan",
	    "expr": [
	      {"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "eth0"}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}},
	        "right": {"prefix": {"addr": "192.168.1.0", "len": 24}}}},
	      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 8080}},
	      {"accept": null}
	    ]}}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plan, err := Build(RuleSet{Rules: []Rule{rule}}, live)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !plan.InSync {
		t.Fatalf("changes = %+v, want in sync", plan.Changes)
	}
}

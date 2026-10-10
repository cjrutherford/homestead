package firewall

import (
	"errors"
	"os"
	"strings"
	"testing"
)

type callLog struct {
	calls   []string
	scripts []string
}

func (l *callLog) run(outputs map[string]struct {
	out string
	err error
}) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		if name == "nft" && len(args) == 2 && args[0] == "--file" {
			// Apply removes its temp script on return; capture content now.
			if data, err := os.ReadFile(args[1]); err == nil {
				l.scripts = append(l.scripts, string(data))
			}
		}
		for prefix, out := range outputs {
			if strings.HasPrefix(key, prefix) || key == prefix {
				l.calls = append(l.calls, key)
				return out.out, out.err
			}
		}
		// File paths vary (--file <tmp>); match by command head.
		head := name
		if len(args) > 0 {
			head += " " + args[0]
		}
		if out, ok := outputs[head]; ok {
			l.calls = append(l.calls, key)
			return out.out, out.err
		}
		return "", errors.New("unexpected command: " + key)
	}
}

func stubApply(t *testing.T, outputs map[string]struct {
	out string
	err error
},
) *callLog {
	t.Helper()
	log := &callLog{}
	old := runCmd
	runCmd = log.run(outputs)
	t.Cleanup(func() { runCmd = old })
	return log
}

func exampleSet() RuleSet {
	return RuleSet{Rules: []Rule{
		{
			Name:     "mgmt-lan",
			Source:   Source{Zone: ZoneLocal, CIDR: "192.168.1.0/24"},
			Service:  Service{Name: "management", Protocol: ProtocolTCP, Port: 8080},
			Action:   ActionAccept,
			Priority: 100,
		},
		{
			Name:     "mgmt-public-drop",
			Source:   Source{Zone: ZonePublic},
			Service:  Service{Name: "management", Protocol: ProtocolTCP, Port: 8080},
			Action:   ActionDrop,
			Priority: 900,
		},
	}}
}

func TestBuildApplyScriptExact(t *testing.T) {
	got, err := BuildApplyScript(exampleSet())
	if err != nil {
		t.Fatalf("BuildApplyScript: %v", err)
	}
	want := `add table inet homestead
flush table inet homestead
add chain inet homestead input { type filter hook input priority filter; policy accept; }
add rule inet homestead input ip saddr 192.168.1.0/24 tcp dport 8080 accept comment "mgmt-lan"
add rule inet homestead input tcp dport 8080 drop comment "mgmt-public-drop"
`
	if got != want {
		t.Fatalf("script:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildApplyScriptRejectsInvalid(t *testing.T) {
	if _, err := BuildApplyScript(RuleSet{Rules: []Rule{{Name: "x"}}}); err == nil {
		t.Fatal("invalid set accepted, want error")
	}
}

const appliedLiveJSON = `{"nftables": [
  {"table": {"family": "inet", "name": "homestead"}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 5,
    "comment": "mgmt-lan",
    "expr": [
      {"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}},
        "right": {"prefix": {"addr": "192.168.1.0", "len": 24}}}},
      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 8080}},
      {"accept": null}
    ]}},
  {"rule": {"family": "inet", "table": "homestead", "chain": "input", "handle": 6,
    "comment": "mgmt-public-drop",
    "expr": [
      {"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 8080}},
      {"drop": null}
    ]}}
]}`

func okOutputs() map[string]struct {
	out string
	err error
} {
	return map[string]struct {
		out string
		err error
	}{
		"nft --check": {"", nil},
		"nft --file":  {"", nil},
		"nft --json":  {appliedLiveJSON, nil},
	}
}

func TestApplySetSuccess(t *testing.T) {
	log := stubApply(t, okOutputs())
	if err := ApplySet(exampleSet()); err != nil {
		t.Fatalf("ApplySet: %v", err)
	}
	stages := []string{}
	for _, call := range log.calls {
		switch {
		case strings.HasPrefix(call, "nft --check"):
			stages = append(stages, "check")
		case strings.HasPrefix(call, "nft --file"):
			stages = append(stages, "apply")
		case strings.HasPrefix(call, "nft --json"):
			stages = append(stages, "verify")
		}
	}
	if len(stages) != 3 || stages[0] != "check" || stages[1] != "apply" || stages[2] != "verify" {
		t.Fatalf("stages = %v, want check/apply/verify", stages)
	}
	if len(log.scripts) != 1 {
		t.Fatalf("scripts captured = %d, want the single transaction file", len(log.scripts))
	}
	for _, script := range log.scripts {
		if !strings.Contains(script, "flush table inet homestead") {
			t.Fatalf("script missing scoped flush:\n%s", script)
		}
	}
}

func TestApplySetCheckFailureAborts(t *testing.T) {
	log := stubApply(t, map[string]struct {
		out string
		err error
	}{
		"nft --check": {"", errors.New("exit status 1: syntax error")},
		"nft --file":  {"", nil},
		"nft --json":  {appliedLiveJSON, nil},
	})
	if err := ApplySet(exampleSet()); err == nil {
		t.Fatal("check failure accepted, want error")
	}
	for _, call := range log.calls {
		if strings.HasPrefix(call, "nft --file") {
			t.Fatalf("apply ran after failed check: %v", log.calls)
		}
	}
}

func TestApplySetApplyFailure(t *testing.T) {
	stubApply(t, map[string]struct {
		out string
		err error
	}{
		"nft --check": {"", nil},
		"nft --file":  {"", errors.New("exit status 1: commit failed")},
		"nft --json":  {appliedLiveJSON, nil},
	})
	if err := ApplySet(exampleSet()); err == nil {
		t.Fatal("apply failure accepted, want error")
	}
}

func TestApplySetVerifyMismatch(t *testing.T) {
	stubApply(t, map[string]struct {
		out string
		err error
	}{
		"nft --check": {"", nil},
		"nft --file":  {"", nil},
		"nft --json":  {`{"nftables": []}`, nil},
	})
	err := ApplySet(exampleSet())
	if err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("err = %v, want differs error", err)
	}
}

func TestApplySetInvalidSkipsCommands(t *testing.T) {
	log := stubApply(t, okOutputs())
	if err := ApplySet(RuleSet{Rules: []Rule{{Name: "x"}}}); err == nil {
		t.Fatal("invalid set accepted, want error")
	}
	if len(log.calls) != 0 {
		t.Fatalf("commands ran for invalid set: %v", log.calls)
	}
}

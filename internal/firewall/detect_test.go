package firewall

import (
	"errors"
	"testing"
)

func stubCommands(t *testing.T, look func(string) (string, error), run func(string, ...string) (string, error)) {
	t.Helper()
	oldLook, oldRun := lookPath, runCmd
	lookPath, runCmd = look, run
	t.Cleanup(func() {
		lookPath, runCmd = oldLook, oldRun
	})
}

func TestNoBinaryMeansUnavailable(t *testing.T) {
	stubCommands(t,
		func(string) (string, error) { return "", errors.New("not found") },
		func(string, ...string) (string, error) { t.Fatal("no commands should run"); return "", nil },
	)
	state := Detect()
	if state.NFTAvailable || state.RulesetAccessible || state.IPTablesAvailable {
		t.Fatalf("state = %+v, want all unavailable", state)
	}
}

const rulesetFixture = `{"nftables": [
  {"metainfo": {"version": "1.0.9"}},
  {"table": {"family": "inet", "name": "filter", "handle": 1}},
  {"table": {"family": "ip", "name": "homestead", "handle": 2}},
  {"chain": {"family": "inet", "table": "filter", "name": "input"}},
  {"rule": {"family": "inet", "table": "filter", "chain": "input"}}
]}`

func TestFullDetection(t *testing.T) {
	stubCommands(t,
		func(string) (string, error) { return "/usr/sbin/nft", nil },
		func(name string, args ...string) (string, error) {
			switch name + " " + args[0] {
			case "nft --version":
				return "nftables v1.0.9 (Old Doc Yak #3)\n", nil
			case "nft --json":
				return rulesetFixture, nil
			case "iptables --version":
				return "iptables v1.8.10 (nf_tables)\n", nil
			}
			t.Fatalf("unexpected command %s %v", name, args)
			return "", nil
		},
	)
	state := Detect()
	if !state.NFTAvailable || state.NFTVersion != "1.0.9" {
		t.Fatalf("nft = %+v", state)
	}
	if !state.RulesetAccessible || state.RulesetError != "" {
		t.Fatalf("ruleset = %+v", state)
	}
	if len(state.Tables) != 2 || !state.HasHomesteadTable {
		t.Fatalf("tables = %+v", state.Tables)
	}
	if !state.IPTablesAvailable || state.IPTablesBackend != "nf_tables" {
		t.Fatalf("iptables = %+v", state)
	}
}

func TestPermissionDeniedIsInaccessible(t *testing.T) {
	stubCommands(t,
		func(string) (string, error) { return "/usr/sbin/nft", nil },
		func(name string, args ...string) (string, error) {
			if args[0] == "--version" {
				return "nftables v1.0.9\n", nil
			}
			return "", errors.New("exit status 1: Error: Could not process rule: Operation not permitted")
		},
	)
	state := Detect()
	if !state.NFTAvailable || state.RulesetAccessible || state.RulesetError != "" {
		t.Fatalf("state = %+v, want available but unlistable", state)
	}
}

func TestGarbageRulesetRecorded(t *testing.T) {
	stubCommands(t,
		func(string) (string, error) { return "/usr/sbin/nft", nil },
		func(name string, args ...string) (string, error) {
			if args[0] == "--version" {
				return "nftables v1.0.9\n", nil
			}
			return "not json", nil
		},
	)
	state := Detect()
	if state.RulesetAccessible || state.RulesetError == "" {
		t.Fatalf("state = %+v, want recorded error", state)
	}
}

func TestVersionBackends(t *testing.T) {
	if got := parseNFTVersion("nftables v0.9.3 (Topsy)\n"); got != "0.9.3" {
		t.Fatalf("version = %q", got)
	}
	for output, want := range map[string]string{
		"iptables v1.8.10 (legacy)\n":    "legacy",
		"iptables v1.8.10 (nf_tables)\n": "nf_tables",
		"weird output\n":                 "unknown",
	} {
		if got := parseIPTablesBackend(output); got != want {
			t.Fatalf("backend %q = %q, want %q", output, got, want)
		}
	}
}

func TestLiveDetectionNeverFails(t *testing.T) {
	state := Detect()
	if state.NFTAvailable && state.NFTVersion == "" && state.RulesetError == "" && !state.RulesetAccessible {
		t.Logf("nft present but version unreadable: %+v", state)
	}
	t.Logf("nft=%v accessible=%v tables=%d iptables=%v(%s)",
		state.NFTAvailable, state.RulesetAccessible, len(state.Tables),
		state.IPTablesAvailable, state.IPTablesBackend)
}

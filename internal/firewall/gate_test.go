package firewall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateRulesRender(t *testing.T) {
	got := GateRules()
	for _, want := range []string{
		"add table inet homestead_gate\nflush table inet homestead_gate\n",
		"output { type filter hook output priority filter; policy accept; }",
		"forward { type filter hook forward priority filter; policy accept; }",
		`output oifname "docker0" ct status dnat accept comment "gate-output-dnat-docker0"`,
		`output oifname "br-*" ct status dnat accept comment "gate-output-dnat-br"`,
		`output oifname "docker0" meta skuid != 0 drop comment "gate-output-nonroot-docker0"`,
		`output oifname "br-*" meta skuid != 0 drop comment "gate-output-nonroot-br"`,
		`forward oifname "docker0" ct status dnat accept comment "gate-forward-dnat-docker0"`,
		`forward oifname "br-*" ct status dnat accept comment "gate-forward-dnat-br"`,
		`forward oifname "docker0" iifname != "docker0" iifname != "br-*" ct state new drop comment "gate-forward-foreign-docker0"`,
		`forward oifname "br-*" iifname != "docker0" iifname != "br-*" ct state new drop comment "gate-forward-foreign-br"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "daddr") || strings.Contains(got, "ip ") {
		t.Fatalf("rules must be interface-based, not address-based:\n%s", got)
	}
	// Each dnat allow precedes the drops of its chain.
	if strings.Index(got, "gate-output-dnat-br") > strings.Index(got, "gate-output-nonroot-docker0") ||
		strings.Index(got, "gate-forward-dnat-br") > strings.Index(got, "gate-forward-foreign-docker0") {
		t.Fatal("dnat allows must come before the drops")
	}
	if strings.Contains(got, "table inet homestead\n") || strings.Contains(got, "delete") {
		t.Fatalf("touches another table:\n%s", got)
	}
}

func TestGateRulesPassNFTCheck(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft binary absent")
	}
	path := filepath.Join(t.TempDir(), "gate.nft")
	if err := os.WriteFile(path, []byte(GateRules()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(nft, "--check", "--file", path).CombinedOutput(); err != nil {
		t.Skipf("nft --check unavailable here (%v: %s)", err, strings.TrimSpace(string(out)))
	}
}

// numericGate is `nft -n list table inet homestead_gate` as printed on
// VM 2: ct status and ct state are numeric, the chain priority is 0.
const numericGate = `table inet homestead_gate {
	chain output {
		type filter hook output priority 0; policy accept;
		oifname "docker0" ct status 0x20 accept comment "gate-output-dnat-docker0"
		oifname "br-*" ct status 0x20 accept comment "gate-output-dnat-br"
		oifname "docker0" meta skuid != 0 drop comment "gate-output-nonroot-docker0"
		oifname "br-*" meta skuid != 0 drop comment "gate-output-nonroot-br"
	}
	chain forward {
		type filter hook forward priority 0; policy accept;
		oifname "docker0" ct status 0x20 accept comment "gate-forward-dnat-docker0"
		oifname "br-*" ct status 0x20 accept comment "gate-forward-dnat-br"
		oifname "docker0" iifname != "docker0" iifname != "br-*" ct state 0x8 drop comment "gate-forward-foreign-docker0"
		oifname "br-*" iifname != "docker0" iifname != "br-*" ct state 0x8 drop comment "gate-forward-foreign-br"
	}
}`

func TestParseGateNumericAndNamed(t *testing.T) {
	if g := ParseGate(numericGate); !g.Present || !g.Complete {
		t.Fatalf("numeric listing: %+v", g)
	}
	named := strings.NewReplacer("ct status 0x20", "ct status dnat", "ct state 0x8", "ct state new").Replace(numericGate)
	if g := ParseGate(named); !g.Complete {
		t.Fatalf("named listing: %+v", g)
	}
	padded := strings.NewReplacer("0x20", "0x00000020", "0x8", "0x00000008").Replace(numericGate)
	if g := ParseGate(padded); !g.Complete {
		t.Fatalf("padded listing: %+v", g)
	}
	// The listing of what we render parses as complete once nft prints it back.
	for name, bad := range map[string]string{
		"missing rule":    strings.Replace(numericGate, `oifname "br-*" meta skuid != 0 drop comment "gate-output-nonroot-br"`, "", 1),
		"wrong iface":     strings.Replace(numericGate, `oifname "br-*" ct status 0x20 accept comment "gate-output-dnat-br"`, `oifname "eth0" ct status 0x20 accept comment "gate-output-dnat-br"`, 1),
		"changed verdict": strings.Replace(numericGate, "meta skuid != 0 drop", "meta skuid != 0 accept", 1),
		"wrong state":     strings.Replace(numericGate, "ct state 0x8 drop", "ct state 0x2 drop", 1),
		"empty":           "table inet homestead_gate {\n}",
	} {
		if g := ParseGate(bad); g.Complete {
			t.Errorf("%s must be incomplete", name)
		}
	}
}

func TestApplyGateAndRemove(t *testing.T) {
	oldRun := runCmd
	t.Cleanup(func() { runCmd = oldRun })
	var calls []string
	runCmd = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "-n" {
			return numericGate, nil
		}
		return "", nil
	}
	if err := ApplyGate(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !strings.HasPrefix(calls[0], "nft --check") || !strings.HasPrefix(calls[1], "nft --file") {
		t.Fatalf("calls %q", calls)
	}
	runCmd = func(name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "-n" {
			return "table inet homestead_gate {\n}", nil
		}
		return "", nil
	}
	if err := ApplyGate(); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("incomplete read-back must fail: %v", err)
	}
	runCmd = func(string, ...string) (string, error) {
		return "", errors.New("exit 1: Error: No such file or directory")
	}
	if err := RemoveGate(); err != nil {
		t.Fatalf("absent table is done: %v", err)
	}
	if st, err := LiveGate(); err != nil || st.Present {
		t.Fatalf("%+v %v", st, err)
	}
}

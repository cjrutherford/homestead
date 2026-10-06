package firewall

import (
	"errors"
	"strings"
	"testing"
)

func adminConn() Connection {
	return Connection{SourceIP: "192.168.1.5", Interface: "eth0", Protocol: ProtocolTCP, Port: 8080}
}

func guardSet() RuleSet {
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
			Name:     "mgmt-public-drop",
			Source:   Source{Zone: ZonePublic},
			Service:  Service{Name: "management", Protocol: ProtocolTCP, Port: 8080},
			Action:   ActionDrop,
			Priority: 900,
		},
	}}
}

func TestGuardAcceptsCoveredPath(t *testing.T) {
	if err := Guard(guardSet(), adminConn()); err != nil {
		t.Fatalf("Guard: %v", err)
	}
}

func TestGuardBlocksDroppedPath(t *testing.T) {
	conn := adminConn()
	conn.SourceIP = "203.0.113.9"
	conn.Interface = ""
	if err := Guard(guardSet(), conn); err == nil {
		t.Fatal("public path accepted, want lockout error")
	} else if !strings.Contains(err.Error(), "mgmt-public-drop") {
		t.Fatalf("err = %v, want naming the rule", err)
	}
}

func TestGuardDefaultAccept(t *testing.T) {
	empty := RuleSet{}
	if err := Guard(empty, adminConn()); err != nil {
		t.Fatalf("empty set: %v (chain default accepts)", err)
	}
}

func TestGuardMatchDetails(t *testing.T) {
	conn := adminConn()
	conn.Interface = "wlan0"
	// mgmt-lan is eth0-bound (no match); the match-all public drop below
	// it shadows: first match wins, even across interfaces.
	if err := Guard(guardSet(), conn); err == nil {
		t.Fatal("shadowed path accepted, want lockout error")
	}
	conn = adminConn()
	conn.Port = 9090
	if err := Guard(guardSet(), conn); err != nil {
		t.Fatalf("other port unmatched: %v", err)
	}
	ranged := guardSet()
	ranged.Rules[0].Service.EndPort = 8100
	conn = adminConn()
	conn.Port = 8050
	if err := Guard(ranged, conn); err != nil {
		t.Fatalf("range: %v", err)
	}
}

func TestGuardValidatesInputs(t *testing.T) {
	bad := adminConn()
	bad.SourceIP = "not-an-ip"
	if err := Guard(guardSet(), bad); err == nil {
		t.Fatal("bad IP accepted")
	}
	bad = adminConn()
	bad.Port = 0
	if err := Guard(guardSet(), bad); err == nil {
		t.Fatal("bad port accepted")
	}
	if err := Guard(RuleSet{Rules: []Rule{{Name: "x"}}}, adminConn()); err == nil {
		t.Fatal("invalid set accepted")
	}
}

func TestApplyGuardedAbortsBeforeCommands(t *testing.T) {
	log := stubFirewall(t, map[string]struct {
		out string
		err error
	}{})
	conn := adminConn()
	conn.SourceIP = "203.0.113.9"
	conn.Interface = ""
	if err := ApplyGuarded(guardSet(), conn); err == nil {
		t.Fatal("blocked path applied, want error")
	}
	if len(log.calls) != 0 {
		t.Fatalf("commands ran despite guard: %v", log.calls)
	}
}

func stubFirewall(t *testing.T, outputs map[string]struct {
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

func TestSnapshotMissingIsEmpty(t *testing.T) {
	stubFirewall(t, map[string]struct {
		out string
		err error
	}{
		"nft list": {"", errors.New("exit status 1: Error: No such file or directory")},
	})
	snap, err := TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if snap.Exists {
		t.Fatal("missing table reported existing")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	const script = "table inet homestead {\n}\n"
	log := &callLog{}
	old := runCmd
	runCmd = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		log.calls = append(log.calls, key)
		switch {
		case strings.HasPrefix(key, "nft list"):
			return script, nil
		case strings.HasPrefix(key, "nft --check"), strings.HasPrefix(key, "nft --file"):
			return "", nil
		}
		return "", errors.New("unexpected: " + key)
	}
	t.Cleanup(func() { runCmd = old })
	snap, err := TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if !snap.Exists || snap.Script != script {
		t.Fatalf("snap = %+v", snap)
	}
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	stages := []string{}
	for _, call := range log.calls[1:] {
		switch {
		case strings.HasPrefix(call, "nft --check"):
			stages = append(stages, "check")
		case strings.HasPrefix(call, "nft --file"):
			stages = append(stages, "apply")
		case strings.HasPrefix(call, "nft list"):
			stages = append(stages, "relist")
		default:
			t.Fatalf("unexpected call %q", call)
		}
	}
	if len(stages) != 3 || stages[0] != "check" || stages[1] != "apply" || stages[2] != "relist" {
		t.Fatalf("stages = %v, want check/apply/relist", stages)
	}
}

func TestRestoreMissingRemovesTable(t *testing.T) {
	calls := []string{}
	old := runCmd
	runCmd = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", errors.New("exit status 1: Error: No such file or directory")
	}
	t.Cleanup(func() { runCmd = old })
	if err := (Snapshot{}).Restore(); err != nil {
		t.Fatalf("Restore missing: %v", err)
	}
	if len(calls) != 1 || calls[0] != "nft delete table inet homestead" {
		t.Fatalf("calls = %v, want single scoped delete", calls)
	}
}

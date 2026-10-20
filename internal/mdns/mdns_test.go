package mdns

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"homestead/internal/discover"
)

// buildQuery encodes one question message for tests.
func buildQuery(id uint16, name string, qtype uint16, qu bool) []byte {
	msg := []byte{byte(id >> 8), byte(id), 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	msg = encodeName(msg, name)
	class := uint16(typeIN)
	if qu {
		class |= quBit
	}
	msg = append(msg, byte(qtype>>8), byte(qtype), byte(class>>8), byte(class))
	return msg
}

// answerCount reads ANCOUNT/ARCOUNT from a response header.
func answerCount(t *testing.T, resp []byte) (int, int) {
	t.Helper()
	if len(resp) < 12 || resp[2]&0x80 == 0 {
		t.Fatalf("not a response: %x", resp)
	}
	return int(resp[6])<<8 | int(resp[7]), int(resp[10])<<8 | int(resp[11])
}

// link builds a LinkAddr from "ip/len" on iface.
func link(cidr, iface string) LinkAddr {
	p := netip.MustParsePrefix(cidr)
	return LinkAddr{Addr: p.Addr(), Prefix: p.Masked(), Interface: iface}
}

func testConfig() Config {
	return Config{
		Hostname: "homestead",
		Port:     8080,
		Version:  "0.0.0-test",
		State:    func() string { return "UNCLAIMED" },
		Addresses: func() []LinkAddr {
			return []LinkAddr{link("192.168.1.10/24", "eth0"), link("192.168.1.11/24", "eth0")}
		},
	}
}

func TestConfigValidate(t *testing.T) {
	if err := testConfig().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bad := testConfig()
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Hostname = "Bad Name!" },
		func(c *Config) { c.Hostname = "" },
		func(c *Config) { c.Port = 0 },
		func(c *Config) { c.Port = 70000 },
		func(c *Config) { c.State = nil },
		func(c *Config) { c.Addresses = nil },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	_ = bad
}

func TestParseQuestions(t *testing.T) {
	questions, err := parseQuestions(buildQuery(7, "homestead.local", typeA, false))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(questions) != 1 || questions[0].Name != "homestead.local" || questions[0].Type != typeA || questions[0].QU {
		t.Fatalf("questions = %+v", questions)
	}
	questions, err = parseQuestions(buildQuery(7, "_homestead._tcp.local", typePTR, true))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !questions[0].QU {
		t.Fatalf("questions = %+v", questions)
	}
	for name, msg := range map[string][]byte{
		"truncated":   {0, 7},
		"response":    {0, 7, 0x84, 0, 0, 1, 0, 0, 0, 0, 0, 0},
		"no-question": {0, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"compressed":  {0, 7, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1},
	} {
		if _, err := parseQuestions(msg); err == nil {
			t.Fatalf("%s parsed", name)
		}
	}
}

func TestAnswerA(t *testing.T) {
	cfg := testConfig()
	resp, unicast := respond(buildQuery(9, "homestead.local", typeA, false), cfg, netip.Addr{})
	if resp == nil || unicast {
		t.Fatal("no multicast answer")
	}
	answers, additionals := answerCount(t, resp)
	if answers != 2 || additionals != 0 {
		t.Fatalf("counts = %d/%d", answers, additionals)
	}
	for _, want := range []string{"homestead", "192.168.1.10", "192.168.1.11"} {
		found := false
		for _, ip := range []string{"\xc0\xa8\x01\x0a", "\xc0\xa8\x01\x0b"} {
			if strings.Contains(string(resp), ip) {
				found = true
			}
		}
		if want == "homestead" {
			found = strings.Contains(string(resp), "homestead")
		}
		if !found {
			t.Fatalf("answer missing %q", want)
		}
	}
	// QU questions answer unicast.
	resp, unicast = respond(buildQuery(9, "homestead.local", typeA, true), cfg, netip.Addr{})
	if resp == nil || !unicast {
		t.Fatal("no unicast answer")
	}
}

func TestAnswerService(t *testing.T) {
	cfg := testConfig()
	resp, _ := respond(buildQuery(3, "_homestead._tcp.local", typePTR, false), cfg, netip.Addr{})
	if resp == nil {
		t.Fatal("no answer")
	}
	answers, additionals := answerCount(t, resp)
	if answers != 1 || additionals != 2 {
		t.Fatalf("counts = %d/%d", answers, additionals)
	}
	if !strings.Contains(string(resp), "_homestead") || !strings.Contains(string(resp), "homestead") {
		t.Fatal("PTR target missing")
	}
	resp, _ = respond(buildQuery(3, "homestead._homestead._tcp.local", typeSRV, false), cfg, netip.Addr{})
	if resp == nil {
		t.Fatal("no SRV answer")
	}
	if !strings.Contains(string(resp), "\x1f\x90") { // 8080
		t.Fatal("SRV port missing")
	}
	resp, _ = respond(buildQuery(3, "homestead._homestead._tcp.local", typeTXT, false), cfg, netip.Addr{})
	if resp == nil {
		t.Fatal("no TXT answer")
	}
	if !strings.Contains(string(resp), "state=UNCLAIMED") {
		t.Fatal("TXT state missing")
	}
}

func TestAnswerSilent(t *testing.T) {
	cfg := testConfig()
	for name, msg := range map[string][]byte{
		"unknown name":  buildQuery(1, "stranger.local", typeA, false),
		"unknown type":  buildQuery(1, "homestead.local", 99, false),
		"truncated":     {0, 1},
		"empty service": buildQuery(1, "_homestead._tcp.local", typePTR, false),
	} {
		question := msg
		if name == "empty service" {
			empty := testConfig()
			empty.Addresses = func() []LinkAddr { return nil }
			if resp, _ := respond(question, empty, netip.Addr{}); resp != nil {
				t.Fatalf("%s answered without addresses", name)
			}
			continue
		}
		if resp, _ := respond(question, cfg, netip.Addr{}); resp != nil {
			t.Fatalf("%s answered", name)
		}
	}
}

func TestLANIPv4(t *testing.T) {
	network := discover.Network{Interfaces: []discover.Interface{
		{Name: "eth0", Up: true, Addresses: []discover.Address{
			{IP: "192.168.1.10"}, {IP: "fe80::1"}, {IP: "169.254.5.5"},
		}},
		{Name: "down0", Up: false, Addresses: []discover.Address{{IP: "10.0.0.5"}}},
		{Name: "lo", Up: true, Loopback: true, Addresses: []discover.Address{{IP: "127.0.0.1"}}},
	}}
	addrs := LANIPv4(network)
	if len(addrs) != 1 || addrs[0].Addr != netip.MustParseAddr("192.168.1.10") || addrs[0].Interface != "eth0" {
		t.Fatalf("addrs = %v", addrs)
	}
}

// TestRoundTrip serves loopback and queries it directly: full
// socket path (parse, match, respond, write) with no multicast
// routing involved. A taken loopback port skips instead of failing.
func TestRoundTrip(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("no interfaces")
	}
	var loopback *net.Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			candidate := iface
			loopback = &candidate
			break
		}
	}
	if loopback == nil {
		t.Skip("no loopback interface")
	}
	cfg := testConfig()
	cfg.Addresses = func() []LinkAddr { return []LinkAddr{link("192.168.1.10/24", "lo")} }
	groupAddr := &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}
	conn, err := listenOn(*loopback, groupAddr)
	if err != nil {
		t.Skipf("loopback port taken: %v", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(ctx, cfg, []linkConn{{iface: loopback.Name, conn: conn}}, groupAddr)
	}()
	defer func() {
		stop()
		<-done
	}()
	time.Sleep(200 * time.Millisecond)
	// QU query over unicast: the answer returns unicast, so no group
	// membership is needed to read it.
	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353})
	if err != nil {
		t.Skipf("dial loopback: %v", err)
	}
	defer client.Close()
	if _, err := client.Write(buildQuery(77, "homestead.local", typeA, true)); err != nil {
		t.Fatalf("send query: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	buf := make([]byte, 9000)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("no answer on the wire: %v", err)
	}
	resp := buf[:n]
	if len(resp) < 3 || resp[2]&0x80 == 0 || resp[0] != 0 || resp[1] != 77 {
		t.Fatalf("not our response: %x", resp)
	}
	answers, _ := answerCount(t, resp)
	if answers != 1 {
		t.Fatalf("answers = %d", answers)
	}
	if !strings.Contains(string(resp), "\xc0\xa8\x01\x0a") {
		t.Fatal("address missing from answer")
	}
}

func TestLANIPv4ExcludesDockerBridges(t *testing.T) {
	network := discover.Network{Interfaces: []discover.Interface{
		{Name: "eth0", Up: true, Addresses: []discover.Address{{IP: "192.168.1.10", Network: "192.168.1.0/24"}}},
		{Name: "docker0", Up: true, Addresses: []discover.Address{{IP: "172.17.0.1", Network: "172.17.0.0/16"}}},
		{Name: "br-1a2b3c", Up: true, Addresses: []discover.Address{{IP: "172.18.0.1", Network: "172.18.0.0/16"}}},
		{Name: "veth9f", Up: true, Addresses: []discover.Address{{IP: "172.19.0.1", Network: "172.19.0.0/16"}}},
	}}
	addrs := LANIPv4(network)
	if len(addrs) != 1 || addrs[0].Interface != "eth0" || addrs[0].Prefix != netip.MustParsePrefix("192.168.1.0/24") {
		t.Fatalf("addrs = %v", addrs)
	}
}

func multiNIC() Config {
	cfg := testConfig()
	cfg.Addresses = func() []LinkAddr {
		return []LinkAddr{link("10.0.2.15/24", "ens3"), link("192.168.1.158/24", "lan0")}
	}
	return cfg
}

func TestRespondOnlyOnQueryLink(t *testing.T) {
	resp, _ := respond(buildQuery(1, "homestead.local", typeA, false), multiNIC(), netip.MustParseAddr("192.168.1.50"))
	if resp == nil {
		t.Fatal("no answer")
	}
	if n, _ := answerCount(t, resp); n != 1 {
		t.Fatalf("answers = %d", n)
	}
	if !strings.Contains(string(resp), "\xc0\xa8\x01\x9e") || strings.Contains(string(resp), "\x0a\x00\x02\x0f") {
		t.Fatalf("wrong address set: %x", resp)
	}
}

func TestRespondUnmatchedSourceFallsBack(t *testing.T) {
	for _, src := range []netip.Addr{netip.MustParseAddr("203.0.113.9"), {}} {
		resp, _ := respond(buildQuery(1, "homestead.local", typeA, false), multiNIC(), src)
		if resp == nil {
			t.Fatal("no answer")
		}
		if n, _ := answerCount(t, resp); n != 2 {
			t.Fatalf("src %v answers = %d", src, n)
		}
	}
}

func TestForInterface(t *testing.T) {
	addrs := multiNIC().Addresses()
	got := forInterface(addrs, "lan0")
	if len(got) != 1 || got[0] != netip.MustParseAddr("192.168.1.158") {
		t.Fatalf("lan0 = %v", got)
	}
	if got := forInterface(addrs, "wlan9"); len(got) != 0 || announce(multiNIC(), got) != nil {
		t.Fatalf("empty interface announced: %v", got)
	}
}

package localdns

import (
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var testLinks = []Link{
	{Addr: netip.MustParseAddr("192.168.1.10"), Prefix: netip.MustParsePrefix("192.168.1.0/24")},
	{Addr: netip.MustParseAddr("10.0.0.10"), Prefix: netip.MustParsePrefix("10.0.0.0/24")},
}

var testNames = []string{"homestead.lan", "immich.lan"}

func query(t *testing.T, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func parse(t *testing.T, msg []byte) (dnsmessage.Header, []netip.Addr) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		t.Fatal(err)
	}
	var addrs []netip.Addr
	for {
		rh, err := p.AnswerHeader()
		if err == dnsmessage.ErrSectionDone {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if rh.Type != dnsmessage.TypeA {
			t.Fatalf("answer type %v", rh.Type)
		}
		a, err := p.AResource()
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, netip.AddrFrom4(a.A))
	}
	return h, addrs
}

func TestAnswer(t *testing.T) {
	local := netip.MustParseAddr("192.168.1.50")
	routed := netip.MustParseAddr("172.16.0.5")
	cases := []struct {
		name  string
		qname string
		typ   dnsmessage.Type
		src   netip.Addr
		rcode dnsmessage.RCode
		auth  bool
		addrs []string
	}{
		{"own name on the asker's subnet", "immich.lan.", dnsmessage.TypeA, local, dnsmessage.RCodeSuccess, true, []string{"192.168.1.10"}},
		{"case-insensitive", "Immich.LAN.", dnsmessage.TypeA, local, dnsmessage.RCodeSuccess, true, []string{"192.168.1.10"}},
		{"routed asker gets every address", "homestead.lan.", dnsmessage.TypeA, routed, dnsmessage.RCodeSuccess, true, []string{"192.168.1.10", "10.0.0.10"}},
		{"known name, no AAAA", "immich.lan.", dnsmessage.TypeAAAA, local, dnsmessage.RCodeSuccess, true, nil},
		{"unknown lan name", "printer.lan.", dnsmessage.TypeA, local, dnsmessage.RCodeNameError, true, nil},
		{"the zone itself", "lan.", dnsmessage.TypeA, local, dnsmessage.RCodeNameError, true, nil},
		{"no forwarding", "example.com.", dnsmessage.TypeA, local, dnsmessage.RCodeRefused, false, nil},
		{"lookalike suffix", "immich.notlan.", dnsmessage.TypeA, local, dnsmessage.RCodeRefused, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := Answer(query(t, tc.qname, tc.typ), testNames, testLinks, tc.src)
			if err != nil {
				t.Fatal(err)
			}
			h, addrs := parse(t, resp)
			if h.ID != 7 || !h.Response || !h.RecursionDesired || h.RecursionAvailable {
				t.Fatalf("header %+v", h)
			}
			if h.RCode != tc.rcode || h.Authoritative != tc.auth {
				t.Fatalf("rcode %v auth %v, want %v %v", h.RCode, h.Authoritative, tc.rcode, tc.auth)
			}
			if len(addrs) != len(tc.addrs) {
				t.Fatalf("addrs %v, want %v", addrs, tc.addrs)
			}
			for i, a := range addrs {
				if a.String() != tc.addrs[i] {
					t.Fatalf("addrs %v, want %v", addrs, tc.addrs)
				}
			}
		})
	}
}

func TestAnswerOddQueries(t *testing.T) {
	if _, err := Answer([]byte{1, 2, 3}, testNames, testLinks, netip.Addr{}); err == nil {
		t.Fatal("garbage was answered")
	}
	// A response is never answered (no reflection loops).
	resp, err := Answer(query(t, "immich.lan.", dnsmessage.TypeA), testNames, testLinks, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Answer(resp, testNames, testLinks, netip.Addr{}); err == nil {
		t.Fatal("a response was answered")
	}
	// Two questions: format error.
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 9})
	_ = b.StartQuestions()
	for _, n := range []string{"immich.lan.", "homestead.lan."} {
		_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(n), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	}
	msg, _ := b.Finish()
	resp, err = Answer(msg, testNames, testLinks, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := parse(t, resp); h.RCode != dnsmessage.RCodeFormatError {
		t.Fatalf("rcode %v", h.RCode)
	}
	// Not a standard query: not implemented.
	b = dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 9, OpCode: 2})
	_ = b.StartQuestions()
	msg, _ = b.Finish()
	resp, err = Answer(msg, testNames, testLinks, netip.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := parse(t, resp); h.RCode != dnsmessage.RCodeNotImplemented {
		t.Fatalf("rcode %v", h.RCode)
	}
}

func TestServeUDPAndTCP(t *testing.T) {
	names := []string{"immich.lan"}
	s, err := start(Config{
		Names: func() []string { return names },
		Links: []Link{{Addr: netip.MustParseAddr("127.0.0.1"), Prefix: netip.MustParsePrefix("127.0.0.0/8")}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Port 0 binds UDP and TCP to different ports; read them back.
	udpAddr := s.closer[0].(net.PacketConn).LocalAddr().String()
	tcpAddr := s.closer[1].(net.Listener).Addr().String()

	conn, err := net.Dial("udp4", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(query(t, "immich.lan.", dnsmessage.TypeA)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if h, addrs := parse(t, buf[:n]); h.RCode != dnsmessage.RCodeSuccess || len(addrs) != 1 || addrs[0].String() != "127.0.0.1" {
		t.Fatalf("udp answer %v %v", h.RCode, addrs)
	}

	tc, err := net.Dial("tcp4", tcpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
	q := query(t, "printer.lan.", dnsmessage.TypeA)
	if _, err := tc.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(q))), q...)); err != nil {
		t.Fatal(err)
	}
	var size [2]byte
	if _, err := io.ReadFull(tc, size[:]); err != nil {
		t.Fatal(err)
	}
	msg := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(tc, msg); err != nil {
		t.Fatal(err)
	}
	if h, _ := parse(t, msg); h.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("tcp rcode %v", h.RCode)
	}
}

func TestStartFailsWhenPortTaken(t *testing.T) {
	taken, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.LocalAddr().(*net.UDPAddr).Port
	_, err = start(Config{
		Names: func() []string { return nil },
		Links: []Link{{Addr: netip.MustParseAddr("127.0.0.1")}},
	}, port)
	if err == nil {
		t.Fatal("started on a taken port")
	}
	if _, err := start(Config{Names: func() []string { return nil }}, 0); err == nil {
		t.Fatal("started without addresses")
	}
}

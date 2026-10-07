package router

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// natpmpServer answers family requests: success echoing the suggested
// port, error codes on demand, reassignments when told.
type natpmpServer struct {
	t           *testing.T
	result      uint16
	assignPort  int
	lastRequest []byte
}

func (s *natpmpServer) serve(addr string) string {
	s.t.Helper()
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		s.t.Fatalf("listen: %v", err)
	}
	s.t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, peer, err := conn.ReadFrom(buffer)
			if err != nil || n < natpmpRequestLen {
				return
			}
			s.lastRequest = append([]byte(nil), buffer[:n]...)
			reply := make([]byte, natpmpReplyLen)
			reply[0] = natpmpVersion
			reply[1] = buffer[1] + 128
			binary.BigEndian.PutUint16(reply[2:4], s.result)
			copy(reply[4:8], []byte{0, 0, 0, 0})
			internal := binary.BigEndian.Uint16(buffer[4:6])
			binary.BigEndian.PutUint16(reply[8:10], internal)
			assigned := int(binary.BigEndian.Uint16(buffer[6:8]))
			if s.assignPort != 0 {
				assigned = s.assignPort
			}
			binary.BigEndian.PutUint16(reply[10:12], uint16(assigned))
			binary.BigEndian.PutUint32(reply[12:16], binary.BigEndian.Uint32(buffer[8:12]))
			_, _ = conn.WriteTo(reply, peer)
		}
	}()
	return conn.LocalAddr().String()
}

func natpmpProvider(t *testing.T, server *natpmpServer, addr string, enabled bool) *NATPMP {
	t.Helper()
	provider, err := NewNATPMP(server.serve(addr), enabled)
	if err != nil {
		t.Fatalf("NewNATPMP: %v", err)
	}
	return provider
}

func natpmpMapping() Mapping {
	return Mapping{
		Protocol: ProtocolUDP, ExternalPort: 3478,
		InternalClient: netip.MustParseAddr("192.168.1.10"), InternalPort: 3478,
		Lifetime: time.Hour,
	}
}

func TestNATPMPContract(t *testing.T) {
	var _ Provider = (*NATPMP)(nil)
	provider, err := NewNATPMP("192.168.1.1:5351", true)
	if err != nil {
		t.Fatalf("NewNATPMP: %v", err)
	}
	if provider.Name() != "nat-pmp" || !provider.Enabled() {
		t.Fatalf("provider = %+v", provider.Name())
	}
	caps := provider.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilityMapping {
		t.Fatalf("caps = %v", caps)
	}
	if err := ValidateProvider(provider); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	for _, raw := range []string{"no-port", "host:0", "host:99999"} {
		if _, err := NewNATPMP(raw, true); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := provider.QueryMapping(context.Background(), ProtocolUDP, 3478); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestNATPMPAddDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := &natpmpServer{t: t}
	provider := natpmpProvider(t, server, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, natpmpMapping()); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	// Request shape: version 0, UDP opcode, internal and suggested ports, lifetime.
	if server.lastRequest[0] != natpmpVersion || server.lastRequest[1] != natpmpOpUDPMap {
		t.Fatalf("header = %v", server.lastRequest[:2])
	}
	if binary.BigEndian.Uint16(server.lastRequest[4:6]) != 3478 ||
		binary.BigEndian.Uint16(server.lastRequest[6:8]) != 3478 {
		t.Fatal("ports wrong")
	}
	if binary.BigEndian.Uint32(server.lastRequest[8:12]) != 3600 {
		t.Fatal("lifetime wrong")
	}
	if err := provider.DeleteMapping(ctx, natpmpMapping()); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	if binary.BigEndian.Uint32(server.lastRequest[8:12]) != 0 {
		t.Fatal("delete carries lifetime")
	}
	// TCP maps the sibling opcode.
	tcp := natpmpMapping()
	tcp.Protocol = ProtocolTCP
	tcp.ExternalPort = 80
	tcp.InternalPort = 80
	if err := provider.AddMapping(ctx, tcp); err != nil {
		t.Fatalf("TCP AddMapping: %v", err)
	}
	if server.lastRequest[1] != natpmpOpTCPMap {
		t.Fatalf("opcode = %d", server.lastRequest[1])
	}
	// Disabled automation refuses before the network.
	off, err := NewNATPMP("192.168.1.1:5351", false)
	if err != nil {
		t.Fatalf("NewNATPMP: %v", err)
	}
	if err := off.AddMapping(ctx, natpmpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := off.DeleteMapping(ctx, natpmpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := provider.AddMapping(ctx, Mapping{}); err == nil {
		t.Fatal("added invalid mapping")
	}
}

func TestNATPMPPErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	denied := &natpmpServer{t: t, result: 2}
	provider := natpmpProvider(t, denied, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, natpmpMapping()); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
	reassigned := &natpmpServer{t: t, assignPort: 49152}
	provider = natpmpProvider(t, reassigned, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, natpmpMapping()); err == nil ||
		!strings.Contains(err.Error(), "assigned external port 49152") {
		t.Fatalf("err = %v", err)
	}
	quiet, err := NewNATPMP("127.0.0.1:1", true)
	if err != nil {
		t.Fatalf("NewNATPMP: %v", err)
	}
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	if err := quiet.AddMapping(short, natpmpMapping()); err == nil {
		t.Fatal("mapped through closed port")
	}
}

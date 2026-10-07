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

// pcpServer answers MAP requests: success echoing the suggested port,
// error codes on demand, wrong nonces or ports when told.
type pcpServer struct {
	t           *testing.T
	result      byte
	assignPort  int
	wrongNonce  bool
	lastRequest []byte
}

func (s *pcpServer) serve(addr string) string {
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
			if err != nil || n < pcpMapSize {
				return
			}
			s.lastRequest = append([]byte(nil), buffer[:n]...)
			response := make([]byte, 60)
			response[0] = pcpVersion
			response[1] = 0x80 | pcpOpMap
			response[2] = 0
			response[3] = s.result
			copy(response[4:8], buffer[4:8])
			copy(response[8:20], buffer[8:20])
			nonce := buffer[20:32]
			if s.wrongNonce {
				nonce = make([]byte, 12)
			}
			copy(response[20:32], nonce)
			response[32] = buffer[32]
			assigned := int(binary.BigEndian.Uint16(buffer[38:40]))
			if s.assignPort != 0 {
				assigned = s.assignPort
			}
			binary.BigEndian.PutUint16(response[38:40], uint16(assigned))
			_, _ = conn.WriteTo(response, peer)
		}
	}()
	return conn.LocalAddr().String()
}

func pcpProvider(t *testing.T, server *pcpServer, addr string, enabled bool) *PCP {
	t.Helper()
	provider, err := NewPCP(server.serve(addr), enabled)
	if err != nil {
		t.Fatalf("NewPCP: %v", err)
	}
	return provider
}

func pcpMapping() Mapping {
	return Mapping{
		Protocol: ProtocolTCP, ExternalPort: 80,
		InternalClient: netip.MustParseAddr("192.168.1.10"), InternalPort: 80,
		Lifetime: time.Hour,
	}
}

func TestPCPContract(t *testing.T) {
	var _ Provider = (*PCP)(nil)
	provider, err := NewPCP("192.168.1.1:5351", true)
	if err != nil {
		t.Fatalf("NewPCP: %v", err)
	}
	if provider.Name() != "pcp" || !provider.Enabled() {
		t.Fatalf("provider = %+v", provider.Name())
	}
	caps := provider.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilityMapping {
		t.Fatalf("caps = %v", caps)
	}
	if err := ValidateProvider(provider); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	if _, err := NewPCP("no-port", true); err == nil {
		t.Fatal("accepted bad server")
	}
	if _, err := NewPCP("host:0", true); err == nil {
		t.Fatal("accepted bad port")
	}
	if _, err := provider.QueryMapping(context.Background(), ProtocolTCP, 80); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestPCPAddDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := &pcpServer{t: t}
	provider := pcpProvider(t, server, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, pcpMapping()); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	// Request shape: version MAP, TCP, internal and suggested ports.
	if server.lastRequest[0] != pcpVersion || server.lastRequest[1] != pcpOpMap {
		t.Fatalf("header = %v", server.lastRequest[:2])
	}
	if server.lastRequest[32] != pcpProtoTCP {
		t.Fatalf("proto = %d", server.lastRequest[32])
	}
	if binary.BigEndian.Uint16(server.lastRequest[36:38]) != 80 ||
		binary.BigEndian.Uint16(server.lastRequest[38:40]) != 80 {
		t.Fatal("ports wrong")
	}
	if err := provider.DeleteMapping(ctx, pcpMapping()); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	if binary.BigEndian.Uint32(server.lastRequest[4:8]) != 0 {
		t.Fatal("delete carries lifetime")
	}
	// Disabled automation refuses before the network.
	off, err := NewPCP("192.168.1.1:5351", false)
	if err != nil {
		t.Fatalf("NewPCP: %v", err)
	}
	if err := off.AddMapping(ctx, pcpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	if err := off.DeleteMapping(ctx, pcpMapping()); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v", err)
	}
	// Invalid mappings refuse before the network.
	if err := provider.AddMapping(ctx, Mapping{}); err == nil {
		t.Fatal("added invalid mapping")
	}
}

func TestPCPErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	denied := &pcpServer{t: t, result: 2}
	provider := pcpProvider(t, denied, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, pcpMapping()); err == nil ||
		!strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v", err)
	}
	reassigned := &pcpServer{t: t, assignPort: 49152}
	provider = pcpProvider(t, reassigned, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, pcpMapping()); err == nil ||
		!strings.Contains(err.Error(), "assigned external port 49152") {
		t.Fatalf("err = %v", err)
	}
	liar := &pcpServer{t: t, wrongNonce: true}
	provider = pcpProvider(t, liar, "127.0.0.1:0", true)
	if err := provider.AddMapping(ctx, pcpMapping()); err == nil ||
		!strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("err = %v", err)
	}
	// Silence exhausts retries as an exchange error.
	quiet, err := NewPCP("127.0.0.1:1", true)
	if err != nil {
		t.Fatalf("NewPCP: %v", err)
	}
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	if err := quiet.AddMapping(short, pcpMapping()); err == nil {
		t.Fatal("mapped through closed port")
	}
}

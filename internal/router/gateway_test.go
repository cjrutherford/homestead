package router

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// pmpServer answers NAT-PMP public-address requests with version and IP.
func pmpServer(t *testing.T, version byte, external netip.Addr) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 64)
		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil || n < 2 {
				return
			}
			response := []byte{version, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
			copy(response[8:12], external.AsSlice())
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	return conn.LocalAddr().String()
}

func TestProbePMP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	external := netip.MustParseAddr("203.0.113.7")
	version, got, err := ProbePMP(ctx, pmpServer(t, 0, external))
	if err != nil {
		t.Fatalf("ProbePMP: %v", err)
	}
	if version != 0 || got != external {
		t.Fatalf("version=%d ip=%v", version, got)
	}
	version, _, err = ProbePMP(ctx, pmpServer(t, 2, external))
	if err != nil {
		t.Fatalf("ProbePMP: %v", err)
	}
	if version != 2 {
		t.Fatalf("version=%d", version)
	}
	// Silence is an error the caller treats as unsupported.
	if _, _, err := ProbePMP(ctx, "127.0.0.1:1"); err == nil {
		t.Fatal("probed closed port")
	}
}

// ssdpServer answers M-SEARCH with a description location, then serves
// a gateway description document.
func ssdpServer(t *testing.T) (string, *string) {
	t.Helper()
	var location string
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<root xmlns="urn:schemas-upnp-org:device-1-0"><device>
			<friendlyName>Test Gateway</friendlyName><modelName>TG-1000</modelName>
			<serviceList><service>
			<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
			<controlURL>/ctl</controlURL>
			</service></serviceList>
			<deviceList><device><friendlyName>Nested</friendlyName>
			<serviceList><service>
			<serviceType>urn:schemas-upnp-org:service:WANPPPConnection:1</serviceType>
			<controlURL>http://absolute.example/ctl2</controlURL>
			</service></serviceList>
			</device></deviceList>
		</device></root>`))
	})
	httpServer := &http.Server{Handler: mux}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = httpServer.Close() })
	go func() { _ = httpServer.Serve(listener) }()
	location = "http://" + listener.Addr().String() + "/desc.xml"
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			_, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			response := "HTTP/1.1 200 OK\r\n" +
				"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n" +
				"LOCATION: " + location + "\r\n\r\n"
			_, _ = conn.WriteTo([]byte(response), addr)
		}
	}()
	old := ssdpTarget
	ssdpTarget = conn.LocalAddr().String()
	t.Cleanup(func() { ssdpTarget = old })
	return location, &location
}

func TestDiscoverUPnP(t *testing.T) {
	location, _ := ssdpServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := DiscoverUPnP(ctx)
	if err != nil {
		t.Fatalf("DiscoverUPnP: %v", err)
	}
	if info == nil {
		t.Fatal("missed gateway")
	}
	if info.Location != location || info.FriendlyName != "Test Gateway" || info.Model != "TG-1000" {
		t.Fatalf("info = %+v", info)
	}
	// First WAN connection service wins; relative URL resolves.
	if info.ControlURL != strings.Replace(location, "/desc.xml", "/ctl", 1) {
		t.Fatalf("control = %q", info.ControlURL)
	}
	// Nothing answering means no UPnP, reported without error.
	old := ssdpTarget
	ssdpTarget = "127.0.0.1:1"
	t.Cleanup(func() { ssdpTarget = old })
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	info, err = DiscoverUPnP(short)
	if err != nil || info != nil {
		t.Fatalf("info = %+v %v", info, err)
	}
}

// pmpServerOn answers on a fixed address for full-stack discovery.
func pmpServerOn(t *testing.T, addr string, version byte, external netip.Addr) {
	t.Helper()
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 64)
		for {
			n, peer, err := conn.ReadFrom(buffer)
			if err != nil || n < 2 {
				return
			}
			response := []byte{version, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
			copy(response[8:12], external.AsSlice())
			_, _ = conn.WriteTo(response, peer)
		}
	}()
}

func TestDiscoverGateway(t *testing.T) {
	location, _ := ssdpServer(t)
	external := netip.MustParseAddr("203.0.113.7")
	local := loopbackAlternative(t)
	pmpServerOn(t, "0.0.0.0:5351", 0, external)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := DiscoverGateway(ctx, "test", local.String())
	if err != nil {
		t.Fatalf("DiscoverGateway: %v", err)
	}
	if info.Gateway.Address != local || info.Gateway.Interface != "test" {
		t.Fatalf("gateway = %+v", info.Gateway)
	}
	if len(info.Protocols) != 2 || info.Protocols[0] != ProtocolNATPMP || info.Protocols[1] != ProtocolUPnP {
		t.Fatalf("protocols = %v", info.Protocols)
	}
	if info.ExternalIP != external {
		t.Fatalf("external = %v", info.ExternalIP)
	}
	if info.UPnP == nil || info.UPnP.Location != location {
		t.Fatalf("upnp = %+v", info.UPnP)
	}
	for _, args := range [][2]string{{"", "192.168.1.1"}, {"eth0", ""}, {"eth0", "127.0.0.1"}, {"", ""}} {
		if _, err := DiscoverGateway(ctx, args[0], args[1]); err == nil {
			t.Fatalf("discovered %v", args)
		}
	}
}

// loopbackAlternative returns a non-loopback local v4 for gateway
// tests, which refuse loopback gateways by design. Skips where the
// sandbox has no such address.
func loopbackAlternative(t *testing.T) netip.Addr {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("interfaces: %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if prefix, ok := addr.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(prefix.IP.To4()); ok && prefix.IP.To4() != nil {
					if parsed, err := netip.ParseAddr(ip.String()); err == nil {
						return parsed
					}
				}
			}
		}
	}
	t.Skip("no non-loopback address available")
	return netip.Addr{}
}

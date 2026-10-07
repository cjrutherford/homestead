package router

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseTCPTable(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue
   0: 0100007F:0050 00000000:0000 0A 00000000:00000000
   1: 00000000:01BB 00000000:0000 0A 00000000:00000000
   2: 0100007F:0050 0A00002A:01BB 01 00000000:00000000
   3: garbage line
   4: 00000000:ZZZZ 00000000:0000 0A 00000000:00000000
`
	ports := parseTCPTable(strings.NewReader(table))
	if len(ports) != 2 || !ports[80] || !ports[443] {
		t.Fatalf("ports = %v", ports)
	}
	v6 := `  sl  local_address                         rem_address                          st
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A
`
	ports = parseTCPTable(strings.NewReader(v6))
	if len(ports) != 1 || !ports[443] {
		t.Fatalf("v6 ports = %v", ports)
	}
}

func fixtureProc(t *testing.T, contents map[string]string) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for name, content := range contents {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		paths = append(paths, path)
	}
	old := procTCPPaths
	procTCPPaths = paths
	t.Cleanup(func() { procTCPPaths = old })
}

func listenOn(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().String(), listener.Addr().(*net.TCPAddr).Port
}

func soapMapping(t *testing.T, entries map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 2048)
		n, _ := r.Body.Read(raw)
		body := string(raw[:n])
		port := ""
		if start := strings.Index(body, "<NewExternalPort>"); start >= 0 {
			rest := body[start+len("<NewExternalPort>"):]
			port = rest[:strings.Index(rest, "</NewExternalPort>")]
		}
		target, ok := entries[port]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<error><errorCode>714</errorCode></error>`))
			return
		}
		parts := strings.Split(target, ":")
		_, _ = w.Write([]byte(`<Envelope><Body><GetSpecificPortMappingEntryResponse>` +
			`<NewInternalClient>` + parts[0] + `</NewInternalClient>` +
			`<NewInternalPort>` + parts[1] + `</NewInternalPort>` +
			`</GetSpecificPortMappingEntryResponse></Body></Envelope>`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func listenFixture(port int) map[string]string {
	return map[string]string{
		"tcp": "  sl  local_address rem_address   st\n" +
			"   0: 00000000:0000 00000000:0000 0A\n" +
			"   1: 0100007F:" + hexPort(port) + " 00000000:0000 0A\n",
	}
}

func hexPort(port int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[port>>12&15], digits[port>>8&15], digits[port>>4&15], digits[port&15]})
}

func TestVerifyAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lanIP := netip.MustParseAddr("127.0.0.1")

	t.Run("service down", func(t *testing.T) {
		fixtureProc(t, map[string]string{"tcp": "  sl  local_address rem_address   st\n"})
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web-http", Protocol: ProtocolTCP, Port: 18080},
			LANAddr: lanIP,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if verdict.Passed || verdict.Responsible != LayerService {
			t.Fatalf("verdict = %+v", verdict)
		}
	})

	t.Run("host firewall signature", func(t *testing.T) {
		// Kernel claims a listener, dials still fail: the firewall
		// signature, reproduced deterministically.
		fixtureProc(t, listenFixture(18081))
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web-http", Protocol: ProtocolTCP, Port: 18081},
			LANAddr: lanIP,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if verdict.Passed || verdict.Responsible != LayerHostFirewall {
			t.Fatalf("verdict = %+v", verdict)
		}
		if !verdict.Layers[0].OK || !strings.Contains(verdict.Layers[1].Detail, "firewall") {
			t.Fatalf("layers = %+v", verdict.Layers)
		}
	})

	t.Run("router missing", func(t *testing.T) {
		addr, port := listenOn(t)
		_ = addr
		fixtureProc(t, listenFixture(port))
		upnp := UPnPInfo{ControlURL: soapMapping(t, map[string]string{})}
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web-http", Protocol: ProtocolTCP, Port: port},
			LANAddr: lanIP, UPnP: &upnp,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if verdict.Passed || verdict.Responsible != LayerRouter {
			t.Fatalf("verdict = %+v", verdict)
		}
	})

	t.Run("router matched", func(t *testing.T) {
		_, port := listenOn(t)
		fixtureProc(t, listenFixture(port))
		target := "127.0.0.1:" + itoaPort(port)
		upnp := UPnPInfo{ControlURL: soapMapping(t, map[string]string{itoaPort(port): target})}
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web", Protocol: ProtocolTCP, Port: port},
			LANAddr: lanIP, UPnP: &upnp,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if !verdict.Passed || verdict.Responsible != "" {
			t.Fatalf("verdict = %+v", verdict)
		}
	})

	t.Run("router mismatched", func(t *testing.T) {
		_, port := listenOn(t)
		fixtureProc(t, listenFixture(port))
		upnp := UPnPInfo{ControlURL: soapMapping(t, map[string]string{itoaPort(port): "192.168.1.99:80"})}
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web", Protocol: ProtocolTCP, Port: port},
			LANAddr: lanIP, UPnP: &upnp,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if verdict.Passed || verdict.Responsible != LayerRouter ||
			!strings.Contains(verdict.Layers[2].Detail, "192.168.1.99") {
			t.Fatalf("verdict = %+v", verdict)
		}
	})

	t.Run("unknown mapping", func(t *testing.T) {
		_, port := listenOn(t)
		fixtureProc(t, listenFixture(port))
		verdict, err := VerifyForward(ctx, ForwardCheck{
			Forward: Forward{Name: "web", Protocol: ProtocolTCP, Port: port},
			LANAddr: lanIP,
		})
		if err != nil {
			t.Fatalf("VerifyForward: %v", err)
		}
		if !verdict.Passed {
			t.Fatalf("verdict = %+v", verdict)
		}
		if !strings.Contains(verdict.Layers[2].Detail, "outside") {
			t.Fatalf("layers = %+v", verdict.Layers)
		}
	})

	t.Run("invalid check", func(t *testing.T) {
		if _, err := VerifyForward(ctx, ForwardCheck{}); err == nil {
			t.Fatal("verified invalid check")
		}
	})
}

func itoaPort(port int) string {
	return strconv.Itoa(port)
}

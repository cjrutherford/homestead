package router

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Verified layers, innermost first: each exonerates the last, so the
// first failure names the responsible layer instead of blending
// service, host, and router faults into one "unreachable".
const (
	LayerService      = "service"
	LayerHostFirewall = "host-firewall"
	LayerRouter       = "router"
)

// procTCPPaths locates kernel socket tables, overridden in tests with
// fixture files. Reading state beats probing: a listening socket with
// a refused dial is firewall evidence, not service evidence.
var procTCPPaths = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// listeningPorts reports locally bound LISTEN ports from the kernel
// tables (hex state 0A). Unreadable tables fail closed to empty rather
// than erroring: verification degrades to dial evidence, never stops.
func listeningPorts() map[uint16]bool {
	listening := map[uint16]bool{}
	for _, path := range procTCPPaths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		for port := range parseTCPTable(file) {
			listening[port] = true
		}
		_ = file.Close()
	}
	return listening
}

// parseTCPTable yields LISTEN ports from one table reader: the local
// address field carries the port after its colon, state sits fourth.
func parseTCPTable(reader io.Reader) map[uint16]bool {
	ports := map[uint16]bool{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		address := fields[1]
		index := strings.LastIndex(address, ":")
		if index < 0 {
			continue
		}
		raw, err := strconv.ParseUint(address[index+1:], 16, 16)
		if err != nil {
			continue
		}
		ports[uint16(raw)] = true
	}
	return ports
}

// ForwardCheck scopes one verification: the forward under test, the
// appliance address to dial for the serving proof, and optional UPnP
// facts for the mapping proof. Without UPnP the router layer reports
// unknown instead of guessing — manual and protocol-driven gateways
// prove externally (S126-style checks from outside).
type ForwardCheck struct {
	Forward Forward
	LANAddr netip.Addr
	UPnP    *UPnPInfo
}

// LayerVerdict is one layer's evidence and outcome.
type LayerVerdict struct {
	Layer  string
	OK     bool
	Detail string
}

// Verdict attributes one forward: ordered layer evidence plus the
// responsible layer — the first failure, or empty when all hold.
// External reachability itself stays outside: hairpin NAT answers
// inside probes with outside lies, so on-appliance proof stops at the
// router mapping and names outside validation as the next step.
type Verdict struct {
	Forward     Forward
	Layers      []LayerVerdict
	Responsible string
	Passed      bool
}

// VerifyForward attributes ingress for one forward across service,
// host firewall, and router layers. Invalid checks fail before
// probing; layers never short-circuit, so one run carries full
// evidence even when the answer is already no.
func VerifyForward(ctx context.Context, check ForwardCheck) (Verdict, error) {
	if err := check.Forward.Validate(); err != nil {
		return Verdict{}, err
	}
	if !check.LANAddr.IsValid() || check.LANAddr.IsUnspecified() || check.LANAddr.IsMulticast() {
		return Verdict{}, fmt.Errorf("router: dial address must be usable")
	}
	verdict := Verdict{Forward: check.Forward}
	listening := listeningPorts()[uint16(check.Forward.Port)]
	if listening {
		verdict.Layers = append(verdict.Layers, LayerVerdict{
			Layer: LayerService, OK: true,
			Detail: fmt.Sprintf("port %d listens locally", check.Forward.Port),
		})
	} else {
		verdict.Layers = append(verdict.Layers, LayerVerdict{
			Layer: LayerService, OK: false,
			Detail: fmt.Sprintf("nothing listens on port %d", check.Forward.Port),
		})
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp",
		net.JoinHostPort(check.LANAddr.String(), strconv.Itoa(check.Forward.Port)))
	if err != nil {
		detail := fmt.Sprintf("LAN dial failed: %v", err)
		if listening {
			detail = fmt.Sprintf("LAN dial refused with a listener present: host firewall likely (%v)", err)
		}
		verdict.Layers = append(verdict.Layers, LayerVerdict{
			Layer: LayerHostFirewall, OK: false, Detail: detail,
		})
	} else {
		_ = conn.Close()
		verdict.Layers = append(verdict.Layers, LayerVerdict{
			Layer: LayerHostFirewall, OK: true,
			Detail: fmt.Sprintf("LAN dial to %s succeeds", check.LANAddr),
		})
	}
	verdict.Layers = append(verdict.Layers, verifyMapping(ctx, check))
	verdict.Passed = true
	for _, layer := range verdict.Layers {
		if !layer.OK {
			verdict.Passed = false
			verdict.Responsible = layer.Layer
			break
		}
	}
	return verdict, nil
}

// verifyMapping proves the NAT rule through UPnP, or reports unknown
// for gateways nothing can ask. A present-but-mismatched mapping names
// the router with the actual target as evidence.
func verifyMapping(ctx context.Context, check ForwardCheck) LayerVerdict {
	if check.UPnP == nil || check.UPnP.ControlURL == "" {
		return LayerVerdict{Layer: LayerRouter, OK: true,
			Detail: "no UPnP facts: mapping unconfirmed, prove reachability from outside"}
	}
	checks, err := QueryMappings(ctx, *check.UPnP, check.LANAddr, []Forward{check.Forward})
	if err != nil {
		return LayerVerdict{Layer: LayerRouter, OK: false, Detail: fmt.Sprintf("mapping query failed: %v", err)}
	}
	if !checks[0].Found {
		return LayerVerdict{Layer: LayerRouter, OK: false,
			Detail: fmt.Sprintf("no gateway mapping for %d/%s", check.Forward.Port, check.Forward.Protocol)}
	}
	if !checks[0].Matches {
		return LayerVerdict{Layer: LayerRouter, OK: false,
			Detail: fmt.Sprintf("gateway maps elsewhere: %s", checks[0].Internal)}
	}
	return LayerVerdict{Layer: LayerRouter, OK: true,
		Detail: fmt.Sprintf("gateway maps %d/%s to %s", check.Forward.Port, check.Forward.Protocol, checks[0].Internal)}
}

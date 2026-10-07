package exposure

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"homestead/internal/dns"
)

// Validation check names, one per layer from name to app.
const (
	CheckDNS    = "dns"
	CheckTCP80  = "tcp-80"
	CheckTCP443 = "tcp-443"
	CheckTLS    = "tls"
	CheckHTTP   = "http"
)

// dialTimeout bounds each connection attempt; the context bounds the run.
const dialTimeout = 10 * time.Second

// Target scopes one validation run: the public hostname asserted at
// every layer, the address it must resolve to, the path and response
// contract proving the app behind the proxy, and an optional dial
// override (split-horizon checks and tests reach the edge by IP while
// still asserting the name for DNS and certificates).
type Target struct {
	Host       string
	Address    netip.Addr
	Path       string
	WantStatus int
	WantBody   string
	Addr       string
	Plaintext  bool
}

// Validate checks target shape: hostname spelling, usable expected
// address, sane path and status. Plaintext documents itself as
// LAN-and-test only — public flows never set it.
func (t Target) Validate() error {
	if len(t.Host) == 0 || len(t.Host) > 253 {
		return fmt.Errorf("exposure: host must be 1-253 characters")
	}
	if !t.Address.IsValid() || t.Address.IsUnspecified() {
		return fmt.Errorf("exposure: address must be usable")
	}
	if t.Path != "" && !strings.HasPrefix(t.Path, "/") {
		return fmt.Errorf("exposure: path must start with /")
	}
	if t.WantStatus != 0 && (t.WantStatus < 100 || t.WantStatus > 599) {
		return fmt.Errorf("exposure: status must be 100-599")
	}
	return nil
}

// Finding is one layer's verdict with the evidence attached.
type Finding struct {
	Check  string
	OK     bool
	Detail string
}

// Report is the five-layer verdict: DNS, both ports, TLS trust, and
// the served response. Passed means every layer holds.
type Report struct {
	Host   string
	Checks []Finding
	Passed bool
}

// Validate runs every layer and rolls up: DNS must resolve to the
// expected address, both edge ports must accept TCP, TLS must verify
// against system roots for the hostname, and the served response must
// meet the path contract. Layers run in dependency order but never
// short-circuit — a down port and a bad certificate both report, so
// one run tells the owner everything.
func Validate(ctx context.Context, probe dns.ProbeFunc, target Target) (Report, error) {
	if err := target.Validate(); err != nil {
		return Report{}, err
	}
	if probe == nil {
		return Report{}, fmt.Errorf("exposure: probe must not be nil")
	}
	report := Report{Host: target.Host}
	report.Checks = append(report.Checks, checkDNS(ctx, probe, target))
	report.Checks = append(report.Checks, checkTCP(ctx, target, CheckTCP80, "80"))
	report.Checks = append(report.Checks, checkTCP(ctx, target, CheckTCP443, "443"))
	report.Checks = append(report.Checks, checkTLS(ctx, target))
	report.Checks = append(report.Checks, checkHTTP(ctx, target))
	report.Passed = true
	for _, finding := range report.Checks {
		if !finding.OK {
			report.Passed = false
			break
		}
	}
	return report, nil
}

// dialHost resolves the dial target: override first, hostname otherwise.
func dialHost(target Target, port string) string {
	if target.Addr != "" {
		return target.Addr
	}
	return net.JoinHostPort(target.Host, port)
}

// checkDNS proves the public name resolves to the expected address,
// probing A or AAAA by family.
func checkDNS(ctx context.Context, probe dns.ProbeFunc, target Target) Finding {
	typ := dns.RecordA
	if target.Address.Is6() {
		typ = dns.RecordAAAA
	}
	answers, err := probe(ctx, target.Host, typ)
	if err != nil {
		return Finding{Check: CheckDNS, Detail: fmt.Sprintf("DNS lookup failed: %v", err)}
	}
	for _, answer := range answers {
		if addr, err := netip.ParseAddr(answer); err == nil && addr == target.Address {
			return Finding{Check: CheckDNS, OK: true,
				Detail: fmt.Sprintf("%s resolves to %s", target.Host, target.Address)}
		}
	}
	return Finding{Check: CheckDNS,
		Detail: fmt.Sprintf("%s resolves to %q, want %s", target.Host, strings.Join(answers, " "), target.Address)}
}

// checkTCP proves the edge port accepts connections.
func checkTCP(ctx context.Context, target Target, check, port string) Finding {
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", dialHost(target, port))
	if err != nil {
		return Finding{Check: check, Detail: fmt.Sprintf("port %s unreachable: %v", port, err)}
	}
	defer conn.Close()
	return Finding{Check: check, OK: true, Detail: fmt.Sprintf("port %s accepts TCP", port)}
}

// checkTLS proves publicly trusted serving: a verified handshake for
// the hostname against system roots, reporting the expiry reader.
// Plaintext targets skip TLS by definition.
func checkTLS(ctx context.Context, target Target) Finding {
	if target.Plaintext {
		return Finding{Check: CheckTLS, OK: true, Detail: "plaintext target skips TLS"}
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", dialHost(target, "443"), &tls.Config{
		ServerName: target.Host,
	})
	if err != nil {
		return Finding{Check: CheckTLS, Detail: "TLS " + classifyTLSError(target.Host, err)}
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Finding{Check: CheckTLS, Detail: "TLS served no certificates"}
	}
	leaf := state.PeerCertificates[0]
	return Finding{Check: CheckTLS, OK: true,
		Detail: fmt.Sprintf("serves trusted certificate for %s until %s", target.Host, leaf.NotAfter.Format("2006-01-02"))}
}

// classifyTLSError translates handshake failures into owner language:
// trust, expiry, name, or transport.
func classifyTLSError(host string, err error) string {
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return fmt.Sprintf("%s is not publicly trusted (staging or internal CA?)", host)
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		return fmt.Sprintf("certificate invalid: %v", invalid)
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return fmt.Sprintf("certificate does not cover %s", host)
	}
	return err.Error()
}

// checkHTTP proves proxy path plus app health in one fetch: the edge
// must answer the path with the contracted status, and the body must
// carry the contracted substring when one is set. Plaintext targets
// fetch http for LAN and test use.
func checkHTTP(ctx context.Context, target Target) Finding {
	scheme := "https"
	if target.Plaintext {
		scheme = "http"
	}
	path := target.Path
	if path == "" {
		path = "/"
	}
	want := target.WantStatus
	if want == 0 {
		want = 200
	}
	url := scheme + "://" + target.Host + path
	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
	}
	if target.Addr != "" {
		if _, _, err := net.SplitHostPort(target.Addr); err != nil {
			return Finding{Check: CheckHTTP, Detail: fmt.Sprintf("bad dial address: %v", err)}
		}
		addr := target.Addr
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, addr)
		}
		transport.TLSClientConfig = &tls.Config{ServerName: target.Host}
	}
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Finding{Check: CheckHTTP, Detail: fmt.Sprintf("request: %v", err)}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Finding{Check: CheckHTTP, Detail: fmt.Sprintf("fetch failed: %v", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Finding{Check: CheckHTTP, Detail: fmt.Sprintf("read: %v", err)}
	}
	if resp.StatusCode != want {
		return Finding{Check: CheckHTTP,
			Detail: fmt.Sprintf("GET %s returned %d, want %d", path, resp.StatusCode, want)}
	}
	if target.WantBody != "" && !strings.Contains(string(raw), target.WantBody) {
		return Finding{Check: CheckHTTP,
			Detail: fmt.Sprintf("GET %s lacks contracted body text", path)}
	}
	return Finding{Check: CheckHTTP, OK: true,
		Detail: fmt.Sprintf("GET %s returned %d as contracted", path, want)}
}

package everything

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Net egress fence (ADR-058 section 5, address fence). This is the library
// only: the address classifier, a dialer pinned to the checked address, and a
// redirect checker. The net.fetch operation, limits and log are S435.

const (
	fenceMaxRedirects   = 5
	fenceConnectTimeout = 10 * time.Second
	fenceTotalTimeout   = 30 * time.Second
)

// Fence error classes (stable strings for the S435 log's error class).
const (
	FenceScheme   = "scheme"
	FenceUserinfo = "userinfo"
	FenceHost     = "host"
	FencePort     = "port"
	FenceBlocked  = "blocked_address"
	FenceResolve  = "resolve"
	FenceRedirect = "redirect"
	FenceConnect  = "connect"
)

// FenceError is a refusal by the fence. Detail is for logs, never for apps.
type FenceError struct {
	Class  string
	Detail string
}

func (e *FenceError) Error() string { return "net fence: " + e.Class + ": " + e.Detail }

func fenceErr(class, format string, a ...any) error {
	return &FenceError{Class: class, Detail: fmt.Sprintf(format, a...)}
}

var fenceMailPorts = map[int]bool{25: true, 110: true, 143: true, 465: true, 587: true, 993: true, 995: true}

var fenceBlockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		// IPv4
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		// IPv6
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2001:db8::/32", "2002::/16", "100::/64",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// FenceAddrBlocked reports whether an address is refused. IPv4-mapped and
// IPv4-compatible IPv6 forms are unwrapped and judged as IPv4; NAT64, 6to4 and
// Teredo are refused whole, so they cannot embed a refused IPv4.
func FenceAddrBlocked(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	a = a.WithZone("")
	if a.Is4In6() {
		a = a.Unmap()
	} else if a.Is6() {
		b := a.As16()
		zero := true
		for _, c := range b[:12] {
			if c != 0 {
				zero = false
			}
		}
		// ::a.b.c.d (IPv4-compatible). :: and ::1 fall to the table.
		if zero && !(b[12] == 0 && b[13] == 0 && b[14] == 0 && b[15] <= 1) {
			a = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		}
	}
	for _, p := range fenceBlockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// fenceParseHostIP parses an IP literal, including the legacy IPv4 spellings
// (decimal, hex, octal, short dotted) that some stacks accept, so a refused
// address cannot hide behind its spelling. ok is false for a real host name.
func fenceParseHostIP(host string) (net.IP, bool) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip, true
	}
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(parts) == 0 || len(parts) > 4 {
		return nil, false
	}
	vals := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, false
		}
		base := 10
		s := p
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			base, s = 16, s[2:]
			if s == "" {
				return nil, false
			}
		} else if len(s) > 1 && s[0] == '0' {
			base, s = 8, s[1:]
		}
		v, err := strconv.ParseUint(s, base, 64)
		if err != nil {
			return nil, false
		}
		vals[i] = v
	}
	var n uint64
	last := len(vals) - 1
	for i := 0; i < last; i++ {
		if vals[i] > 255 {
			return nil, false
		}
		n |= vals[i] << (8 * (3 - uint(i)))
	}
	if vals[last] >= 1<<(8*(4-uint(last))) {
		return nil, false
	}
	n |= vals[last]
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)), true
}

// FenceCheckURL runs step 1 of the fence (and the port rule) on a URL.
func FenceCheckURL(u *url.URL) error {
	if u == nil {
		return fenceErr(FenceHost, "no url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fenceErr(FenceScheme, "scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fenceErr(FenceUserinfo, "userinfo present")
	}
	if u.Hostname() == "" {
		return fenceErr(FenceHost, "empty host")
	}
	port := u.Port()
	if port == "" {
		return nil
	}
	return fenceCheckPort(port)
}

func fenceCheckPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fenceErr(FencePort, "bad port %q", port)
	}
	if fenceMailPorts[n] {
		return fenceErr(FencePort, "port %d refused", n)
	}
	return nil
}

// FenceResolver is the lookup the fence uses; tests inject a fake.
type FenceResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// FenceDialFunc dials one already-checked ip:port.
type FenceDialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// NetFence holds the fence's resolver and dialer. The zero value uses the
// system resolver and a dialer that re-checks the socket address at connect.
type NetFence struct {
	Resolver FenceResolver // nil: net.DefaultResolver
	Dial     FenceDialFunc // nil: net.Dialer with fenceControl
	// TLSConfig is cloned; nil means system roots. MinVersion is forced to 1.2.
	TLSConfig *tls.Config
}

// fenceControl is the net.Dialer Control callback: it judges the actual remote
// address of the socket, after any resolution, just before connect.
func fenceControl(network, address string, _ syscall.RawConn) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fenceErr(FenceBlocked, "unparseable socket address")
	}
	ip := net.ParseIP(host)
	if ip == nil || FenceAddrBlocked(ip) {
		return fenceErr(FenceBlocked, "socket address %s refused", host)
	}
	return fenceCheckPort(port)
}

func (f *NetFence) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if f.Dial != nil {
		return f.Dial(ctx, network, addr)
	}
	d := &net.Dialer{Timeout: fenceConnectTimeout, Control: fenceControl}
	return d.DialContext(ctx, network, addr)
}

// Resolve resolves and judges a host. Every answer must pass (fail closed).
// IP literals, in any spelling, take the same check without a lookup.
func (f *NetFence) Resolve(ctx context.Context, host string) ([]net.IP, error) {
	if host == "" {
		return nil, fenceErr(FenceHost, "empty host")
	}
	if ip, ok := fenceParseHostIP(host); ok {
		if FenceAddrBlocked(ip) {
			return nil, fenceErr(FenceBlocked, "address %s refused", ip)
		}
		return []net.IP{ip}, nil
	}
	r := f.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ans, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fenceErr(FenceResolve, "lookup failed")
	}
	if len(ans) == 0 {
		return nil, fenceErr(FenceResolve, "no answers")
	}
	ips := make([]net.IP, 0, len(ans))
	for _, a := range ans {
		if FenceAddrBlocked(a.IP) {
			return nil, fenceErr(FenceBlocked, "an answer for the host is refused")
		}
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// DialContext resolves once, checks every answer, then dials the checked
// addresses directly in order. It never re-resolves.
func (f *NetFence) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fenceErr(FenceHost, "bad address")
	}
	if err := fenceCheckPort(port); err != nil {
		return nil, err
	}
	ips, err := f.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, ip := range ips {
		c, err := f.dial(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	var fe *FenceError
	if errors.As(last, &fe) {
		return nil, last
	}
	return nil, fenceErr(FenceConnect, "connect failed")
}

// CheckRedirect is the http.Client redirect policy: at most 5 hops, each hop
// re-checked (scheme, userinfo, host, port; the dialer re-checks the address),
// https to http refused, Authorization and Cookie dropped across origins.
func (f *NetFence) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > fenceMaxRedirects {
		return fenceErr(FenceRedirect, "more than %d redirects", fenceMaxRedirects)
	}
	if err := FenceCheckURL(req.URL); err != nil {
		return err
	}
	prev := via[len(via)-1]
	if prev.URL.Scheme == "https" && req.URL.Scheme == "http" {
		return fenceErr(FenceRedirect, "https to http refused")
	}
	if fenceOrigin(prev.URL) != fenceOrigin(req.URL) {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	return nil
}

func fenceOrigin(u *url.URL) string {
	p := u.Port()
	if p == "" {
		if u.Scheme == "https" {
			p = "443"
		} else {
			p = "80"
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + p
}

// Client returns an http.Client that can only reach what the fence allows: no
// proxy, no cookie jar, no keep-alive reuse, TLS 1.2+ against the original
// host name, 10 s to connect and 30 s in total.
func (f *NetFence) Client() *http.Client {
	var cfg *tls.Config
	if f.TLSConfig != nil {
		cfg = f.TLSConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	cfg.MinVersion = tls.VersionTLS12
	cfg.InsecureSkipVerify = false
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           f.DialContext,
		TLSClientConfig:       cfg,
		TLSHandshakeTimeout:   fenceConnectTimeout,
		ResponseHeaderTimeout: fenceTotalTimeout,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
	}
	return &http.Client{
		Transport:     fenceRoundTripper{f: f, next: tr},
		CheckRedirect: f.CheckRedirect,
		Jar:           nil,
		Timeout:       fenceTotalTimeout,
	}
}

// fenceRoundTripper checks the URL of every request, including the first.
type fenceRoundTripper struct {
	f    *NetFence
	next http.RoundTripper
}

func (r fenceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := FenceCheckURL(req.URL); err != nil {
		return nil, err
	}
	return r.next.RoundTrip(req)
}

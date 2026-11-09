package everything

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]string // host -> successive answer sets
	calls   map[string]int
}

func (r *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	sets, ok := r.answers[host]
	if !ok {
		return nil, errors.New("nxdomain")
	}
	i := r.calls[host]
	r.calls[host]++
	if i >= len(sets) {
		i = len(sets) - 1
	}
	var out []net.IPAddr
	for _, s := range sets[i] {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

type dialRec struct {
	mu    sync.Mutex
	addrs []string
}

func (d *dialRec) dial(_ context.Context, _, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.mu.Unlock()
	return nil, errors.New("fake dial")
}

func isFence(err error, class string) bool {
	var fe *FenceError
	return errors.As(err, &fe) && fe.Class == class
}

func TestFenceAddrBlocked(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.64.0.1", "100.127.255.254", "127.0.0.1", "127.9.9.9",
		"169.254.169.254", "169.254.0.1", "172.16.0.1", "172.31.255.255", "192.0.0.192", "192.0.2.7",
		"192.168.1.1", "198.18.0.1", "198.19.255.255", "198.51.100.9", "203.0.113.9", "224.0.0.1",
		"239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fd00:ec2::254", "fdff::1", "fe80::1", "febf::1", "fec0::1", "ff02::1",
		"64:ff9b::808:808", "64:ff9b::7f00:1", "64:ff9b:1::1", "2001::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2",
		"2001:db8::1", "2002::1", "2002:7f00:1::1", "2002:a9fe:a9fe::1", "100::1",
		// embedded IPv4
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1", "::ffff:192.168.0.1",
		"::127.0.0.1", "::10.0.0.1", "::169.254.169.254", "::ffff:0:0", "::0.0.0.2",
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil || !FenceAddrBlocked(ip) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34", "100.63.255.255", "100.128.0.1", "172.15.255.255",
		"172.32.0.1", "198.17.255.255", "198.20.0.1", "223.255.255.255", "192.0.1.1",
		"2606:4700:4700::1111", "2a00:1450:4009::200e", "2001:4860:4860::8888", "2001:1::1",
		"::ffff:8.8.8.8", "::8.8.8.8", "2003::1",
	}
	for _, s := range allowed {
		if ip := net.ParseIP(s); ip == nil || FenceAddrBlocked(ip) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !FenceAddrBlocked(nil) || !FenceAddrBlocked(net.IP{1, 2}) {
		t.Error("invalid ip must be blocked")
	}
}

func TestFenceParseHostIPSpellings(t *testing.T) {
	cases := map[string]string{
		"2130706433": "127.0.0.1", "0x7f000001": "127.0.0.1", "0x7f.1": "127.0.0.1", "017700000001": "127.0.0.1",
		"127.1": "127.0.0.1", "0177.0.0.1": "127.0.0.1", "0xa9.0xfe.0xa9.0xfe": "169.254.169.254",
		"2852039166": "169.254.169.254", "[::1]": "::1", "8.8.8.8": "8.8.8.8", "127.0.0.1.": "127.0.0.1",
	}
	for in, want := range cases {
		ip, ok := fenceParseHostIP(in)
		if !ok || ip.String() != want {
			t.Errorf("%q: got %v %v want %s", in, ip, ok, want)
		}
	}
	for _, in := range []string{"example.com", "1.2.3.4.5", "256.1.1.1", "99999999999", "a.b", "0x", "1..2", "9e9"} {
		if _, ok := fenceParseHostIP(in); ok {
			t.Errorf("%q must be a host name", in)
		}
	}
}

func TestFenceLiteralsRefusedWithoutLookup(t *testing.T) {
	res := &fakeResolver{}
	rec := &dialRec{}
	f := &NetFence{Resolver: res, Dial: rec.dial}
	for _, h := range []string{"127.0.0.1", "2130706433", "0x7f000001", "017700000001", "127.1", "[::1]", "::ffff:127.0.0.1",
		"169.254.169.254", "[fd00:ec2::254]", "[::ffff:a9fe:a9fe]", "10.0.0.1", "100.64.0.1"} {
		_, err := f.DialContext(context.Background(), "tcp", net.JoinHostPort(strings.Trim(h, "[]"), "80"))
		if !isFence(err, FenceBlocked) {
			t.Errorf("%s: %v", h, err)
		}
	}
	if len(rec.addrs) != 0 || len(res.calls) != 0 {
		t.Fatalf("dialed or resolved: %v %v", rec.addrs, res.calls)
	}
}

func TestFenceResolvedAnswers(t *testing.T) {
	res := &fakeResolver{answers: map[string][][]string{
		"good.example":   {{"93.184.216.34", "2606:4700::1"}},
		"priv.example":   {{"10.0.0.5"}},
		"mixed.example":  {{"93.184.216.34", "127.0.0.1"}},
		"mixed6.example": {{"2606:4700::1", "::1"}},
		"mapped.example": {{"::ffff:192.168.1.1"}},
		"meta.example":   {{"169.254.169.254"}},
		"meta6.example":  {{"fd00:ec2::254"}},
		"nat64.example":  {{"64:ff9b::a00:1"}},
		"sixto4.example": {{"2002:c0a8:101::1"}},
		"teredo.example": {{"2001:0:4136:e378:8000:63bf:3fff:fdd2"}},
		"cgnat.example":  {{"100.100.100.200"}},
		"empty.example":  {{}},
	}}
	rec := &dialRec{}
	f := &NetFence{Resolver: res, Dial: rec.dial}
	for _, h := range []string{"priv", "mixed", "mixed6", "mapped", "meta", "meta6", "nat64", "sixto4", "teredo", "cgnat"} {
		if _, err := f.DialContext(context.Background(), "tcp", h+".example:443"); !isFence(err, FenceBlocked) {
			t.Errorf("%s: %v", h, err)
		}
	}
	if _, err := f.DialContext(context.Background(), "tcp", "empty.example:443"); !isFence(err, FenceResolve) {
		t.Errorf("empty: %v", err)
	}
	if _, err := f.DialContext(context.Background(), "tcp", "nx.example:443"); !isFence(err, FenceResolve) {
		t.Errorf("nx: %v", err)
	}
	if len(rec.addrs) != 0 {
		t.Fatalf("a refused host was dialed: %v", rec.addrs)
	}
	// Allowed host: dials the checked IPs directly, in order, never the name.
	f.DialContext(context.Background(), "tcp", "good.example:443")
	if len(rec.addrs) != 2 || rec.addrs[0] != "93.184.216.34:443" || rec.addrs[1] != "[2606:4700::1]:443" {
		t.Fatalf("dialed %v", rec.addrs)
	}
}

func TestFenceRebindingNoSecondLookup(t *testing.T) {
	res := &fakeResolver{answers: map[string][][]string{"rebind.example": {{"93.184.216.34"}, {"127.0.0.1"}}}}
	rec := &dialRec{}
	f := &NetFence{Resolver: res, Dial: rec.dial}
	f.DialContext(context.Background(), "tcp", "rebind.example:80")
	if res.calls["rebind.example"] != 1 {
		t.Fatalf("lookups: %d", res.calls["rebind.example"])
	}
	if len(rec.addrs) != 1 || rec.addrs[0] != "93.184.216.34:80" {
		t.Fatalf("dialed %v (must be the checked IP, not the name)", rec.addrs)
	}
	// A later dial resolves again and the now-private answer is refused.
	_, err := f.DialContext(context.Background(), "tcp", "rebind.example:80")
	if !isFence(err, FenceBlocked) || len(rec.addrs) != 1 {
		t.Fatalf("second: %v %v", err, rec.addrs)
	}
}

func TestFencePorts(t *testing.T) {
	res := &fakeResolver{answers: map[string][][]string{"good.example": {{"93.184.216.34"}}}}
	rec := &dialRec{}
	f := &NetFence{Resolver: res, Dial: rec.dial}
	for _, p := range []string{"25", "110", "143", "465", "587", "993", "995", "0", "65536", "x"} {
		if _, err := f.DialContext(context.Background(), "tcp", "good.example:"+p); !isFence(err, FencePort) {
			t.Errorf("port %s: %v", p, err)
		}
		u, _ := url.Parse("http://good.example:" + p + "/")
		if p != "x" && FenceCheckURL(u) == nil {
			t.Errorf("url port %s allowed", p)
		}
	}
	if len(rec.addrs) != 0 {
		t.Fatal("dialed a refused port")
	}
	for _, p := range []string{"80", "443", "8080", "2525", "24"} {
		f.DialContext(context.Background(), "tcp", "good.example:"+p)
	}
	if len(rec.addrs) != 5 {
		t.Fatalf("dialed %v", rec.addrs)
	}
}

func TestFenceCheckURL(t *testing.T) {
	bad := map[string]string{
		"ftp://example.com/": FenceScheme, "file:///etc/passwd": FenceScheme, "ws://example.com/": FenceScheme,
		"gopher://example.com/": FenceScheme, "//example.com/": FenceScheme, "/x": FenceScheme,
		"http://user@example.com/": FenceUserinfo, "http://user:pw@example.com/": FenceUserinfo,
		"http:///x": FenceHost, "https://example.com:25/": FencePort, "https://example.com:587/": FencePort,
	}
	for s, class := range bad {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(s, err)
		}
		if err := FenceCheckURL(u); !isFence(err, class) {
			t.Errorf("%s: %v want %s", s, err, class)
		}
	}
	for _, s := range []string{"http://example.com/", "https://example.com:8443/x?y=1", "HTTPS://EXAMPLE.com"} {
		u, _ := url.Parse(s)
		if err := FenceCheckURL(u); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	if FenceCheckURL(nil) == nil {
		t.Error("nil url")
	}
}

func TestFenceControlChecksSocketAddress(t *testing.T) {
	for _, a := range []string{"127.0.0.1:80", "[::1]:80", "10.0.0.1:443", "169.254.169.254:80", "[::ffff:10.0.0.1]:80", "8.8.8.8:25", "bad", "name:80"} {
		if err := fenceControl("tcp", a, nil); err == nil {
			t.Errorf("%s allowed", a)
		}
	}
	if err := fenceControl("tcp4", "8.8.8.8:443", nil); err != nil {
		t.Error(err)
	}
	if err := fenceControl("tcp6", "[2606:4700::1]:443", nil); err != nil {
		t.Error(err)
	}
}

// The default dialer (Control, no fake Dial) refuses a real loopback listener
// even when the fence's resolution step is bypassed. No internet involved.
func TestFenceDefaultDialerRefusesLoopbackSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback")
	}
	defer ln.Close()
	f := &NetFence{}
	c, err := f.dial(context.Background(), "tcp", ln.Addr().String())
	if err == nil {
		c.Close()
		t.Fatal("connected to loopback through the fenced dialer")
	}
	if _, err := f.DialContext(context.Background(), "tcp", ln.Addr().String()); !isFence(err, FenceBlocked) {
		t.Fatalf("%v", err)
	}
}

func fenceTestClient(res FenceResolver, dial FenceDialFunc) *http.Client {
	return (&NetFence{Resolver: res, Dial: dial}).Client()
}

func TestFenceClientRedirects(t *testing.T) {
	var hits []string
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		auth = append(auth, r.Header.Get("Authorization")+"|"+r.Header.Get("Cookie"))
		switch r.URL.Path {
		case "/ok":
			io.WriteString(w, "fine")
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/to-private":
			http.Redirect(w, r, "http://priv.example/secret", 302)
		case "/to-literal":
			http.Redirect(w, r, "http://127.0.0.1/secret", 302)
		case "/to-meta":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", 302)
		case "/to-meta6":
			http.Redirect(w, r, "http://[fd00:ec2::254]/", 302)
		case "/to-decimal":
			http.Redirect(w, r, "http://2130706433/", 302)
		case "/to-mail":
			http.Redirect(w, r, "http://pub.example:25/", 302)
		case "/to-ftp":
			http.Redirect(w, r, "ftp://pub.example/", 302)
		case "/to-userinfo":
			http.Redirect(w, r, "http://u:p@pub.example/", 302)
		case "/to-other":
			http.Redirect(w, r, "http://other.example/ok", 302)
		case "/to-ok":
			http.Redirect(w, r, "/ok", 302)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	res := &fakeResolver{answers: map[string][][]string{
		"pub.example": {{"93.184.216.34"}}, "other.example": {{"93.184.216.35"}}, "priv.example": {{"192.168.1.10"}},
	}}
	var dialed []string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return net.Dial("tcp", "127.0.0.1:"+port) // test server stands in for the public host
	}
	c := fenceTestClient(res, dial)
	get := func(path string, hdr map[string]string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", "http://pub.example:"+port+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		return c.Do(req)
	}
	if r, err := get("/ok", nil); err != nil {
		t.Fatal(err)
	} else {
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if string(b) != "fine" || dialed[0] != "93.184.216.34:"+port {
			t.Fatalf("%s %v", b, dialed)
		}
	}
	if r, err := get("/to-ok", nil); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
	}
	for path, class := range map[string]string{
		"/to-private": FenceBlocked, "/to-literal": FenceBlocked, "/to-meta": FenceBlocked, "/to-meta6": FenceBlocked,
		"/to-decimal": FenceBlocked, "/to-mail": FencePort, "/to-ftp": FenceScheme, "/to-userinfo": FenceUserinfo,
		"/loop": FenceRedirect,
	} {
		hits = nil
		_, err := get(path, nil)
		if !isFence(err, class) {
			t.Errorf("%s: %v want %s", path, err, class)
		}
		for _, h := range hits {
			if h == "/secret" || h == "/latest/meta-data/" {
				t.Errorf("%s reached the private target", path)
			}
		}
	}
	hits = nil
	get("/loop", nil)
	if len(hits) != fenceMaxRedirects+1 {
		t.Errorf("loop made %d requests", len(hits))
	}
	// Credentials drop across origins, stay on the same origin.
	auth = nil
	if r, err := get("/to-other", map[string]string{"Authorization": "Bearer x", "Cookie": "a=b"}); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
	}
	if auth[0] != "Bearer x|a=b" || auth[1] != "|" {
		t.Errorf("auth across origins: %v", auth)
	}
	auth = nil
	if r, err := get("/to-ok", map[string]string{"Authorization": "Bearer x"}); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
	}
	if auth[1] != "Bearer x|" {
		t.Errorf("same origin lost auth: %v", auth)
	}
	// First request is fenced too.
	for _, u := range []string{"http://priv.example/", "http://127.0.0.1:" + port + "/ok", "http://pub.example:587/", "ftp://pub.example/", "http://u@pub.example/"} {
		if _, err := c.Get(u); err == nil {
			t.Errorf("%s allowed", u)
		}
	}
}

func TestFenceRedirectHttpsToHttpRefused(t *testing.T) {
	f := &NetFence{}
	mk := func(s string) *http.Request { r, _ := http.NewRequest("GET", s, nil); return r }
	if err := f.CheckRedirect(mk("http://a.example/"), []*http.Request{mk("https://a.example/")}); !isFence(err, FenceRedirect) {
		t.Fatalf("%v", err)
	}
	if err := f.CheckRedirect(mk("https://b.example/"), []*http.Request{mk("http://a.example/")}); err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, 6)
	for i := range via {
		via[i] = mk("https://a.example/")
	}
	if err := f.CheckRedirect(mk("https://a.example/"), via); !isFence(err, FenceRedirect) {
		t.Fatal(err)
	}
	if err := f.CheckRedirect(mk("https://a.example/"), via[:5]); err != nil {
		t.Fatal(err)
	}
}

func TestFenceClientSettings(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")
	t.Setenv("ALL_PROXY", "http://proxy.invalid:3128")
	c := (&NetFence{}).Client()
	if c.Jar != nil || c.Timeout != fenceTotalTimeout || c.CheckRedirect == nil {
		t.Fatalf("client: %+v", c)
	}
	tr := c.Transport.(fenceRoundTripper).next.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("proxy set")
	}
	if tr.TLSClientConfig.MinVersion < 0x0303 || tr.TLSClientConfig.InsecureSkipVerify || !tr.DisableKeepAlives {
		t.Fatalf("tls/keepalive: %+v", tr.TLSClientConfig)
	}
	// Even with proxy env set, nothing is sent to the proxy: a refused URL never dials.
	rec := &dialRec{}
	pc := (&NetFence{Resolver: &fakeResolver{answers: map[string][][]string{"p.example": {{"93.184.216.34"}}}}, Dial: rec.dial}).Client()
	pc.Get("http://p.example/")
	for _, a := range rec.addrs {
		if strings.Contains(a, "proxy") || a != "93.184.216.34:80" {
			t.Fatalf("dialed %v", rec.addrs)
		}
	}
	// A caller TLS config cannot lower the minimum or disable verification.
	f := &NetFence{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS10, InsecureSkipVerify: true}}
	tr2 := f.Client().Transport.(fenceRoundTripper).next.(*http.Transport)
	if tr2.TLSClientConfig.MinVersion != tls.VersionTLS12 || tr2.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("tls override: %+v", tr2.TLSClientConfig)
	}
}

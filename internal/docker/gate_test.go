package docker

import (
	"strings"
	"testing"
)

func gatedRoute() Route {
	return Route{
		Host: "navidrome.lan", Target: Target{Host: "navidrome-navidrome", Port: 4533},
		Exposure: ExposureLocal, TLS: TLSInternal,
		Gate: &Gate{Canonical: "navidrome.lan", CopyHeaders: []string{"X-Authentik-Username"}, Bypass: []string{`^/rest/.*`, `^/share/.*`}},
	}
}

func idx(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("missing %q in:\n%s", sub, s)
	}
	return i
}

func TestGatedRouteOrderAndFailClosed(t *testing.T) {
	out, err := RenderRoute(gatedRoute())
	if err != nil {
		t.Fatal(err)
	}
	strip := idx(t, out, "request_header -X-Authentik-*")
	bypass := idx(t, out, "@gate_bypass0 path_regexp ^/rest/.*")
	share := idx(t, out, "@gate_bypass1 path_regexp ^/share/.*")
	outpost := idx(t, out, "handle /outpost.goauthentik.io/* {")
	fa := idx(t, out, "forward_auth authentik-server:9000 {")
	if !(strip < bypass && bypass < share && share < outpost && outpost < fa) {
		t.Fatalf("handles out of order:\n%s", out)
	}
	for _, want := range []string{"request_header -Remote-User", "request_header -X-Forwarded-User", "request_header -X-Authentik-Username",
		"uri /outpost.goauthentik.io/auth/caddy", "copy_headers X-Authentik-Username"} {
		idx(t, out, want)
	}
	// The app is reached only in bypass handles (before) or after forward_auth.
	first := idx(t, out, "reverse_proxy navidrome-navidrome:4533")
	if first < bypass || first > outpost {
		t.Fatalf("the app is reachable outside a bypass or the forward-auth handle:\n%s", out)
	}
	if strings.Count(out, "reverse_proxy navidrome-navidrome:4533") != 3 {
		t.Fatalf("want 2 bypass handles and 1 gated proxy:\n%s", out)
	}
	last := strings.LastIndex(out, "reverse_proxy navidrome-navidrome:4533")
	if last < fa {
		t.Fatalf("the gated proxy must follow forward_auth:\n%s", out)
	}
	// Never an unconditional proxy after the strip, outside a handle.
	if strings.Contains(out, "\n\t\treverse_proxy navidrome") {
		t.Fatalf("app proxy outside a handle:\n%s", out)
	}
	// The alias redirects to the canonical name.
	idx(t, out, "navidrome.local {")
	idx(t, out, "redir https://navidrome.lan{uri} 308")
}

func TestGatedRouteTunnelCanonical(t *testing.T) {
	r := gatedRoute()
	r.Tunnel = []Hostname{"music.example.com", "music2.example.com"}
	r.Gate.Canonical = "music.example.com"
	out, err := RenderRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	idx(t, out, "http://music.example.com:8480 {")
	idx(t, out, "http://music2.example.com:8480 {")
	idx(t, out, "redir https://music.example.com{uri} 308")
	// The LAN names redirect to the public name, never serve the app.
	local := out[:idx(t, out, "# Tunnel entry")]
	if strings.Contains(local, "reverse_proxy navidrome") || strings.Contains(local, "forward_auth") {
		t.Fatalf("a non-canonical local name serves:\n%s", out)
	}
	tunnel := out[idx(t, out, "# Tunnel entry"):]
	idx(t, tunnel, "header_up X-Forwarded-Proto https")
	idx(t, tunnel, "forward_auth authentik-server:9000")
	idx(t, tunnel, "header_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}")
}

func TestGatedRouteLocalCanonicalRedirectsTunnel(t *testing.T) {
	r := gatedRoute()
	r.Tunnel = []Hostname{"music.example.com"}
	out, err := RenderRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	tunnel := out[idx(t, out, "# Tunnel entry"):]
	if strings.Contains(tunnel, "reverse_proxy") {
		t.Fatalf("a stale tunnel name must redirect, not serve:\n%s", tunnel)
	}
	idx(t, tunnel, "redir https://navidrome.lan{uri} 308")
}

func TestPlainRouteUnchangedByGateCode(t *testing.T) {
	r := gatedRoute()
	r.Gate = nil
	out, _ := RenderRoute(r)
	if strings.Contains(out, "forward_auth") || strings.Contains(out, "request_header") {
		t.Fatalf("an ungated route renders a gate:\n%s", out)
	}
}

func TestGateValidation(t *testing.T) {
	cases := map[string]func(*Route){
		"canonical not a name": func(r *Route) { r.Gate.Canonical = "other.lan" },
		"bare wildcard":        func(r *Route) { r.Gate.Bypass = []string{`^/.*`} },
		"unanchored":           func(r *Route) { r.Gate.Bypass = []string{`/rest/.*`} },
		"braces":               func(r *Route) { r.Gate.Bypass = []string{`^/a{2}`} },
		"outpost path":         func(r *Route) { r.Gate.Bypass = []string{`^/outpost.*`} },
		"bad header":           func(r *Route) { r.Gate.CopyHeaders = []string{"Authorization"} },
		"dashboard":            func(r *Route) { r.Dashboard = true },
		"path route":           func(r *Route) { r.Path = "/apps/navidrome/" },
	}
	for name, mutate := range cases {
		r := gatedRoute()
		mutate(&r)
		if _, err := RenderRoute(r); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	for _, ok := range []string{`^/rest/.*`, `^/share/.*`, `^/api/push/.*`, `^/icon\.svg$`, `^/status(/.*)?$`} {
		if err := ValidateBypass(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

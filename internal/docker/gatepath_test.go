package docker

import (
	"strings"
	"testing"
)

// gatedPathSet is the dashboard with Navidrome gated at its path (S472).
func gatedPathSet(published bool) RouteSet {
	return RouteSet{Routes: []Route{
		{Host: "homestead.lan", Dashboard: true, Exposure: ExposureLocal, TLS: TLSInternal,
			Aliases: []Hostname{"homestead.local"}, Tunnel: []Hostname{"homestead.example.com"}},
		{Host: "navidrome.lan", Target: Target{Host: "navidrome-navidrome", Port: 4533}, Exposure: ExposureLocal, TLS: TLSInternal,
			Path: "/apps/navidrome/", Published: published,
			Gate: &Gate{Canonical: "homestead.lan", CopyHeaders: []string{"X-Authentik-Username"},
				Bypass: []string{`^/apps/navidrome/rest/.*`, `^/apps/navidrome/share/.*`}}},
	}}
}

func TestGatedPathLANOrderAndCookieAfterGate(t *testing.T) {
	local, tunnel := renderDashboard(t, gatedPathSet(false))
	strip := idx(t, local, "request_header -X-Authentik-*")
	bypass := idx(t, local, "path_regexp ^/apps/navidrome/rest/.*")
	share := idx(t, local, "path_regexp ^/apps/navidrome/share/.*")
	fa := idx(t, local, "forward_auth authentik-server:9000 {")
	cookie := strings.LastIndex(local, "request_header Cookie")
	if !(strip < bypass && bypass < share && share < fa && fa < cookie) {
		t.Fatalf("gate order wrong (the cookie strip must follow forward_auth):\n%s", local)
	}
	idx(t, local, "uri /outpost.goauthentik.io/auth/caddy")
	idx(t, local, "copy_headers X-Authentik-Username")
	// The site-level outpost handle, and the other local name redirects.
	idx(t, local, "handle /outpost.goauthentik.io/* {")
	idx(t, local, "not host homestead.lan")
	idx(t, local, "redir @gate_apps_navidrome_other https://homestead.lan{uri} 308")
	// Not published: nothing about it on the tunnel entry.
	if strings.Contains(tunnel, "navidrome") || strings.Contains(tunnel, "outpost") {
		t.Fatalf("an unpublished gated app is on the tunnel entry:\n%s", tunnel)
	}
	// The app is reached only in a bypass or after forward_auth.
	if strings.Count(local, "reverse_proxy navidrome-navidrome:4533") != 3 {
		t.Fatalf("want two bypass proxies and one gated proxy:\n%s", local)
	}
	if first := idx(t, local, "reverse_proxy navidrome-navidrome:4533"); first < bypass {
		t.Fatalf("the app is reachable before its bypass match:\n%s", local)
	}
}

func TestGatedPathPublishedLANRedirectsAndTunnelGates(t *testing.T) {
	local, tunnel := renderDashboard(t, gatedPathSet(true))
	if strings.Contains(local, "forward_auth") || strings.Contains(local, "reverse_proxy navidrome") || strings.Contains(local, "outpost.goauthentik.io/* {") {
		t.Fatalf("a published gated app serves on the local names:\n%s", local)
	}
	idx(t, local, "redir https://homestead.example.com{uri} 308")
	idx(t, tunnel, "handle /outpost.goauthentik.io/* {")
	idx(t, tunnel, "forward_auth authentik-server:9000 {")
	idx(t, tunnel, "header_up X-Forwarded-Proto https")
	idx(t, tunnel, "path_regexp ^/apps/navidrome/rest/.*")
	idx(t, tunnel, "header_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}")
}

func TestGatedPathValidation(t *testing.T) {
	set := gatedPathSet(false)
	set.Routes[1].Gate.Bypass = []string{`^/rest/.*`}
	if err := set.Validate(); err == nil || !strings.Contains(err.Error(), "must start with ^/apps/navidrome/") {
		t.Fatalf("a bypass outside the app's path must be refused: %v", err)
	}
	set = gatedPathSet(false)
	set.Routes[1].StripPrefix = true
	if err := set.Validate(); err == nil {
		t.Fatal("a stripped path route must not be gated")
	}
	// HealGates leaves a path gate alone.
	set = gatedPathSet(false)
	HealGates(set.Routes)
	if set.Routes[1].Gate.Canonical != "homestead.lan" {
		t.Fatalf("HealGates moved a path gate: %+v", set.Routes[1].Gate)
	}
}

func TestPathBypass(t *testing.T) {
	for in, want := range map[string]string{
		`^/rest/.*`:                `^/apps/navidrome/rest/.*`,
		`^/app/(assets|static)/.*`: `^/apps/navidrome/app/(assets|static)/.*`,
	} {
		if got := PathBypass("/apps/navidrome/", in); got != want {
			t.Errorf("PathBypass(%q) = %q, want %q", in, got, want)
		}
		if err := ValidateBypass(PathBypass("/apps/navidrome/", in)); err != nil {
			t.Errorf("%v", err)
		}
	}
}

package docker

import (
	"strings"
	"testing"
)

func moveTable(published ...string) []Route {
	dash := Route{Host: "homestead.lan", Dashboard: true, Tunnel: []Hostname{"home.example.com"}}
	routes := []Route{dash}
	for _, n := range []string{"navidrome", "kuma", "wordcraft"} {
		r := Route{Host: Hostname(n + ".lan"), Path: AppsPrefix + n + "/", Gate: &Gate{}}
		for _, p := range published {
			if p == n {
				r.Published = true
			}
		}
		routes = append(routes, r)
	}
	return routes
}

func titles(r Route) string { return strings.Trim(strings.TrimPrefix(r.Path, AppsPrefix), "/") }

// S504: publishing the first gated path app moves the shared gate to the
// public name, so every other gated, unpublished app is named.
func TestGateMoveLANToPublicNamesOtherGatedApps(t *testing.T) {
	m := GateCanonicalMove(moveTable(), "/apps/kuma/", func(r *Route) { r.Published = true })
	if !m.Moved() || m.From != "homestead.lan" || m.To != "home.example.com" {
		t.Fatalf("move = %+v", m)
	}
	lines := strings.Join(m.Lines(titles), "\n")
	if len(m.Affected) != 2 || strings.Contains(lines, "kuma") || !strings.Contains(lines, "navidrome is gated but not published: its local address homestead.lan/apps/navidrome/ will redirect to home.example.com") ||
		!strings.Contains(lines, "unreachable until it is published or its sign-in gate is turned off") {
		t.Fatalf("lines:\n%s", lines)
	}
}

func TestGateMovePublicToLAN(t *testing.T) {
	m := GateCanonicalMove(moveTable("kuma"), "/apps/kuma/", func(r *Route) { r.Published = false })
	if !m.Moved() || m.To != "homestead.lan" {
		t.Fatalf("move = %+v", m)
	}
	lines := strings.Join(m.Lines(titles), "\n")
	if !strings.Contains(lines, "navidrome is gated but not published: its local address homestead.lan/apps/navidrome/ stops redirecting to home.example.com") {
		t.Fatalf("lines:\n%s", lines)
	}
}

func TestGateMoveGatingAPublishedApp(t *testing.T) {
	routes := moveTable("kuma")
	routes[2].Gate = nil
	m := GateCanonicalMove(routes, "/apps/kuma/", func(r *Route) { r.Gate = &Gate{} })
	if !m.Moved() || len(m.Affected) != 2 {
		t.Fatalf("move = %+v", m)
	}
}

func TestGateMoveQuietWhenCanonicalStays(t *testing.T) {
	// Another published gated app keeps the canonical public.
	m := GateCanonicalMove(moveTable("navidrome", "kuma"), "/apps/kuma/", func(r *Route) { r.Published = false })
	if m.Moved() || len(m.Lines(titles)) != 0 {
		t.Fatalf("move = %+v", m)
	}
	// No other gated path app: nothing to name even though it moves.
	only := moveTable()[:2]
	m = GateCanonicalMove(only, "/apps/navidrome/", func(r *Route) { r.Published = true })
	if !m.Moved() || len(m.Lines(titles)) != 0 {
		t.Fatalf("move = %+v", m)
	}
	// An ungated app being published does not move the gate.
	routes := moveTable()
	routes[1].Gate = nil
	m = GateCanonicalMove(routes, "/apps/navidrome/", func(r *Route) { r.Published = true })
	if m.Moved() {
		t.Fatalf("move = %+v", m)
	}
}

func TestGateMoveSkipsTunnelOnlyGates(t *testing.T) {
	routes := moveTable()
	routes[3].Gate = &Gate{TunnelOnly: true}
	m := GateCanonicalMove(routes, "/apps/navidrome/", func(r *Route) { r.Published = true })
	if len(m.Affected) != 1 || m.Affected[0].Path != "/apps/kuma/" {
		t.Fatalf("affected = %+v", m.Affected)
	}
}

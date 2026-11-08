package appsso

import (
	"strings"
	"testing"

	"homestead/internal/docker"
)

// pathInputs is Navidrome served at its path on the dashboard's names
// (ADR-061; routes resolved as docker.ServedRoutes does).
func pathInputs(t *testing.T, published bool) Inputs {
	m := manifest(t, "navidrome")
	r := route("navidrome.lan", "navidrome")
	r.Path, r.Published = "/apps/navidrome/", published
	dash := docker.Route{Host: "homestead.lan", Dashboard: true, Aliases: []docker.Hostname{"homestead.local"}, Tunnel: []docker.Hostname{"homestead.example.com"}}
	table := []docker.Route{dash, r, route("authentik.lan", "server")}
	in := faInputs(t)
	in.Manifest = m
	in.Routes = docker.ServedRoutes(table, AppRoutes(m, table))
	return in
}

func TestForwardPlanAtPathLAN(t *testing.T) {
	in := pathInputs(t, false)
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Gate.Canonical != "homestead.lan" || p.RedirectURIs[0] != "https://homestead.lan" || p.SignInURL != "https://homestead.lan/apps/navidrome/" {
		t.Fatalf("plan: %+v", p)
	}
	if p.Gate.Bypass[0].Path != `^/apps/navidrome/rest/.*` || p.Gate.Bypass[1].Path != `^/apps/navidrome/share/.*` || p.Gate.Bypass[2].Path != `^/apps/navidrome/app/(assets|static)/.*` {
		t.Fatalf("bypass must carry the prefix: %+v", p.Gate.Bypass)
	}
	if got := strings.Join(p.Gate.Redirects, ","); got != "homestead.local" {
		t.Fatalf("redirects = %q", got)
	}
	g := p.Gate.gate()
	if g.Bypass[0] != `^/apps/navidrome/rest/.*` || g.Canonical != "homestead.lan" {
		t.Fatalf("docker gate: %+v", g)
	}
}

func TestForwardPlanAtPathPublishedUsesTheDashboardName(t *testing.T) {
	in := pathInputs(t, true)
	in.PublicAuthentikURL = "https://homestead.example.com/apps/auth"
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Gate.Canonical != "homestead.example.com" || p.RedirectURIs[0] != "https://homestead.example.com" || p.SignInURL != "https://homestead.example.com/apps/navidrome/" {
		t.Fatalf("plan: %+v", p)
	}
	// The same app on its host form has a different revision, so a moved
	// app shows a re-plan.
	host, _ := BuildPlan(faInputs(t), true)
	if host.Gate.Revision == p.Gate.Revision {
		t.Fatal("the gate revision ignores the path prefix")
	}
}

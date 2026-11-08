package appsso

import (
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/sso"
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

func TestForwardPlanAtPathUsesTheSharedGate(t *testing.T) {
	in := pathInputs(t, true)
	in.PublicAuthentikURL = "https://homestead.example.com/apps/auth"
	in.OtherGates = []OtherGate{{Path: "/apps/wordcraft/", Bypass: []string{"^/apps/wordcraft/open/.*"}}, {Path: "/apps/navidrome/", Bypass: []string{"^/apps/navidrome/x/.*"}}}
	p, err := BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	g := p.Gate
	if !g.Shared || p.Provider != SharedGateProvider {
		t.Fatalf("not on the shared gate: %+v", p)
	}
	want := []string{"homestead-navidrome-users", "homestead-navidrome-admins", "homestead-owner"}
	if strings.Join(g.Groups, ",") != strings.Join(want, ",") || strings.Join(g.gate().Groups, ",") != strings.Join(want, ",") {
		t.Fatalf("groups = %v", g.Groups)
	}
	// The provider's open paths: this app's and the other app's, never the
	// stale entry of this app's own path.
	pb := strings.Join(g.ProviderBypass, " ")
	if !strings.Contains(pb, "^/apps/wordcraft/open/.*") || !strings.Contains(pb, "^/apps/navidrome/rest/.*") || strings.Contains(pb, "navidrome/x/") {
		t.Fatalf("provider bypass = %v", g.ProviderBypass)
	}
	if strings.Join(g.OtherApps, ",") != "/apps/wordcraft/" {
		t.Fatalf("others = %v", g.OtherApps)
	}
	if len(p.Access.Bindings) != 0 || len(p.Access.Groups) != 3 {
		t.Fatalf("the shared application binds nobody; the app keeps its groups: %+v", p.Access)
	}
	spec := p.proxySpec(sso.Spec{})
	if spec.ProviderName != SharedGateProvider || spec.AppSlug != SharedGateSlug || spec.ExternalHost != "https://homestead.example.com" {
		t.Fatalf("spec: %+v", spec)
	}
	off, err := BuildPlan(func() Inputs {
		i := in
		i.Applied = &Record{Package: "navidrome", Issuer: p.Issuer, RedirectURIs: p.RedirectURIs, Access: p.Access}
		return i
	}(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !off.Gate.Shared || len(off.Gate.OtherApps) != 1 || !strings.Contains(strings.Join(off.Changes, " "), "The gate stays for /apps/wordcraft/") {
		t.Fatalf("off plan must keep the gate for the other app: %+v", off)
	}
}

// A craft app (S481, ADR-062 section 6) is gated on its public path only:
// not offered until published, then a tunnel-only gate with the app's
// groups, no redirects and the LAN left open.
func TestForwardPlanCraftIsTunnelOnly(t *testing.T) {
	build := func(published bool) (Plan, error) {
		m := manifest(t, "figcraft")
		r := route("figcraft.lan", "figcraft")
		r.Path, r.Published, r.StripPrefix, r.CraftApp = "/apps/figcraft/", published, true, "figcraft"
		dash := docker.Route{Host: "homestead.lan", Dashboard: true, Aliases: []docker.Hostname{"homestead.local"}, Tunnel: []docker.Hostname{"homestead.example.com"}}
		table := []docker.Route{dash, r, route("authentik.lan", "server")}
		in := faInputs(t)
		in.Manifest = m
		in.Routes = docker.ServedRoutes(table, AppRoutes(m, table))
		in.PublicAuthentikURL = "https://homestead.example.com/apps/auth"
		return BuildPlan(in, true)
	}
	if _, err := build(false); err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("unpublished craft plan: %v", err)
	}
	p, err := build(true)
	if err != nil {
		t.Fatal(err)
	}
	g := p.Gate
	if !g.Shared || !g.TunnelOnly || len(g.Redirects) != 0 || len(g.Bypass) != 0 || g.Canonical != "homestead.example.com" {
		t.Fatalf("gate: %+v", g)
	}
	if got := strings.Join(g.Groups, ","); got != "homestead-figcraft-users,homestead-figcraft-admins,homestead-owner" {
		t.Fatalf("groups = %q", got)
	}
	if dg := g.gate(); !dg.TunnelOnly || len(dg.Groups) != 3 {
		t.Fatalf("docker gate: %+v", dg)
	}
	if !strings.Contains(strings.Join(p.Changes, "\n"), "local address stays open") {
		t.Fatalf("changes: %v", p.Changes)
	}
}

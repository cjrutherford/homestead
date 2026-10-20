package docker

import (
	"strings"
	"testing"
)

func dashRoute() Route {
	return Route{Host: "homestead.lan", Aliases: []Hostname{"homestead.local"}, Exposure: ExposureLocal, TLS: TLSInternal, Dashboard: true}
}

func TestRenderDashboardRouteBothNames(t *testing.T) {
	out, err := RenderRoute(dashRoute())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"homestead.lan, homestead.local {",
		"tls internal",
		"reverse_proxy unix/" + DashboardSocketPath,
		"header_up X-Forwarded-For {http.request.remote.host}",
		"flush_interval -1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "trusted_proxies") {
		t.Error("trusted_proxies must stay unset")
	}
}

func TestDashboardRouteValidation(t *testing.T) {
	for name, mod := range map[string]func(*Route){
		"public":    func(r *Route) { r.Exposure = ExposurePublic },
		"auto tls":  func(r *Route) { r.TLS = TLSAuto },
		"tls off":   func(r *Route) { r.TLS = TLSOff },
		"craft":     func(r *Route) { r.CraftApp = "x" },
		"dup alias": func(r *Route) { r.Aliases = []Hostname{"homestead.lan"} },
	} {
		r := dashRoute()
		mod(&r)
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	plain := Route{Host: "a.lan", Aliases: []Hostname{"b.lan"}, Target: Target{Host: "a", Port: 1}, Exposure: ExposureLocal, TLS: TLSInternal}
	if plain.Validate() == nil {
		t.Error("aliases on a non-dashboard route accepted")
	}
	other := dashRoute()
	other.Host, other.Aliases = "other.lan", nil
	if (RouteSet{Routes: []Route{dashRoute(), other}}).Validate() == nil {
		t.Error("two dashboard routes accepted")
	}
	clash := Route{Host: "homestead.local", Target: Target{Host: "a", Port: 1}, Exposure: ExposureLocal, TLS: TLSInternal}
	if (RouteSet{Routes: []Route{dashRoute(), clash}}).Validate() == nil {
		t.Error("alias clash accepted")
	}
	if _, ok := (RouteSet{Routes: []Route{dashRoute()}}).DashboardRoute(); !ok {
		t.Error("DashboardRoute not found")
	}
}

func TestComposeMountsDashboardDirReadOnly(t *testing.T) {
	if !strings.Contains(composeFile(), "- "+DashboardSocketDir+":"+DashboardSocketDir+":ro") {
		t.Fatal("compose does not mount the dashboard socket directory read-only")
	}
}

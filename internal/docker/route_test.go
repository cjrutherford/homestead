package docker

import (
	"strings"
	"testing"
)

func validRoute() Route {
	return Route{
		Host:     "app.lan",
		Target:   Target{Host: "app", Port: 8080},
		Exposure: ExposureLocal,
		TLS:      TLSInternal,
		Health:   HealthCheck{Path: "/healthz", IntervalSeconds: 30},
	}
}

func TestRouteValid(t *testing.T) {
	r := validRoute()
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bare := Route{
		Host:     "homestead",
		Target:   Target{Host: "192.168.1.10", Port: 80},
		Exposure: ExposureOverlay,
		TLS:      TLSOff,
	}
	if err := bare.Validate(); err != nil {
		t.Fatalf("bare Validate: %v", err)
	}
}

func TestRouteInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Route)
		want   string
	}{
		{"empty host", func(r *Route) { r.Host = "" }, "route.host"},
		{"uppercase host", func(r *Route) { r.Host = "App.lan" }, "lowercase"},
		{"wildcard host", func(r *Route) { r.Host = "*.lan" }, "route.host"},
		{"empty label", func(r *Route) { r.Host = "app..lan" }, "empty labels"},
		{"bad label", func(r *Route) { r.Host = "app_.lan" }, "invalid label"},
		{"empty target", func(r *Route) { r.Target.Host = "" }, "route.target.host"},
		{"url target", func(r *Route) { r.Target.Host = "http://app:8080" }, "not a URL"},
		{"zero port", func(r *Route) { r.Target.Port = 0 }, "route.target.port"},
		{"high port", func(r *Route) { r.Target.Port = 70000 }, "route.target.port"},
		{"bad exposure", func(r *Route) { r.Exposure = "dmz" }, "route.exposure"},
		{"bad tls", func(r *Route) { r.TLS = "letsencrypt" }, "route.tls"},
		{"relative probe", func(r *Route) { r.Health.Path = "healthz" }, "start with /"},
		{"fast probe", func(r *Route) { r.Health.IntervalSeconds = 1 }, "route.health.interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRoute()
			tc.mutate(&r)
			err := r.Validate()
			if err == nil {
				t.Fatal("accepted invalid route")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestExposureAndTLSValues(t *testing.T) {
	for _, e := range []Exposure{ExposureLocal, ExposureOverlay, ExposurePublic} {
		if !e.Valid() {
			t.Fatalf("%q not valid", e)
		}
	}
	if (Exposure("dmz")).Valid() {
		t.Fatal("dmz valid")
	}
	for _, m := range []TLSMode{TLSOff, TLSInternal, TLSAuto} {
		if !m.Valid() {
			t.Fatalf("%q not valid", m)
		}
	}
	if (TLSMode("acme")).Valid() {
		t.Fatal("acme valid")
	}
}

func TestRouteSet(t *testing.T) {
	set := RouteSet{Routes: []Route{
		func() Route { r := validRoute(); r.Host = "wiki.lan"; return r }(),
		validRoute(),
	}}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sorted := set.Sorted()
	if sorted[0].Host != "app.lan" || sorted[1].Host != "wiki.lan" {
		t.Fatalf("sorted = %v", sorted)
	}
	got, ok := set.Find("wiki.lan")
	if !ok || string(got.Target.Host) != "app" {
		t.Fatalf("Find = %+v %v", got, ok)
	}
	if _, ok := set.Find("missing.lan"); ok {
		t.Fatal("found missing host")
	}
	dup := RouteSet{Routes: []Route{validRoute(), validRoute()}}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("dup err = %v", err)
	}
	bad := RouteSet{Routes: []Route{func() Route { r := validRoute(); r.TLS = "x"; return r }()}}
	if err := bad.Validate(); err == nil {
		t.Fatal("accepted invalid member")
	}
}

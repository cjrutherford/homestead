package jellyfin

import (
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

func routeManifest(t *testing.T) catalog.Manifest {
	t.Helper()
	cat, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	manifest, err := cat.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return manifest
}

func TestDefaultHostname(t *testing.T) {
	host, err := DefaultHostname("lan")
	if err != nil {
		t.Fatalf("DefaultHostname: %v", err)
	}
	if host != "jellyfin.lan" {
		t.Fatalf("host = %q", host)
	}
	if _, err := DefaultHostname("BAD!"); err == nil {
		t.Fatal("accepted bad domain")
	}
	if _, err := DefaultHostname(""); err == nil {
		t.Fatal("accepted empty domain")
	}
}

func TestRouteFor(t *testing.T) {
	manifest := routeManifest(t)
	route, err := RouteFor(manifest, "jellyfin.lan", "jellyfin", 8096)
	if err != nil {
		t.Fatalf("RouteFor: %v", err)
	}
	if route.Host != "jellyfin.lan" || route.Target.Host != "jellyfin" ||
		route.Target.Port != 8096 || route.Exposure != docker.ExposureLocal ||
		route.TLS != docker.TLSInternal ||
		route.Health.Path != HealthPath || route.Health.IntervalSeconds != HealthIntervalSeconds {
		t.Fatalf("route = %+v", route)
	}
	// The route renders through the shared generator with probing.
	text, err := docker.RenderRoute(route)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	for _, want := range []string{"jellyfin.lan, jellyfin.local {", "tls internal", "health_uri /System/Info/Public"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if _, err := RouteFor(manifest, "Bad!", "jellyfin", 8096); err == nil {
		t.Fatal("built bad hostname")
	}
	if _, err := RouteFor(manifest, "jellyfin.lan", "jellyfin", 0); err == nil {
		t.Fatal("built bad port")
	}
	if _, err := RouteFor(catalog.Manifest{}, "jellyfin.lan", "jellyfin", 8096); err == nil {
		t.Fatal("built from invalid manifest")
	}
}

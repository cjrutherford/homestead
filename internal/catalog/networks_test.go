package catalog

import (
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/exposure"
)

func recommendManifest() Manifest {
	manifest := validManifest()
	manifest.Exposure.Endpoints = append(manifest.Exposure.Endpoints, exposure.EndpointExposure{
		Endpoint: "admin", Recommended: docker.ExposureLocal,
		Warnings: []string{"Admin surface carries full control."},
	})
	manifest.Services = append(manifest.Services, Service{
		Name: "admin", Image: "jellyfin/admin:1.0.0", Exposes: "admin",
	})
	return manifest
}

func TestRecommendRoutes(t *testing.T) {
	got, err := RecommendRoutes(recommendManifest())
	if err != nil {
		t.Fatalf("RecommendRoutes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("recommendations = %+v", got)
	}
	web := got[0]
	if web.Endpoint != "web" || web.Service != "jellyfin" ||
		web.Recommended != docker.ExposureLocal || web.TLS != docker.TLSInternal {
		t.Fatalf("web = %+v", web)
	}
	public := recommendManifest()
	public.Exposure.Endpoints[0].Recommended = docker.ExposurePublic
	public.Exposure.Endpoints[0].AllowWider = true
	got, err = RecommendRoutes(public)
	if err != nil {
		t.Fatalf("RecommendRoutes: %v", err)
	}
	if got[0].TLS != docker.TLSAuto {
		t.Fatalf("public tls = %+v", got[0])
	}
	if _, err := RecommendRoutes(Manifest{}); err == nil {
		t.Fatal("recommended from invalid manifest")
	}
}

func TestBuildRoute(t *testing.T) {
	got, err := RecommendRoutes(recommendManifest())
	if err != nil {
		t.Fatalf("RecommendRoutes: %v", err)
	}
	route, err := BuildRoute(got[0], "jellyfin.lan", "jellyfin", 8096)
	if err != nil {
		t.Fatalf("BuildRoute: %v", err)
	}
	if route.Host != "jellyfin.lan" || route.Target.Host != "jellyfin" ||
		route.Target.Port != 8096 || route.Exposure != docker.ExposureLocal ||
		route.TLS != docker.TLSInternal {
		t.Fatalf("route = %+v", route)
	}
	if _, err := BuildRoute(got[0], "Bad!", "x", 1); err == nil {
		t.Fatal("built bad hostname")
	}
	if _, err := BuildRoute(got[0], "jellyfin.lan", "x", 0); err == nil {
		t.Fatal("built bad port")
	}
}

func TestExposureFor(t *testing.T) {
	declared, err := ExposureFor(recommendManifest(), "jellyfin")
	if err != nil {
		t.Fatalf("ExposureFor: %v", err)
	}
	if declared.Endpoint != "web" || !declared.AllowWider {
		t.Fatalf("declared = %+v", declared)
	}
	if _, err := ExposureFor(recommendManifest(), "ghost"); err == nil ||
		!strings.Contains(err.Error(), "serves no endpoint") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ExposureFor(Manifest{}, "jellyfin"); err == nil {
		t.Fatal("looked up invalid manifest")
	}
}

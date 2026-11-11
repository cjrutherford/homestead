package server

import (
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/catalog"
	"homestead/internal/docker"
)

func loadManifest(t *testing.T, name string) catalog.Manifest {
	t.Helper()
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := entries.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// S504: the publish warning lists the paths a gated app leaves open, and
// says nothing of the kind for an app with none.
func TestPublishWarningListsBypassPaths(t *testing.T) {
	nav := loadManifest(t, "navidrome")
	got := openToInternetWarning(nav, true, "https://home.example.com/apps/navidrome/", "/apps/navidrome/")
	for _, want := range []string{"checked with Authentik first", "Open without the gate: ^/apps/navidrome/rest/.*", "/apps/navidrome/share/", "/apps/navidrome/app/(assets|static)/"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	plain := nav
	fa := *nav.SSO.ForwardAuth
	fa.Bypass = nil
	plain.SSO.ForwardAuth = &fa
	if got := openToInternetWarning(plain, true, "https://home.example.com/apps/navidrome/", "/apps/navidrome/"); strings.Contains(got, "Open without the gate") || !strings.HasSuffix(got, "checked with Authentik first.") {
		t.Fatalf("no bypass: %q", got)
	}
}

// S504: publishing or unpublishing a gated path app names the other gated
// apps whose local address moves, in the warnings the digest covers.
func TestPublishPlanNamesGatedAppsWhoseLocalAddressMoves(t *testing.T) {
	dash := docker.Route{Host: "homestead.lan", Dashboard: true, Tunnel: []docker.Hostname{"home.example.com"}}
	nav := docker.Route{Host: "navidrome.lan", Path: "/apps/navidrome/", Gate: &docker.Gate{}}
	kuma := docker.Route{Host: "kuma.lan", Path: "/apps/uptime-kuma/", Gate: &docker.Gate{}}
	p := &appPublic{catalogDir: filepath.Join("..", "..", "catalog")}
	f := appPublicFacts{all: []docker.Route{dash, nav, kuma}, routes: []docker.Route{kuma}}
	lines := p.gateMoveWarnings(f, true)
	if len(lines) != 1 || !strings.Contains(lines[0], "Navidrome is gated but not published: its local address homestead.lan/apps/navidrome/ will redirect to home.example.com") {
		t.Fatalf("publish: %v", lines)
	}
	plan := appPublicPlan{Warnings: lines}
	before := plan.digest()
	plan.Warnings = nil
	if plan.digest() == before {
		t.Fatal("the digest does not cover the warning")
	}
	kuma.Published = true
	f = appPublicFacts{all: []docker.Route{dash, nav, kuma}, routes: []docker.Route{kuma}}
	if lines := p.gateMoveWarnings(f, false); len(lines) != 1 || !strings.Contains(lines[0], "stops redirecting to home.example.com") {
		t.Fatalf("unpublish: %v", lines)
	}
	// Canonical stays: another gated app is published.
	nav.Published = true
	f = appPublicFacts{all: []docker.Route{dash, nav, kuma}, routes: []docker.Route{kuma}}
	if lines := p.gateMoveWarnings(f, false); len(lines) != 0 {
		t.Fatalf("unmoved: %v", lines)
	}
}

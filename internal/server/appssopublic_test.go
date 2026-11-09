package server

import (
	"strings"
	"testing"

	"homestead/internal/docker"
)

// ADR-047 §1, §3: published apps get Authentik's public address only
// while the edge answers it on this box; until then the owner is told
// what to do, and nothing is offered while Authentik is unpublished.
func TestPublicAuthentik(t *testing.T) {
	served := docker.Hostname("")
	prev := signInSiteServed
	signInSiteServed = func(docker.RouteSet) (docker.Hostname, bool) { return served, served != "" }
	t.Cleanup(func() { signInSiteServed = prev })
	set := func(published bool) docker.RouteSet {
		return docker.RouteSet{Routes: []docker.Route{
			{Host: "homestead.lan", Dashboard: true, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
				Tunnel: []docker.Hostname{"homestead.example.com"}},
			{Host: "authentik.lan", Target: docker.Target{Host: "server", Port: 9000}, Exposure: docker.ExposureLocal,
				TLS: docker.TLSInternal, Path: "/apps/auth/", Published: published},
		}}
	}
	if url, waiting := publicAuthentik(set(false)); url != "" || waiting != "" {
		t.Fatalf("unpublished = %q %q", url, waiting)
	}
	if url, waiting := publicAuthentik(set(true)); url != "" || !strings.Contains(waiting, "Add a certificate for homestead.example.com in TLS") {
		t.Fatalf("no certificate = %q %q", url, waiting)
	}
	served = "homestead.example.com"
	if url, waiting := publicAuthentik(set(true)); url != "https://homestead.example.com/apps/auth" || waiting != "" {
		t.Fatalf("served = %q %q", url, waiting)
	}
}

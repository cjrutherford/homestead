package server

import (
	"strings"
	"testing"

	"homestead/internal/docker"
)

func TestStampSingleOriginFollowsTheCatalog(t *testing.T) {
	routes := []docker.Route{
		{Host: "gitea.lan", Target: docker.Target{Host: "gitea", Port: 3000}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
		{Host: "freshrss.lan", Target: docker.Target{Host: "freshrss", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, SingleOrigin: true},
	}
	if !stampSingleOrigin("../../catalog", routes) {
		t.Fatal("expected a change")
	}
	if !routes[0].SingleOrigin || routes[1].SingleOrigin {
		t.Fatalf("gitea must be marked and freshrss cleared: %+v", routes)
	}
	if stampSingleOrigin("../../catalog", routes) {
		t.Fatal("stamping is idempotent")
	}
}

func TestSingleOriginWarningPreviewsTheRedirect(t *testing.T) {
	r := docker.Route{Host: "gitea.lan", Target: docker.Target{Host: "gitea", Port: 3000}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	add := singleOriginWarning(r, appPublicPlan{Action: "add", Hostname: "gitea.example.com"})
	for _, want := range []string{"gitea.lan and gitea.local", "307", "https://gitea.example.com", "tunnel"} {
		if !strings.Contains(add, want) {
			t.Fatalf("add warning lacks %q: %s", want, add)
		}
	}
	if rm := singleOriginWarning(r, appPublicPlan{Action: "remove", Hostname: "gitea.example.com"}); !strings.Contains(rm, "stop redirecting") {
		t.Fatal(rm)
	}
}

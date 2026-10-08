package jellyfin

import (
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/exposure"
	"homestead/internal/firewall"
)

func exposureRoute(t *testing.T) docker.Route {
	route, err := RouteFor(routeManifest(t), "jellyfin.lan", "jellyfin", 8096)
	if err != nil {
		t.Fatalf("RouteFor: %v", err)
	}
	return route
}

func exposureEndpoint() exposure.EndpointExposure {
	return exposure.EndpointExposure{
		Endpoint: "web", Recommended: docker.ExposureLocal, AllowWider: true,
		Warnings: []string{"Media libraries leave the LAN when widened."},
	}
}

func TestDecideLocal(t *testing.T) {
	decision, err := Decide(exposureRoute(t), docker.ExposureLocal, exposureEndpoint(), nil, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Route.Exposure != docker.ExposureLocal || !decision.Assessment.Aligned {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Override != nil {
		t.Fatal("override attached without widening")
	}
	if len(decision.Recommendations) != 1 || decision.Recommendations[0].Source != "package" {
		t.Fatalf("recommendations = %+v", decision.Recommendations)
	}
	text, err := docker.RenderRoute(decision.Route)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	if strings.Contains(text, "handle") {
		t.Fatalf("local route restricted:\n%s", text)
	}
}

func TestDecideOverlayWithPolicy(t *testing.T) {
	policy := &docker.Policy{Name: "tailnet", Sources: []firewall.Source{
		{Zone: firewall.ZoneOverlay, CIDR: "100.64.0.0/10"},
	}}
	decision, err := Decide(exposureRoute(t), docker.ExposureOverlay, exposureEndpoint(), policy, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Route.Exposure != docker.ExposureOverlay {
		t.Fatalf("decision = %+v", decision)
	}
	text, err := docker.RenderRoute(decision.Route)
	if err != nil {
		t.Fatalf("RenderRoute: %v", err)
	}
	if !strings.Contains(text, "@allowed remote_ip 100.64.0.0/10") {
		t.Fatalf("policy not enforced:\n%s", text)
	}
}

func TestDecideCeilingAndOverride(t *testing.T) {
	closed := exposureEndpoint()
	closed.AllowWider = false
	// Public needs a public hostname; decisions validate shape, with
	// physics refusing before ceilings.
	public := exposureRoute(t)
	public.Host = "jellyfin.example.com"
	if _, err := Decide(public, docker.ExposurePublic, closed, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "record an override") {
		t.Fatalf("err = %v", err)
	}
	over := &exposure.Override{Host: "jellyfin.example.com", To: docker.ExposurePublic, Reason: "launch", Actor: "owner"}
	decision, err := Decide(public, docker.ExposurePublic, closed, nil, over)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Override == nil || decision.Route.Exposure != docker.ExposurePublic {
		t.Fatalf("decision = %+v", decision)
	}
	if !decision.Assessment.Aligned {
		t.Fatalf("assessment = %+v", decision.Assessment)
	}
	stale := &exposure.Override{Host: "jellyfin.example.com", To: docker.ExposureOverlay, Reason: "x", Actor: "owner"}
	if _, err := Decide(public, docker.ExposurePublic, closed, nil, stale); err == nil {
		t.Fatal("accepted mismatched override")
	}
}

func TestDecideRefusals(t *testing.T) {
	route := exposureRoute(t)
	if _, err := Decide(route, "dmz", exposureEndpoint(), nil, nil); err == nil {
		t.Fatal("decided bad exposure")
	}
	private := route
	private.Host = "app.lan"
	if _, err := Decide(private, docker.ExposurePublic, exposureEndpoint(), nil, nil); err == nil {
		// app.lan is private: refused. (jellyfin.lan would behave the same;
		// this guards the rule, not the name.)
		t.Fatal("widened private name")
	}
	if _, err := Decide(docker.Route{}, docker.ExposureLocal, exposureEndpoint(), nil, nil); err == nil {
		t.Fatal("decided invalid route")
	}
	badPolicy := &docker.Policy{Name: "Bad!"}
	if _, err := Decide(route, docker.ExposureLocal, exposureEndpoint(), badPolicy, nil); err == nil {
		t.Fatal("decided invalid policy")
	}
}

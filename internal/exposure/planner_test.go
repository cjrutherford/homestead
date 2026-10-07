package exposure

import (
	"net/netip"
	"strings"
	"testing"

	"homestead/internal/cert"
	"homestead/internal/docker"
)

func planRoute() docker.Route {
	return docker.Route{
		Host:     "app.example.com",
		Target:   docker.Target{Host: "app", Port: 8080},
		Exposure: docker.ExposureLocal,
		TLS:      docker.TLSInternal,
	}
}

func planRequest() Request {
	return Request{
		Route:    planRoute(),
		To:       docker.ExposurePublic,
		Zone:     "example.com",
		PublicIP: netip.MustParseAddr("203.0.113.7"),
	}
}

func TestPlanPublic(t *testing.T) {
	plan, err := PlanExposure(planRequest())
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.From != docker.ExposureLocal || plan.To != docker.ExposurePublic {
		t.Fatalf("plan = %+v", plan)
	}
	if !plan.Assessment.Aligned || plan.Assessment.Recommended != docker.ExposurePublic {
		t.Fatalf("assessment = %+v", plan.Assessment)
	}
	if len(plan.DNS) != 1 || plan.DNS[0].Name != "app" ||
		string(plan.DNS[0].Type) != "A" || plan.DNS[0].Value != "203.0.113.7" {
		t.Fatalf("dns = %+v", plan.DNS)
	}
	if plan.Zone != "example.com" {
		t.Fatalf("zone = %q", plan.Zone)
	}
	if plan.TLS.Action != TLSIssue || plan.TLS.Name != "app-example-com" {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	if len(plan.Firewall) != 2 || plan.Firewall[0].Service.Port != 80 ||
		plan.Firewall[1].Service.Port != 443 {
		t.Fatalf("firewall = %+v", plan.Firewall)
	}
	for _, policy := range plan.Firewall {
		if len(policy.Sources) != 1 || string(policy.Sources[0].Zone) != "public" {
			t.Fatalf("policy = %+v", policy)
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("policy: %v", err)
		}
	}
	if plan.Edge.Exposure != docker.ExposurePublic || plan.Edge.TLS != docker.TLSInternal {
		t.Fatalf("edge = %+v", plan.Edge)
	}
}

func TestPlanRefusals(t *testing.T) {
	private := planRequest()
	private.Route.Host = "app.lan"
	if _, err := PlanExposure(private); err == nil ||
		!strings.Contains(err.Error(), "cannot go public") {
		t.Fatalf("err = %v", err)
	}
	noIP := planRequest()
	noIP.PublicIP = netip.Addr{}
	if _, err := PlanExposure(noIP); err == nil ||
		!strings.Contains(err.Error(), "public address") {
		t.Fatalf("err = %v", err)
	}
	outside := planRequest()
	outside.Route.Host = "app.other.com"
	if _, err := PlanExposure(outside); err == nil ||
		!strings.Contains(err.Error(), "outside zone") {
		t.Fatalf("err = %v", err)
	}
	badTarget := planRequest()
	badTarget.To = "dmz"
	if _, err := PlanExposure(badTarget); err == nil {
		t.Fatal("planned bad target")
	}
	badRoute := planRequest()
	badRoute.Route.Host = ""
	if _, err := PlanExposure(badRoute); err == nil {
		t.Fatal("planned bad route")
	}
}

func TestPlanLocalAndNoop(t *testing.T) {
	local := planRequest()
	local.To = docker.ExposureOverlay
	plan, err := PlanExposure(local)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if len(plan.DNS) != 0 || plan.Zone != "" {
		t.Fatalf("dns = %+v %q", plan.DNS, plan.Zone)
	}
	if string(plan.Firewall[0].Sources[0].Zone) != "overlay" {
		t.Fatalf("firewall = %+v", plan.Firewall)
	}
	noop := planRequest()
	noop.Route.Exposure = docker.ExposurePublic
	plan, err = PlanExposure(noop)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if !plan.Assessment.Aligned {
		t.Fatalf("assessment = %+v", plan.Assessment)
	}
}

func TestPlanTLSMatrix(t *testing.T) {
	healthy := cert.Certificate{
		Name: "web", DNSNames: []string{"app.example.com"},
		Issuer: cert.Issuer{Name: "le", Type: cert.IssuerACME,
			Directory: "https://x/", Challenge: cert.ChallengeHTTP01},
		State: cert.StateIssued,
	}
	cover := func() Request {
		req := planRequest()
		req.Certificates = cert.Set{Certificates: []cert.Certificate{healthy}}
		return req
	}
	// No statuses: issue without proof.
	plan, err := PlanExposure(cover())
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.TLS.Action != TLSIssue || plan.TLS.Name != "web" {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	statuses := map[string]cert.Status{
		"web": {Name: "web", Health: cert.HealthHealthy},
	}
	covered := cover()
	covered.Statuses = statuses
	plan, err = PlanExposure(covered)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.TLS.Action != TLSNone {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	expiring := cover()
	expiring.Statuses = map[string]cert.Status{"web": {Name: "web", Health: cert.HealthExpiring}}
	plan, err = PlanExposure(expiring)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.TLS.Action != TLSRenew {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	broken := cover()
	broken.Statuses = map[string]cert.Status{"web": {Name: "web", Health: cert.HealthMismatch}}
	plan, err = PlanExposure(broken)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.TLS.Action != TLSIssue {
		t.Fatalf("tls = %+v", plan.TLS)
	}
	manual := cover()
	manual.Certificates.Certificates[0].Issuer = cert.Issuer{Name: "owner", Type: cert.IssuerManual}
	plan, err = PlanExposure(manual)
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	if plan.TLS.Action != TLSNone {
		t.Fatalf("tls = %+v", plan.TLS)
	}
}

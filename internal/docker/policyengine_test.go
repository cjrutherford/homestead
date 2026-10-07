package docker

import (
	"strings"
	"testing"

	"homestead/internal/firewall"
)

func assessRoute(host string, exposure Exposure, policy *Policy) Route {
	return Route{
		Host:     Hostname(host),
		Target:   Target{Host: "app", Port: 8080},
		Exposure: exposure,
		TLS:      TLSInternal,
		Policy:   policy,
	}
}

func TestAssess(t *testing.T) {
	// Private names resolve local with a warning when wider.
	assessment, err := Assess(assessRoute("app.lan", ExposurePublic, nil))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if assessment.Recommended != ExposureLocal || assessment.Aligned {
		t.Fatalf("assessment = %+v", assessment)
	}
	if len(assessment.Findings) != 1 || assessment.Findings[0].Severity != SeverityWarn {
		t.Fatalf("findings = %+v", assessment.Findings)
	}
	// Coherent private stays quiet.
	quiet, err := Assess(assessRoute("app.lan", ExposureLocal, nil))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !quiet.Aligned || len(quiet.Findings) != 0 {
		t.Fatalf("assessment = %+v", quiet)
	}
	// Public on public agrees silently.
	agreed, err := Assess(assessRoute("app.example.com", ExposurePublic, nil))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !agreed.Aligned || agreed.Recommended != ExposurePublic || len(agreed.Findings) != 0 {
		t.Fatalf("assessment = %+v", agreed)
	}
	// Public names held narrow note the ceremony path.
	narrow, err := Assess(assessRoute("app.example.com", ExposureLocal, nil))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !narrow.Aligned || len(narrow.Findings) != 1 ||
		narrow.Findings[0].Severity != SeverityInfo ||
		!strings.Contains(narrow.Findings[0].Message, "exposure ceremony") {
		t.Fatalf("assessment = %+v", narrow)
	}
	// Proxy narrowing on public routes is informational.
	policy := &Policy{Name: "office", Sources: []firewall.Source{
		{Zone: firewall.ZoneLocal, CIDR: "192.168.1.0/24"},
	}}
	narrowed, err := Assess(assessRoute("app.example.com", ExposurePublic, policy))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !narrowed.Aligned || len(narrowed.Findings) != 1 ||
		!strings.Contains(narrowed.Findings[0].Message, "narrows public exposure") {
		t.Fatalf("assessment = %+v", narrowed)
	}
	// Invalid routes fail, never grade.
	if _, err := Assess(Route{}); err == nil {
		t.Fatal("graded invalid route")
	}
}

func TestAssessAll(t *testing.T) {
	routes := []Route{
		assessRoute("app.lan", ExposureLocal, nil),
		assessRoute("app.example.com", ExposurePublic, nil),
	}
	assessments, err := AssessAll(routes)
	if err != nil {
		t.Fatalf("AssessAll: %v", err)
	}
	if len(assessments) != 2 || !assessments[0].Aligned || !assessments[1].Aligned {
		t.Fatalf("assessments = %+v", assessments)
	}
	bad := append(routes, Route{})
	if _, err := AssessAll(bad); err == nil ||
		!strings.Contains(err.Error(), "route #2") {
		t.Fatalf("err = %v", err)
	}
}

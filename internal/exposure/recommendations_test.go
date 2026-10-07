package exposure

import (
	"strings"
	"testing"

	"homestead/internal/cert"
	"homestead/internal/docker"
)

func recommendRoute() docker.Route {
	return docker.Route{
		Host:     "app.example.com",
		Target:   docker.Target{Host: "app", Port: 8080},
		Exposure: docker.ExposurePublic,
		TLS:      docker.TLSAuto,
	}
}

func TestRecommendQuiet(t *testing.T) {
	got, err := Recommend(recommendRoute(), nil, nil)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("recommendations = %+v", got)
	}
	if text := Format("app.example.com", got); !strings.Contains(text, "stands as declared") {
		t.Fatalf("format = %q", text)
	}
}

func TestRecommendMerged(t *testing.T) {
	route := recommendRoute()
	route.Exposure = docker.ExposureLocal
	route.TLS = docker.TLSOff
	endpoint := &EndpointExposure{
		Endpoint: "admin", Recommended: docker.ExposureLocal,
		Warnings: []string{"Admin surface carries full control."},
	}
	status := &cert.Status{Name: "web", Health: cert.HealthExpiring}
	got, err := Recommend(route, endpoint, status)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	// Engine ceremony info, package warning, expiring cert warn.
	if len(got) != 3 {
		t.Fatalf("recommendations = %+v", got)
	}
	if got[0].Severity != docker.SeverityWarn || got[0].Source != SourceCertificate {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Severity != docker.SeverityInfo || got[1].Source != SourceEngine {
		t.Fatalf("second = %+v", got[1])
	}
	if got[2].Source != SourcePackage {
		t.Fatalf("third = %+v", got[2])
	}
	text := Format(route.Host, got)
	if !strings.Contains(text, "app.example.com recommendations:") ||
		!strings.Contains(text, "[warn] (certificate)") {
		t.Fatalf("format = %q", text)
	}
}

func TestRecommendCeilingBreachAdvises(t *testing.T) {
	route := recommendRoute()
	endpoint := &EndpointExposure{Endpoint: "admin", Recommended: docker.ExposureLocal}
	got, err := Recommend(route, endpoint, nil)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	// Breach counsels — never refuses.
	if len(got) != 1 || got[0].Severity != docker.SeverityWarn ||
		!strings.Contains(got[0].Message, "audited override") {
		t.Fatalf("recommendations = %+v", got)
	}
}

func TestRecommendCertificate(t *testing.T) {
	route := recommendRoute()
	route.TLS = docker.TLSOff
	got, err := Recommend(route, nil, nil)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(got) != 1 || got[0].Source != SourceCertificate ||
		!strings.Contains(got[0].Message, "plaintext") {
		t.Fatalf("recommendations = %+v", got)
	}
	statuses := []cert.Status{
		{Name: "a", Health: cert.HealthExpired},
		{Name: "b", Health: cert.HealthMissing},
		{Name: "c", Health: cert.HealthHealthy},
	}
	var warns int
	for _, status := range statuses {
		status := status
		got, err := Recommend(recommendRoute(), nil, &status)
		if err != nil {
			t.Fatalf("Recommend: %v", err)
		}
		warns += len(got)
	}
	if warns != 2 {
		t.Fatalf("warns = %d", warns)
	}
	if _, err := Recommend(docker.Route{}, nil, nil); err == nil {
		t.Fatal("advised invalid route")
	}
}

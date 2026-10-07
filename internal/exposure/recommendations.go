package exposure

import (
	"fmt"
	"sort"
	"strings"

	"homestead/internal/cert"
	"homestead/internal/docker"
)

// Recommendation sources name who counsels: the engine grades
// posture, packages warn from authorship, certificates report serving
// readiness. Owners see all three before choosing — and choosing
// against advice stays possible (S125 records it), never pretended
// away.
const (
	SourceEngine      = "engine"
	SourcePackage     = "package"
	SourceCertificate = "certificate"
)

// Recommendation is one displayed counsel: graded, sourced, and
// worded for owners rather than operators.
type Recommendation struct {
	Severity docker.Severity
	Source   string
	Message  string
}

// Recommend counsels a route's declared posture without refusing
// anything: engine findings map over, package warnings and ceiling
// breaches display, and certificate readiness reports. Warns sort
// before information so the urgent reads first; within a grade,
// engine, package, then certificate. Invalid routes fail — advice
// needs a valid subject.
func Recommend(route docker.Route, endpoint *EndpointExposure, status *cert.Status) ([]Recommendation, error) {
	if err := route.Validate(); err != nil {
		return nil, err
	}
	assessment, err := docker.Assess(route)
	if err != nil {
		return nil, err
	}
	var out []Recommendation
	for _, finding := range assessment.Findings {
		out = append(out, Recommendation{Severity: finding.Severity, Source: SourceEngine, Message: finding.Message})
	}
	if endpoint != nil {
		for _, warning := range endpoint.Warnings {
			out = append(out, Recommendation{Severity: docker.SeverityInfo, Source: SourcePackage, Message: warning})
		}
		if !endpoint.Allows(route.Exposure) {
			out = append(out, Recommendation{
				Severity: docker.SeverityWarn,
				Source:   SourcePackage,
				Message:  fmt.Sprintf("exceeds the package ceiling of %s; widening needs an audited override", endpoint.Recommended),
			})
		}
	}
	if route.Exposure == docker.ExposurePublic && route.TLS == docker.TLSOff {
		out = append(out, Recommendation{
			Severity: docker.SeverityWarn,
			Source:   SourceCertificate,
			Message:  "serves public plaintext; terminate TLS before widening",
		})
	}
	if status != nil {
		switch status.Health {
		case cert.HealthExpired, cert.HealthExpiring:
			out = append(out, Recommendation{
				Severity: docker.SeverityWarn,
				Source:   SourceCertificate,
				Message:  fmt.Sprintf("certificate %q is %s", status.Name, status.Health),
			})
		case cert.HealthMissing, cert.HealthMismatch, cert.HealthInvalid:
			out = append(out, Recommendation{
				Severity: docker.SeverityWarn,
				Source:   SourceCertificate,
				Message:  fmt.Sprintf("no healthy material for certificate %q", status.Name),
			})
		}
	}
	precedence := map[string]int{SourceEngine: 0, SourcePackage: 1, SourceCertificate: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == docker.SeverityWarn
		}
		return precedence[out[i].Source] < precedence[out[j].Source]
	})
	return out, nil
}

// Format renders recommendations for display: one graded line each,
// with a confirming header when nothing counsels against the posture.
// Presentation layers (CLI, dashboard) quote this verbatim rather than
// rewording, so wording changes stay reviewable here.
func Format(host docker.Hostname, recommendations []Recommendation) string {
	var body strings.Builder
	if len(recommendations) == 0 {
		return fmt.Sprintf("%s: no recommendations; posture stands as declared.\n", host)
	}
	fmt.Fprintf(&body, "%s recommendations:\n", host)
	for _, recommendation := range recommendations {
		fmt.Fprintf(&body, "[%s] (%s) %s\n", recommendation.Severity, recommendation.Source, recommendation.Message)
	}
	return body.String()
}

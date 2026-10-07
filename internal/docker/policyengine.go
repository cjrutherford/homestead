package docker

import (
	"fmt"
)

// Severity grades an assessment finding: information the owner should
// know versus warnings that deserve action before exposure widens.
// Blocks live with planners (S121), never here — the engine evaluates,
// others enforce.
type Severity string

const (
	// SeverityInfo records notable-but-coherent posture.
	SeverityInfo Severity = "info"
	// SeverityWarn flags exposure that cannot work as declared.
	SeverityWarn Severity = "warn"
)

// Finding is one engine observation with its grade.
type Finding struct {
	Severity Severity
	Message  string
}

// Assessment compares a route's selected exposure against the engine's
// recommendation, independently of any app install: no network calls,
// no mutations, no side effects. Least privilege governs ties — the
// narrowest coherent exposure wins — and private names always resolve
// local, since no public origin can ever reach them.
type Assessment struct {
	Host        Hostname
	Selected    Exposure
	Recommended Exposure
	Aligned     bool
	Findings    []Finding
}

// Assess evaluates one route. Invalid routes fail instead of grading;
// grading the ungradeable would launder bad input into advice.
func Assess(route Route) (Assessment, error) {
	if err := route.Validate(); err != nil {
		return Assessment{}, err
	}
	assessment := Assessment{Host: route.Host, Selected: route.Exposure}
	if IsPrivateName(route.Host) {
		assessment.Recommended = ExposureLocal
		if route.Exposure != ExposureLocal {
			assessment.Findings = append(assessment.Findings, Finding{
				Severity: SeverityWarn,
				Message:  fmt.Sprintf("%q is private and unreachable publicly; local exposure recommended", route.Host),
			})
		}
	} else {
		assessment.Recommended = route.Exposure
		if route.Exposure != ExposurePublic {
			assessment.Findings = append(assessment.Findings, Finding{
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("%q is public but serves %s only; widen through the exposure ceremony", route.Host, route.Exposure),
			})
		}
	}
	assessment.Aligned = assessment.Selected == assessment.Recommended
	if route.Policy != nil && len(route.Policy.networks()) > 0 && route.Exposure == ExposurePublic {
		assessment.Findings = append(assessment.Findings, Finding{
			Severity: SeverityInfo,
			Message:  fmt.Sprintf("policy %q narrows public exposure at the proxy", route.Policy.Name),
		})
	}
	return assessment, nil
}

// AssessAll grades a route table, failing fast on the first invalid
// route for the same laundering reason as Assess.
func AssessAll(routes []Route) ([]Assessment, error) {
	assessments := make([]Assessment, 0, len(routes))
	for i := range routes {
		assessment, err := Assess(routes[i])
		if err != nil {
			return nil, fmt.Errorf("route #%d: %w", i, err)
		}
		assessments = append(assessments, assessment)
	}
	return assessments, nil
}

package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Finding statuses mirror the doctor vocabulary (S044): ok serves the
// intended value, fail serves something else or nothing, warn marks
// infrastructure trouble (timeouts, unexpected errors) rather than a
// verdict on the data.
const (
	FindingOK   = "ok"
	FindingWarn = "warn"
	FindingFail = "fail"
)

// Finding explains one desired record's observed state: what was asked,
// what answered, and what it means.
type Finding struct {
	Record  Record
	FQDN    string
	Status  string
	Detail  string
	Answers []string
}

// Report is the diagnosis for a zone table: one finding per desired
// record, rolled up to a single status.
type Report struct {
	Zone     string
	Audience Audience
	Findings []Finding
}

// Overall rolls findings up, fail over warn over ok — the doctor's
// roll-up in DNS words.
func (r Report) Overall() string {
	result := FindingOK
	for _, finding := range r.Findings {
		switch finding.Status {
		case FindingFail:
			return FindingFail
		case FindingWarn:
			result = FindingWarn
		}
	}
	return result
}

// classifyError sorts probe failures: authoritative absence and empty
// answers are data (fail), timeouts and surprises are infrastructure
// (warn with cause).
func classifyError(fqdn string, typ RecordType, err error) (string, string) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return FindingFail, fmt.Sprintf("%s does not exist (NXDOMAIN): the record was never created or the name is wrong", fqdn)
		}
		if dnsErr.IsTimeout {
			return FindingWarn, fmt.Sprintf("%s query timed out: the resolver is unreachable or DNS traffic is blocked", fqdn)
		}
	}
	return FindingWarn, fmt.Sprintf("%s %s lookup failed: %v", fqdn, typ, err)
}

// Diagnose explains every desired record through probe: present values
// pass, absent names and wrong values fail with what answered, and
// probe trouble warns with cause. TTLs stay out — caching makes them
// unobservable, and claiming otherwise would mislead.
func Diagnose(ctx context.Context, probe ProbeFunc, zoneName string, desired []Record) (Report, error) {
	if err := validateDNSName("zone.name", zoneName, false); err != nil {
		return Report{}, err
	}
	if probe == nil {
		return Report{}, fmt.Errorf("dns: probe must not be nil")
	}
	report := Report{Zone: zoneName}
	for i := range desired {
		if err := desired[i].Validate(); err != nil {
			return Report{}, fmt.Errorf("record #%d: %w", i, err)
		}
		name := fqdn(zoneName, desired[i].Name)
		answers, err := probe(ctx, name, desired[i].Type)
		if err != nil {
			status, detail := classifyError(name, desired[i].Type, err)
			report.Findings = append(report.Findings, Finding{
				Record: desired[i], FQDN: name, Status: status, Detail: detail,
			})
			continue
		}
		matched := false
		for _, answer := range answers {
			if sameValue(desired[i].Type, desired[i].Value, answer) {
				matched = true
				break
			}
		}
		switch {
		case matched:
			report.Findings = append(report.Findings, Finding{
				Record: desired[i], FQDN: name, Status: FindingOK, Answers: answers,
				Detail: fmt.Sprintf("%s %s resolves to %s", name, desired[i].Type, desired[i].Value),
			})
		case len(answers) == 0:
			report.Findings = append(report.Findings, Finding{
				Record: desired[i], FQDN: name, Status: FindingFail,
				Detail: fmt.Sprintf("%s exists but carries no %s records: add the record or fix the type", name, desired[i].Type),
			})
		default:
			report.Findings = append(report.Findings, Finding{
				Record: desired[i], FQDN: name, Status: FindingFail, Answers: answers,
				Detail: fmt.Sprintf("%s answers %q without %q: stale cache, wrong provider value, or the wrong view",
					name, strings.Join(answers, " "), desired[i].Value),
			})
		}
	}
	return report, nil
}

// VerifyView diagnoses an audience's effective table (S098): the same
// per-record explanations over the merged baseline-plus-view set, so
// owners see each view's intended state verified, not just the baseline.
func VerifyView(ctx context.Context, probe ProbeFunc, zone Zone, audience Audience) (Report, error) {
	effective, err := EffectiveRecords(zone, audience)
	if err != nil {
		return Report{}, err
	}
	report, err := Diagnose(ctx, probe, zone.Name, effective)
	if err != nil {
		return Report{}, err
	}
	report.Audience = audience
	return report, nil
}

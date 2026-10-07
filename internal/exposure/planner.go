package exposure

import (
	"fmt"
	"net/netip"
	"strings"

	"homestead/internal/cert"
	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/firewall"
)

// TLSAction selects the certificate work a plan needs: nothing when a
// healthy certificate covers the host, issuance for new or broken
// coverage, renewal inside the window.
type TLSAction string

const (
	// TLSNone needs no certificate work.
	TLSNone TLSAction = "none"
	// TLSIssue fetches a first certificate under the suggested name.
	TLSIssue TLSAction = "issue"
	// TLSRenew refreshes an expiring certificate in place.
	TLSRenew TLSAction = "renew"
)

// CertificatePlan names the TLS work: action plus the certificate to
// fetch or refresh, with the reasoning attached.
type CertificatePlan struct {
	Action TLSAction
	Name   string
	Detail string
}

// Plan is the exposure change set for one route across four domains:
// the engine assessment of the target, required DNS records, TLS work,
// desired firewall policies, and the edge state to reconcile. Sections
// are desired deltas for this route only — callers merge tables before
// applying, and DNS reconciliation diffs against live provider state
// at apply time (S098) rather than here. Firewall policies stay
// unexpanded: expansion needs live network facts (S053), and public or
// overlay zone-wide sources need explicit CIDRs the planner never
// invents.
type Plan struct {
	Host       docker.Hostname
	From       docker.Exposure
	To         docker.Exposure
	Assessment docker.Assessment
	Zone       string
	DNS        []dns.Record
	TLS        CertificatePlan
	Firewall   []firewall.ServicePolicy
	Edge       docker.Route
}

// Request scopes one planning run: the current route, the target
// exposure, the DNS origin for record names, the public address for
// public records, the certificate table with monitoring statuses, and
// nothing else — the planner is offline by design.
type Request struct {
	Route        docker.Route
	To           docker.Exposure
	Zone         string
	PublicIP     netip.Addr
	Certificates cert.Set
	Statuses     map[string]cert.Status
}

// PlanExposure produces the change set moving a route to the target
// exposure. Widening private names to public is refused outright — no
// public origin can reach them, so planning would manufacture a broken
// promise (the block S120 assigns here). An already-placed route plans
// empty sections with a confirming assessment.
func PlanExposure(req Request) (Plan, error) {
	if err := req.Route.Validate(); err != nil {
		return Plan{}, err
	}
	if req.To != docker.ExposureLocal && req.To != docker.ExposureOverlay && req.To != docker.ExposurePublic {
		return Plan{}, fmt.Errorf("exposure: target must be local, overlay, or public")
	}
	if req.To == docker.ExposurePublic && docker.IsPrivateName(req.Route.Host) {
		return Plan{}, fmt.Errorf("exposure: %q is private and cannot go public", req.Route.Host)
	}
	target := req.Route
	target.Exposure = req.To
	assessment, err := docker.Assess(target)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Host: req.Route.Host, From: req.Route.Exposure, To: req.To,
		Assessment: assessment, Edge: target,
	}
	if err := planDNS(&plan, req); err != nil {
		return Plan{}, err
	}
	if err := planTLS(&plan, req); err != nil {
		return Plan{}, err
	}
	planFirewall(&plan)
	return plan, nil
}

// planDNS records the required public record when widening outward: an
// address record for the host under the zone origin. Non-public targets
// need no DNS work. The value rides the request because only live
// discovery knows the public address — the planner never guesses it.
func planDNS(plan *Plan, req Request) error {
	if req.To != docker.ExposurePublic {
		return nil
	}
	if !req.PublicIP.IsValid() || req.PublicIP.IsUnspecified() {
		return fmt.Errorf("exposure: going public needs the public address")
	}
	host := string(req.Route.Host)
	var relative string
	switch {
	case host == req.Zone:
		relative = "@"
	case strings.HasSuffix(host, "."+req.Zone):
		relative = strings.TrimSuffix(host, "."+req.Zone)
	default:
		return fmt.Errorf("exposure: %q is outside zone %q", host, req.Zone)
	}
	typ := dns.RecordA
	if req.PublicIP.Is6() {
		typ = dns.RecordAAAA
	}
	record := dns.Record{Name: relative, Type: typ, Value: req.PublicIP.String()}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("exposure: dns: %w", err)
	}
	plan.Zone = req.Zone
	plan.DNS = []dns.Record{record}
	return nil
}

// planTLS derives certificate work from coverage plus monitoring: a
// healthy covering certificate means none, expiring means renew,
// anything else means issue under the covering name or a deterministic
// suggestion. Manual issuers never renew — owners resupply — so their
// action stays none with the reasoning attached.
func planTLS(plan *Plan, req Request) error {
	host := string(req.Route.Host)
	if err := req.Certificates.Validate(); err != nil {
		return err
	}
	covering, ok := req.Certificates.Find(host)
	if !ok {
		plan.TLS = CertificatePlan{
			Action: TLSIssue,
			Name:   strings.ReplaceAll(host, ".", "-"),
			Detail: fmt.Sprintf("no certificate covers %q", host),
		}
		return nil
	}
	if covering.Issuer.Type == cert.IssuerManual {
		plan.TLS = CertificatePlan{Action: TLSNone, Name: covering.Name, Detail: "owner supplies manual certificates"}
		return nil
	}
	status, known := req.Statuses[covering.Name]
	if !known || status.Health == cert.HealthMissing || status.Health == cert.HealthMismatch || status.Health == cert.HealthInvalid {
		plan.TLS = CertificatePlan{Action: TLSIssue, Name: covering.Name, Detail: fmt.Sprintf("no healthy material for %q", covering.Name)}
		return nil
	}
	if status.Health == cert.HealthExpiring || status.Health == cert.HealthExpired {
		plan.TLS = CertificatePlan{Action: TLSRenew, Name: covering.Name, Detail: fmt.Sprintf("certificate %q is %s", covering.Name, status.Health)}
		return nil
	}
	plan.TLS = CertificatePlan{Action: TLSNone, Name: covering.Name, Detail: fmt.Sprintf("certificate %q is healthy", covering.Name)}
	return nil
}

// planFirewall desires web serving from the target origin class: HTTP
// and HTTPS accepts scoped to the exposure zone. Policies stay
// unexpanded (see Plan); callers expand with live facts at apply time.
func planFirewall(plan *Plan) {
	zone := firewall.Zone(plan.To)
	for _, service := range []firewall.Service{
		{Name: "web-http", Protocol: firewall.ProtocolTCP, Port: 80},
		{Name: "web-https", Protocol: firewall.ProtocolTCP, Port: 443},
	} {
		plan.Firewall = append(plan.Firewall, firewall.ServicePolicy{
			Service: service,
			Sources: []firewall.Source{{Zone: zone}},
		})
	}
}

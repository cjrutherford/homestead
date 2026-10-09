// Package baseline gates external exposure (S222): one readiness
// report proving claim, administrator, firewall, and local
// management TLS are complete before the bootstrap chain (S223+)
// proceeds outward. Checks read live state and pure crypto only —
// no mutations, no network calls — so gating never changes what it
// inspects. Surfaces (if any) belong to the slices that enforce the
// gate; this package only judges.
package baseline

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"homestead/internal/auth"
	"homestead/internal/cert"
	"homestead/internal/claim"
	"homestead/internal/firewall"
)

// Statuses naming check outcomes.
const (
	// StatusPass marks a completed baseline leg.
	StatusPass = "pass"
	// StatusFail marks an incomplete leg with the remedy in Detail.
	StatusFail = "fail"
)

// ManagementHostname is the local name management TLS must serve:
// the first-run discovery name (S221), so the same name owners
// found the box by is the name secured first.
const ManagementHostname = "homestead.local"

// Check is one baseline leg with its verdict.
type Check struct {
	Name   string
	Status string
	Detail string
}

// Report is the readiness verdict: Ready only when every check
// passes. Consumers gate on Ready and quote Details for remedies.
type Report struct {
	Ready  bool
	Checks []Check
}

// Inputs wires the state each leg reads: claim and administrator
// services live, firewall detection injected (production passes
// firewall.Detect; tests stub states), and now anchors the TLS
// validity window for deterministic tests.
type Inputs struct {
	Claim    *claim.Service
	Auth     *auth.Service
	Firewall func() firewall.State
	Now      time.Time
}

// Assess evaluates the four legs in fixed order — claim, admin,
// firewall, management TLS — failing fast is refused: every leg
// reports, so one visit names everything missing. Nil services fail
// their leg instead of panicking: an unwired dependency reads as
// incomplete, never as ready.
func Assess(in Inputs) Report {
	checks := []Check{
		checkClaim(in.Claim),
		checkAdmin(in.Auth),
		checkFirewall(in.Firewall),
		checkManagementTLS(in.Now),
	}
	ready := true
	for _, check := range checks {
		if check.Status != StatusPass {
			ready = false
			break
		}
	}
	return Report{Ready: ready, Checks: checks}
}

// checkClaim demands a redeemed appliance: nothing downstream may
// proceed on an unowned box.
func checkClaim(svc *claim.Service) Check {
	if svc == nil {
		return Check{Name: "claim", Status: StatusFail, Detail: "claim service unwired"}
	}
	state, err := svc.CurrentState()
	if err != nil {
		return Check{Name: "claim", Status: StatusFail, Detail: fmt.Sprintf("claim state unreadable: %v", err)}
	}
	if state != claim.StateClaimed {
		return Check{Name: "claim", Status: StatusFail, Detail: "appliance unclaimed: redeem the console token first"}
	}
	return Check{Name: "claim", Status: StatusPass, Detail: "appliance claimed"}
}

// checkAdmin demands a first administrator: exposure without an
// owner to answer for it is lockout by another name.
func checkAdmin(svc *auth.Service) Check {
	if svc == nil {
		return Check{Name: "admin", Status: StatusFail, Detail: "auth service unwired"}
	}
	has, err := svc.HasAdmin()
	if err != nil {
		return Check{Name: "admin", Status: StatusFail, Detail: fmt.Sprintf("administrator lookup failed: %v", err)}
	}
	if !has {
		return Check{Name: "admin", Status: StatusFail, Detail: "no administrator: run admin setup first"}
	}
	return Check{Name: "admin", Status: StatusPass, Detail: "administrator exists"}
}

// checkFirewall demands applied Homestead rules: nftables present,
// the ruleset readable, and our table in it. An unreadable ruleset
// (unprivileged daemon) fails as unverifiable rather than let an
// absent table read as merely unapplied.
func checkFirewall(detect func() firewall.State) Check {
	if detect == nil {
		return Check{Name: "firewall", Status: StatusFail, Detail: "firewall detection unwired"}
	}
	state := detect()
	if !state.NFTAvailable {
		return Check{Name: "firewall", Status: StatusFail, Detail: "nftables unavailable: firewall cannot filter"}
	}
	if !state.RulesetAccessible {
		return Check{Name: "firewall", Status: StatusFail, Detail: "ruleset unreadable: cannot verify filtering (unprivileged?)"}
	}
	if !state.HasHomesteadTable {
		return Check{Name: "firewall", Status: StatusFail, Detail: "Homestead rules not applied: run firewall apply first"}
	}
	return Check{Name: "firewall", Status: StatusPass, Detail: "Homestead rules applied"}
}

// checkManagementTLS proves the local-TLS leg end to end in pure
// crypto: a fresh internal authority issues a serving certificate
// for the management hostname, and the chain verifies at now with
// margin to spare. No persisted authority or served endpoint exists
// yet — this leg proves the mechanics work, and provisioning the
// material belongs to later slices.
func checkManagementTLS(now time.Time) Check {
	if now.IsZero() {
		now = time.Now()
	}
	authority, err := cert.GenerateCA("homestead-local", 5*365*24*time.Hour, now)
	if err != nil {
		return Check{Name: "management-tls", Status: StatusFail, Detail: fmt.Sprintf("internal CA failed: %v", err)}
	}
	chainPEM, _, err := authority.IssueServerCert([]string{ManagementHostname}, 90*24*time.Hour, now)
	if err != nil {
		return Check{Name: "management-tls", Status: StatusFail, Detail: fmt.Sprintf("serving certificate failed: %v", err)}
	}
	block, _ := pem.Decode(chainPEM)
	if block == nil {
		return Check{Name: "management-tls", Status: StatusFail, Detail: "serving certificate is not PEM"}
	}
	serving, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Check{Name: "management-tls", Status: StatusFail, Detail: fmt.Sprintf("serving certificate: %v", err)}
	}
	pool := x509.NewCertPool()
	pool.AddCert(authority.Cert)
	if _, err := serving.Verify(x509.VerifyOptions{
		DNSName:     ManagementHostname,
		Roots:       pool,
		CurrentTime: now.Add(30 * 24 * time.Hour),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return Check{Name: "management-tls", Status: StatusFail, Detail: fmt.Sprintf("serving certificate invalid: %v", err)}
	}
	return Check{Name: "management-tls", Status: StatusPass, Detail: "internal CA issues valid serving certificate for " + ManagementHostname}
}

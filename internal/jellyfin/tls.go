package jellyfin

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"homestead/internal/cert"
	"homestead/internal/docker"
)

// Certificate is serving material: the chain plus its key, minted or
// supplied, verified before anything trusts it.
type Certificate struct {
	ChainPEM []byte
	KeyPEM   []byte
}

// CheckReadiness gates the route's TLS strategy without fetching
// anything: internal needs a configured authority, automatic needs
// valid direct challenges (S101), and plaintext acknowledges itself.
// Callers install on ready, fix or choose otherwise on refusal —
// readiness advises placement, never issues.
func CheckReadiness(route docker.Route, caConfigured bool) (bool, string) {
	if err := route.Validate(); err != nil {
		return false, fmt.Sprintf("invalid route: %v", err)
	}
	switch route.TLS {
	case docker.TLSInternal:
		if !caConfigured {
			return false, "internal TLS needs a configured authority"
		}
		return true, "internal authority configured"
	case docker.TLSAuto:
		if !docker.DirectChallengesValid(route.Host, route.Exposure) {
			return false, fmt.Sprintf("automatic TLS cannot serve %q here; use internal or DNS-01", route.Host)
		}
		return true, "direct challenges valid"
	default:
		return true, "serves plaintext by choice"
	}
}

// DefaultServerLifetime bounds minted serving certificates to about a
// year: short enough to rotate, long enough to stay quiet.
const DefaultServerLifetime = 397 * 24 * time.Hour

// IssueInternal mints the serving certificate for a media hostname
// from the configured authority: single-SAN leaf under a fresh key,
// validated before return so callers never store broken material.
func IssueInternal(ca *cert.Authority, host docker.Hostname, now time.Time) (Certificate, error) {
	if ca == nil {
		return Certificate{}, fmt.Errorf("jellyfin: no internal authority")
	}
	if err := validateCertName(host); err != nil {
		return Certificate{}, fmt.Errorf("jellyfin: %w", err)
	}
	chain, key, err := ca.IssueServerCert([]string{string(host)}, DefaultServerLifetime, now)
	if err != nil {
		return Certificate{}, fmt.Errorf("jellyfin: issue: %w", err)
	}
	issued := Certificate{ChainPEM: chain, KeyPEM: key}
	if err := VerifyCertificate(issued.ChainPEM, host, now); err != nil {
		return Certificate{}, err
	}
	return issued, nil
}

// validateCertName checks certificate name spelling: route hostnames
// plus one leading wildcard label, which routes never carry but
// certificates legitimately cover.
func validateCertName(host docker.Hostname) error {
	name := string(host)
	if strings.HasPrefix(name, "*.") {
		name = name[2:]
		if name == "" {
			return fmt.Errorf("route.host: wildcard needs a base name")
		}
	}
	return docker.Hostname(name).Validate()
}

// VerifyCertificate proves serving material covers a hostname inside
// its validity window: leaf parses, SAN matches exactly or by leading
// wildcard (one label only, per CA/B rules), and now sits inside
// validity. Trust roots stay out — callers verify chains against
// pools where trust matters (S105 monitors whole chains).
func VerifyCertificate(chainPEM []byte, host docker.Hostname, now time.Time) error {
	block, _ := pem.Decode(chainPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("jellyfin: material is not a PEM chain")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("jellyfin: parse leaf: %w", err)
	}
	matched := false
	for _, name := range leaf.DNSNames {
		if WildcardMatch(name, string(host)) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("jellyfin: certificate covers %q, not %q", strings.Join(leaf.DNSNames, " "), host)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return fmt.Errorf("jellyfin: certificate not valid at issuance check")
	}
	return nil
}

// WildcardMatch reports wildcard-aware DNS equality: exact match,
// or a leading "*." covering exactly one label.
func WildcardMatch(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if !strings.HasPrefix(pattern, "*.") || strings.Contains(host, "..") {
		return false
	}
	suffix := pattern[1:]
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	return !strings.Contains(strings.TrimSuffix(host, suffix), ".")
}

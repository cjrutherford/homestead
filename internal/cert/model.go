package cert

import (
	"fmt"
	"strings"
	"time"
)

// IssuerType selects how certificates arrive (ADR-010): the internal CA
// for private names, ACME automation for public ones, and manual supply
// where the owner brings their own. Mechanics per type attach in later
// slices, never as new types here without a superseding ADR.
type IssuerType string

const (
	// IssuerInternal signs from the Homestead internal CA (S104).
	IssuerInternal IssuerType = "internal"
	// IssuerACME automates issuance through an ACME directory (S101-S103).
	IssuerACME IssuerType = "acme"
	// IssuerManual uses owner-supplied certificates Homestead never fetches.
	IssuerManual IssuerType = "manual"
)

// Valid reports a known issuer type.
func (t IssuerType) Valid() bool {
	return t == IssuerInternal || t == IssuerACME || t == IssuerManual
}

// ChallengeType selects the ACME challenge for public issuance.
type ChallengeType string

const (
	// ChallengeHTTP01 proves control over HTTP (port 80 reachable).
	ChallengeHTTP01 ChallengeType = "http-01"
	// ChallengeTLSALPN01 proves control over TLS (port 443 reachable).
	ChallengeTLSALPN01 ChallengeType = "tls-alpn-01"
	// ChallengeDNS01 proves control through DNS TXT values, the only
	// challenge that issues wildcards.
	ChallengeDNS01 ChallengeType = "dns-01"
)

// Valid reports a known challenge type.
func (c ChallengeType) Valid() bool {
	return c == ChallengeHTTP01 || c == ChallengeTLSALPN01 || c == ChallengeDNS01
}

// Issuer is one certificate source: a stable name plus type-specific
// parameters. Each type carries only its own fields — ACME directory
// and challenge on acme, a DNS provider name on dns-01, nothing extra
// on internal or manual — so intent never implies machinery it lacks.
type Issuer struct {
	Name        string
	Type        IssuerType
	Directory   string
	Challenge   ChallengeType
	DNSProvider string
}

// Validate checks the issuer end to end.
func (i Issuer) Validate() error {
	if len(i.Name) == 0 || len(i.Name) > 64 {
		return fmt.Errorf("cert: issuer name must be 1-64 characters")
	}
	for _, r := range i.Name {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("cert: invalid issuer name %q", i.Name)
		}
	}
	if !i.Type.Valid() {
		return fmt.Errorf("cert: issuer type must be internal, acme, or manual")
	}
	if i.Type == IssuerACME {
		if !strings.HasPrefix(i.Directory, "https://") {
			return fmt.Errorf("cert: acme directory must be an https URL")
		}
		if !i.Challenge.Valid() {
			return fmt.Errorf("cert: acme challenge must be http-01, tls-alpn-01, or dns-01")
		}
		if i.Challenge == ChallengeDNS01 && i.DNSProvider == "" {
			return fmt.Errorf("cert: dns-01 needs a DNS provider")
		}
		if i.Challenge != ChallengeDNS01 && i.DNSProvider != "" {
			return fmt.Errorf("cert: DNS provider only applies to dns-01")
		}
		return nil
	}
	if i.Directory != "" || i.Challenge != "" || i.DNSProvider != "" {
		return fmt.Errorf("cert: %s issuer takes no ACME parameters", i.Type)
	}
	return nil
}

// validateDNSName checks canonical shape: lowercase DNS, single-label
// LAN names included, one leading wildcard label allowed (certificates
// cover *.example.com where routes never do).
func validateDNSName(field, name string) error {
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("%s: must be 1-253 characters", field)
	}
	if strings.ToLower(name) != name {
		return fmt.Errorf("%s: must be lowercase", field)
	}
	rest := name
	if strings.HasPrefix(rest, "*.") {
		rest = rest[2:]
		if rest == "" {
			return fmt.Errorf("%s: wildcard needs a base name", field)
		}
	}
	if strings.HasPrefix(rest, ".") || strings.HasSuffix(rest, ".") || strings.Contains(rest, "..") {
		return fmt.Errorf("%s: must not have empty labels", field)
	}
	for _, label := range strings.Split(rest, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("%s: invalid label %q", field, label)
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return fmt.Errorf("%s: invalid label %q", field, label)
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%s: invalid label %q", field, label)
		}
	}
	return nil
}

// State is the issuance lifecycle of one certificate.
type State string

const (
	// StatePending awaits first issuance.
	StatePending State = "pending"
	// StateIssued holds a valid certificate.
	StateIssued State = "issued"
	// StateFailed records an issuance failure in LastError.
	StateFailed State = "failed"
)

// Valid reports a known state.
func (s State) Valid() bool {
	return s == StatePending || s == StateIssued || s == StateFailed
}

// DefaultRenewalDays renews certificates with a month of validity left
// when no explicit threshold is set.
const DefaultRenewalDays = 30

// Renewal is the renewal policy: automatic renewal at most once per
// cycle, starting DaysBeforeExpiry days out (0 means the default).
// Manual-issuer certificates never auto-renew — there is nothing to
// fetch — so Auto with a manual issuer is invalid.
type Renewal struct {
	Auto             bool
	DaysBeforeExpiry int
}

// Validate checks the renewal policy against its issuer.
func (r Renewal) Validate(issuerType IssuerType) error {
	if r.DaysBeforeExpiry < 0 || r.DaysBeforeExpiry > 90 {
		return fmt.Errorf("cert: renewal days must be 0-90")
	}
	if r.Auto && issuerType == IssuerManual {
		return fmt.Errorf("cert: manual certificates cannot auto-renew")
	}
	return nil
}

// threshold resolves the effective renewal threshold in days.
func (r Renewal) threshold() int {
	if r.DaysBeforeExpiry == 0 {
		return DefaultRenewalDays
	}
	return r.DaysBeforeExpiry
}

// RenewalDue reports whether notAfter falls within the renewal window
// at now. Manual-issuer renewal never comes due automatically.
func (r Renewal) RenewalDue(now, notAfter time.Time) bool {
	if !r.Auto {
		return false
	}
	return now.AddDate(0, 0, r.threshold()).After(notAfter) || now.AddDate(0, 0, r.threshold()).Equal(notAfter)
}

// Certificate is one desired identity: a stable key, the DNS names it
// covers, who issues it, lifecycle state, and renewal policy. Stale
// failure text is invalid — LastError clears on every transition out
// of failed, so readers never act on old news.
type Certificate struct {
	Name      string
	DNSNames  []string
	Issuer    Issuer
	State     State
	Renewal   Renewal
	LastError string
}

// Validate checks a certificate end to end.
func (c Certificate) Validate() error {
	if len(c.Name) == 0 || len(c.Name) > 64 {
		return fmt.Errorf("cert: name must be 1-64 characters")
	}
	for _, r := range c.Name {
		if r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("cert: invalid name %q", c.Name)
		}
	}
	if len(c.DNSNames) == 0 {
		return fmt.Errorf("cert: must cover at least one DNS name")
	}
	seen := map[string]bool{}
	for _, name := range c.DNSNames {
		if err := validateDNSName("cert.dns", name); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("cert: duplicate DNS name %q", name)
		}
		seen[name] = true
	}
	if err := c.Issuer.Validate(); err != nil {
		return err
	}
	if !c.State.Valid() {
		return fmt.Errorf("cert: state must be pending, issued, or failed")
	}
	if err := c.Renewal.Validate(c.Issuer.Type); err != nil {
		return err
	}
	if c.LastError != "" && c.State != StateFailed {
		return fmt.Errorf("cert: error text only belongs on failed certificates")
	}
	return nil
}

// Set is the desired certificate table: unique names, and each DNS name
// claimed by at most one certificate so serving never goes ambiguous.
type Set struct {
	Certificates []Certificate
}

// Validate checks every certificate, name uniqueness, and DNS-name
// ownership.
func (s Set) Validate() error {
	names := map[string]bool{}
	claimed := map[string]string{}
	for i := range s.Certificates {
		if err := s.Certificates[i].Validate(); err != nil {
			return fmt.Errorf("certificate #%d: %w", i, err)
		}
		if names[s.Certificates[i].Name] {
			return fmt.Errorf("duplicate certificate name %q", s.Certificates[i].Name)
		}
		names[s.Certificates[i].Name] = true
		for _, dnsName := range s.Certificates[i].DNSNames {
			if owner, ok := claimed[dnsName]; ok {
				return fmt.Errorf("DNS name %q claimed by both %q and %q", dnsName, owner, s.Certificates[i].Name)
			}
			claimed[dnsName] = s.Certificates[i].Name
		}
	}
	return nil
}

// Find returns the certificate covering dnsName, if any.
func (s Set) Find(dnsName string) (Certificate, bool) {
	for _, c := range s.Certificates {
		for _, name := range c.DNSNames {
			if name == dnsName {
				return c, true
			}
		}
	}
	return Certificate{}, false
}

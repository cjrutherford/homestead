package cert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"time"
)

// Health grades one certificate: healthy serves the intended names
// with room to spare, expiring sits inside the renewal window, expired
// no longer validates, missing has no observed material, mismatch
// serves the wrong names, and invalid means the observed material does
// not parse. Actuation (re-issuance scheduling) lives above this unit,
// which reports only.
const (
	HealthHealthy  = "healthy"
	HealthExpiring = "expiring"
	HealthExpired  = "expired"
	HealthMissing  = "missing"
	HealthMismatch = "mismatch"
	HealthInvalid  = "invalid"
)

// Observed is the inspected live state of one certificate chain.
type Observed struct {
	DNSNames      []string
	NotBefore     time.Time
	NotAfter      time.Time
	Issuer        string
	ChainLength   int
	DaysRemaining int
}

// InspectChain parses chain PEM and reports the leaf's serving state at
// now: names, validity window, issuer, chain depth, and whole days
// remaining (negative past expiry).
func InspectChain(chainPEM []byte, now time.Time) (Observed, error) {
	var certs []*x509.Certificate
	rest := chainPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if !isCertificateBlock(block) {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return Observed{}, fmt.Errorf("cert: parse chain: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return Observed{}, fmt.Errorf("cert: no certificates in chain")
	}
	leaf := certs[0]
	return Observed{
		DNSNames:      leaf.DNSNames,
		NotBefore:     leaf.NotBefore,
		NotAfter:      leaf.NotAfter,
		Issuer:        leaf.Issuer.CommonName,
		ChainLength:   len(certs),
		DaysRemaining: int(leaf.NotAfter.Sub(now).Hours() / 24),
	}, nil
}

// isCertificateBlock accepts CERTIFICATE blocks, the only chain content
// this unit reads (keys and CSR blocks never belong in chain material).
func isCertificateBlock(block *pem.Block) bool {
	return block.Type == "CERTIFICATE"
}

// FetchChain captures the chain a TLS endpoint serves for serverName
// (SNI selects the certificate; the address may be IP:port). Inspection
// skips verification deliberately — trust decisions belong to callers
// verifying against pools — so self-signed and expired chains capture
// exactly what misbehaving endpoints serve.
func FetchChain(ctx context.Context, addr, serverName string) ([]byte, error) {
	if addr == "" || serverName == "" {
		return nil, fmt.Errorf("cert: address and server name must not be empty")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		InsecureSkipVerify: true, // inspection only; see above.
		ServerName:         serverName,
	})
	if err != nil {
		return nil, fmt.Errorf("cert: fetch %s: %w", addr, err)
	}
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("cert: fetch %s: %w", addr, err)
	}
	var chain []byte
	for _, cert := range conn.ConnectionState().PeerCertificates {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("cert: fetch %s: empty chain", addr)
	}
	return chain, nil
}

// Status grades one desired certificate against observed chain PEM keyed
// by certificate name: missing without material, invalid when material
// does not parse, mismatch when the leaf skips desired names, expired
// past NotAfter, expiring inside the renewal window, healthy otherwise.
// RenewalDue follows the certificate's own policy (S100).
type Status struct {
	Name          string
	Health        string
	DaysRemaining int
	NotAfter      time.Time
	RenewalDue    bool
	Detail        string
}

// Check grades every certificate in the set at now. Observed maps
// certificate names to served chain PEM; unknown names stay missing.
func Check(set Set, observed map[string][]byte, now time.Time) ([]Status, error) {
	if err := set.Validate(); err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(set.Certificates))
	for _, certificate := range set.Certificates {
		statuses = append(statuses, checkOne(certificate, observed[certificate.Name], now))
	}
	return statuses, nil
}

// checkOne grades a single certificate.
func checkOne(certificate Certificate, chainPEM []byte, now time.Time) Status {
	status := Status{Name: certificate.Name}
	if len(chainPEM) == 0 {
		status.Health = HealthMissing
		status.Detail = fmt.Sprintf("%s has no observed material", certificate.Name)
		return status
	}
	seen, err := InspectChain(chainPEM, now)
	if err != nil {
		status.Health = HealthInvalid
		status.Detail = fmt.Sprintf("%s observed material does not parse: %v", certificate.Name, err)
		return status
	}
	status.DaysRemaining = seen.DaysRemaining
	status.NotAfter = seen.NotAfter
	covered := map[string]bool{}
	for _, name := range seen.DNSNames {
		covered[name] = true
	}
	for _, want := range certificate.DNSNames {
		if !covered[want] {
			status.Health = HealthMismatch
			status.Detail = fmt.Sprintf("%s serves %q without %q", certificate.Name,
				joinNames(seen.DNSNames), want)
			return status
		}
	}
	switch {
	case !now.Before(seen.NotAfter):
		status.Health = HealthExpired
		status.Detail = fmt.Sprintf("%s expired %s", certificate.Name, seen.NotAfter.Format(time.DateOnly))
	case certificate.Renewal.RenewalDue(now, seen.NotAfter):
		status.Health = HealthExpiring
		status.Detail = fmt.Sprintf("%s expires %s, inside the renewal window",
			certificate.Name, seen.NotAfter.Format(time.DateOnly))
	default:
		status.Health = HealthHealthy
		status.Detail = fmt.Sprintf("%s serves all names until %s",
			certificate.Name, seen.NotAfter.Format(time.DateOnly))
	}
	status.RenewalDue = certificate.Renewal.RenewalDue(now, seen.NotAfter)
	return status
}

// joinNames renders served names for mismatch details.
func joinNames(names []string) string {
	out := ""
	for i, name := range names {
		if i > 0 {
			out += " "
		}
		out += name
	}
	if out == "" {
		return "(no names)"
	}
	return out
}

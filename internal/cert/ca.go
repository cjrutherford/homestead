package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// CA design: a single-tier ECDSA P-256 authority (S104). One level keeps
// the trust story auditable — no intermediates to track — so the CA
// carries MaxPathLenZero and signs server certificates directly.
// Lifetimes: callers choose, with long CA and browser-friendly server
// defaults suggested below. Distribution into OS and client trust
// stores is host mutation owned by the security baseline (S222), never
// here: this unit mints and signs, nothing more.
const (
	// DefaultCALifetime keeps the authority stable for a decade; rotation
	// is a re-issuance event (DEFERRED), not background noise.
	DefaultCALifetime = 10 * 365 * 24 * time.Hour
	// DefaultServerLifetime stays within browser-friendly practice.
	DefaultServerLifetime = 397 * 24 * time.Hour
)

// serialLimit bounds serials to 128 bits, positive by construction.
var serialLimit = new(big.Int).Lsh(big.NewInt(1), 128)

// Authority is the Homestead internal CA: parsed certificate plus its
// private key, with PEM forms for storage (secret store or root-only
// files at the caller's layer).
type Authority struct {
	Cert    *x509.Certificate
	CertPEM []byte
	Key     *ecdsa.PrivateKey
	KeyPEM  []byte
}

// newSerial mints an unpredictable positive serial.
func newSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("cert: serial: %w", err)
	}
	return serial, nil
}

// GenerateCA creates a fresh self-signed authority: CA cert with
// cert-signing usage, single tier, and a P-256 key. now anchors
// validity for deterministic tests; production passes time.Now.
func GenerateCA(commonName string, lifetime time.Duration, now time.Time) (Authority, error) {
	return GenerateConstrainedCA(commonName, lifetime, now, nil)
}

// GenerateConstrainedCA is GenerateCA with an X.509 name constraint: when
// permittedDNS is non-empty the CA may only vouch for names at or under
// those domains (critical, so a verifier that cannot enforce it refuses
// the chain rather than ignoring it). S280 constrains the persisted
// authority to the owner's local suffixes.
func GenerateConstrainedCA(commonName string, lifetime time.Duration, now time.Time, permittedDNS []string) (Authority, error) {
	if len(commonName) == 0 || len(commonName) > 64 {
		return Authority{}, fmt.Errorf("cert: CA name must be 1-64 characters")
	}
	if lifetime < 24*time.Hour || lifetime > 30*365*24*time.Hour {
		return Authority{}, fmt.Errorf("cert: CA lifetime must be 1 day to 30 years")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Authority{}, fmt.Errorf("cert: CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return Authority{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.UTC(),
		NotAfter:              now.UTC().Add(lifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	if len(permittedDNS) > 0 {
		template.PermittedDNSDomainsCritical = true
		template.PermittedDNSDomains = append([]string(nil), permittedDNS...)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return Authority{}, fmt.Errorf("cert: CA self-sign: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Authority{}, fmt.Errorf("cert: CA parse: %w", err)
	}
	keyPEM, err := EncodeAccountKey(key)
	if err != nil {
		return Authority{}, err
	}
	return Authority{
		Cert:    cert,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Key:     key,
		KeyPEM:  keyPEM,
	}, nil
}

// ParseAuthority loads a stored authority, proving the key matches the
// certificate instead of assuming the pair belongs together.
func ParseAuthority(certPEM, keyPEM []byte) (Authority, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return Authority{}, fmt.Errorf("cert: CA certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Authority{}, fmt.Errorf("cert: CA certificate: %w", err)
	}
	if !cert.IsCA {
		return Authority{}, fmt.Errorf("cert: not a CA certificate")
	}
	key, err := ParseAccountKey(keyPEM)
	if err != nil {
		return Authority{}, err
	}
	if !publicKeyEqual(cert.PublicKey, &key.PublicKey) {
		return Authority{}, fmt.Errorf("cert: CA key does not match certificate")
	}
	return Authority{Cert: cert, CertPEM: certPEM, Key: key, KeyPEM: keyPEM}, nil
}

// publicKeyEqual compares ECDSA public keys by coordinates.
func publicKeyEqual(a any, b *ecdsa.PublicKey) bool {
	other, ok := a.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	return other.X.Cmp(b.X) == 0 && other.Y.Cmp(b.Y) == 0 && other.Curve == b.Curve
}

// IssueServerCert signs a server certificate for dnsNames (wildcards
// allowed, matching the S100 spelling) under a fresh P-256 key,
// returning the chain PEM plus the key PEM. Lifetimes bound to hours
// through three years: server certificates rotate, they do not linger.
func (a Authority) IssueServerCert(dnsNames []string, lifetime time.Duration, now time.Time) (chainPEM, keyPEM []byte, err error) {
	if a.Cert == nil || a.Key == nil {
		return nil, nil, fmt.Errorf("cert: authority is empty")
	}
	if !a.Cert.IsCA {
		return nil, nil, fmt.Errorf("cert: authority is not a CA")
	}
	if !publicKeyEqual(a.Cert.PublicKey, &a.Key.PublicKey) {
		return nil, nil, fmt.Errorf("cert: authority key does not match certificate")
	}
	if len(dnsNames) == 0 {
		return nil, nil, fmt.Errorf("cert: must cover at least one DNS name")
	}
	seen := map[string]bool{}
	for _, name := range dnsNames {
		if err := validateDNSName("cert.dns", name); err != nil {
			return nil, nil, err
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("cert: duplicate DNS name %q", name)
		}
		seen[name] = true
	}
	if lifetime < time.Hour || lifetime > 3*365*24*time.Hour {
		return nil, nil, fmt.Errorf("cert: server lifetime must be 1 hour to 3 years")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("cert: server key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: dnsNames[0]},
		DNSNames:              dnsNames,
		NotBefore:             now.UTC(),
		NotAfter:              now.UTC().Add(lifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.Cert, &key.PublicKey, a.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("cert: sign server: %w", err)
	}
	keyPEM, err = EncodeAccountKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM, nil
}

package cert

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"homestead/internal/secret"
	"homestead/internal/store"
)

// Persisted internal CA (S280, ADR-030). The S104 authority is minted
// once, here, and never regenerated silently:
//
//   - the private key is a custody-sealed sys_ secret (ADR-021), named by
//     generation so a rotation is a single atomic row update (new key
//     first, then the row, then the old key is dropped);
//   - the public certificate, fingerprint, serial, validity and name
//     constraints live in SQLite (migration 41) and are mirrored to an
//     inspectable YAML file next to the desired configuration;
//   - nothing private ever reaches YAML, API responses, audit or logs.
//
// Lifecycle and thresholds (documented in docs/architecture/INTERNAL_CA_TRUST.md):
// the root lives DefaultCALifetime (10 years); doctor warns inside
// LocalRootWarnWindow of expiry and fails inside LocalRootFailWindow.
// Root rotation is an explicit, previewed owner action; leaf rotation is
// automatic.
const (
	// LocalCAKeyPrefix prefixes the per-generation key secret name.
	LocalCAKeyPrefix = secret.SystemSecretPrefix + "local_ca_key_"
	// LocalCACommonNamePrefix names the root; a random suffix tells
	// several appliances' roots apart in a device's trust list.
	LocalCACommonNamePrefix = "Homestead Local CA"
	// LocalRootWarnWindow and LocalRootFailWindow grade the root's expiry.
	LocalRootWarnWindow = 365 * 24 * time.Hour
	LocalRootFailWindow = 30 * 24 * time.Hour
)

// LocalPermittedDomains is the name constraint on the root: it can vouch
// for local suffixes only, so a device that trusts it is not exposed to
// impersonation of public names if the key were ever stolen (ADR-030).
var LocalPermittedDomains = []string{"lan", "local", "home.arpa", "internal", "localhost"}

// LocalNameAllowed reports whether host falls under the permitted
// domains the root is constrained to.
func LocalNameAllowed(host string) bool {
	for _, d := range LocalPermittedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// ErrCAKeyMissing reports a recorded root whose private key is not in the
// secret store (for example the key file was lost on restore). The CA is
// never silently replaced: the owner decides, via an explicit rotation.
var ErrCAKeyMissing = errors.New("cert: the internal CA private key is missing from the secret store; restore the secret key file or rotate the root explicitly")

// CertFingerprint is the SHA-256 of a DER certificate in the upper-case
// colon-separated form `openssl x509 -fingerprint -sha256` prints.
func CertFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	hexed := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(sum))
	for i := 0; i < len(hexed); i += 2 {
		parts = append(parts, hexed[i:i+2])
	}
	return strings.Join(parts, ":")
}

// CAInfo is the public view of the persisted authority. It never
// carries key material, only whether the key is present.
type CAInfo struct {
	Present             bool      `json:"present"`
	Subject             string    `json:"subject,omitempty"`
	Serial              string    `json:"serial,omitempty"`
	FingerprintSHA256   string    `json:"fingerprint_sha256,omitempty"`
	NotBefore           time.Time `json:"not_before,omitempty"`
	NotAfter            time.Time `json:"not_after,omitempty"`
	PermittedDNS        []string  `json:"permitted_dns,omitempty"`
	Generation          int       `json:"generation,omitempty"`
	PreviousFingerprint string    `json:"previous_fingerprint_sha256,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	KeyPresent          bool      `json:"key_present"`
	CertificatePEM      string    `json:"certificate_pem,omitempty"`
}

// LocalCA persists and serves the internal authority.
type LocalCA struct {
	Store   *store.Store
	Secrets *secret.Store
	// YAMLPath is the inspectable public mirror; empty disables it.
	YAMLPath string
	// Audit records owner-visible events; never given key material.
	Audit func(actor, action, outcome, detail string) error
	// Lifetime of a newly minted root; zero means DefaultCALifetime.
	Lifetime time.Duration
	// Now stamps records; nil means time.Now.
	Now func() time.Time

	mu sync.Mutex
}

func (l *LocalCA) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *LocalCA) audit(actor, action, outcome, detail string) {
	if l.Audit != nil {
		_ = l.Audit(actor, action, outcome, detail)
	}
}

func caKeyName(generation int) string { return fmt.Sprintf("%s%d", LocalCAKeyPrefix, generation) }

type caRow struct {
	certPEM, fingerprint, serial, subject, permitted string
	notBefore, notAfter, created, previous           string
	generation                                       int
}

func (l *LocalCA) row() (caRow, bool, error) {
	var r caRow
	err := l.Store.DB().QueryRow(`SELECT cert_pem, fingerprint_sha256, serial, subject, permitted_dns,
		not_before, not_after, generation, previous_fingerprint, created_at FROM internal_ca WHERE id = 1`).
		Scan(&r.certPEM, &r.fingerprint, &r.serial, &r.subject, &r.permitted,
			&r.notBefore, &r.notAfter, &r.generation, &r.previous, &r.created)
	if errors.Is(err, sql.ErrNoRows) {
		return caRow{}, false, nil
	}
	if err != nil {
		return caRow{}, false, fmt.Errorf("cert: read internal CA: %w", err)
	}
	return r, true, nil
}

func (l *LocalCA) infoOf(r caRow) CAInfo {
	info := CAInfo{
		Present: true, Subject: r.subject, Serial: r.serial, FingerprintSHA256: r.fingerprint,
		NotBefore: parseTime(r.notBefore), NotAfter: parseTime(r.notAfter),
		Generation: r.generation, PreviousFingerprint: r.previous,
		CreatedAt: parseTime(r.created), CertificatePEM: r.certPEM,
	}
	_ = json.Unmarshal([]byte(r.permitted), &info.PermittedDNS)
	if _, err := l.Secrets.Get(caKeyName(r.generation)); err == nil {
		info.KeyPresent = true
	}
	return info
}

// Info reports the persisted authority; Present is false before it is
// created. Public material only.
func (l *LocalCA) Info() (CAInfo, error) {
	r, ok, err := l.row()
	if err != nil || !ok {
		return CAInfo{}, err
	}
	return l.infoOf(r), nil
}

// Authority loads the signing authority (certificate plus the key from
// the secret store), proving the pair matches. Internal use only.
func (l *LocalCA) Authority() (Authority, error) {
	r, ok, err := l.row()
	if err != nil {
		return Authority{}, err
	}
	if !ok {
		return Authority{}, errors.New("cert: the internal CA has not been created")
	}
	return l.authorityOf(r)
}

func (l *LocalCA) authorityOf(r caRow) (Authority, error) {
	keyPEM, err := l.Secrets.Get(caKeyName(r.generation))
	if errors.Is(err, secret.ErrNotFound) {
		return Authority{}, ErrCAKeyMissing
	}
	if err != nil {
		return Authority{}, err
	}
	return ParseAuthority([]byte(r.certPEM), []byte(keyPEM))
}

// RootPEM and RootDER return the public root certificate only.
func (l *LocalCA) RootPEM() ([]byte, error) {
	r, ok, err := l.row()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoCA
	}
	// Re-encode from the parsed certificate so the output is exactly one
	// CERTIFICATE block, whatever the stored text holds: the unauthenticated
	// download route (ADR-030) can only ever emit public certificate bytes.
	c, err := parseCert([]byte(r.certPEM))
	if err != nil || !c.IsCA {
		return nil, errors.New("cert: stored root is not a CA certificate")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), nil
}

// ErrNoCA reports an absent authority.
var ErrNoCA = errors.New("cert: the internal CA has not been created")

// RootDER is RootPEM decoded to DER.
func (l *LocalCA) RootDER() ([]byte, error) {
	p, err := l.RootPEM()
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, errors.New("cert: stored root is not PEM")
	}
	return block.Bytes, nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// mint creates a new authority and its public row fields.
func (l *LocalCA) mint() (Authority, error) {
	suffix, err := randomSuffix()
	if err != nil {
		return Authority{}, err
	}
	lifetime := l.Lifetime
	if lifetime == 0 {
		lifetime = DefaultCALifetime
	}
	return GenerateConstrainedCA(LocalCACommonNamePrefix+" ("+suffix+")", lifetime, l.now(), LocalPermittedDomains)
}

// Ensure creates the authority exactly once (idempotent). An existing
// authority is verified (key present and matching) and returned, never
// replaced. A key without a row is the debris of an interrupted first
// creation — nothing was ever recorded or published — and is overwritten.
func (l *LocalCA) Ensure(actor string) (CAInfo, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok, err := l.row(); err != nil {
		return CAInfo{}, false, err
	} else if ok {
		if _, err := l.authorityOf(r); err != nil {
			return CAInfo{}, false, err
		}
		return l.infoOf(r), false, nil
	}
	authority, err := l.mint()
	if err != nil {
		return CAInfo{}, false, err
	}
	if err := l.Secrets.SetSystem(caKeyName(1), string(authority.KeyPEM)); err != nil {
		return CAInfo{}, false, err
	}
	r, err := l.insert(authority, 1, "")
	if err != nil {
		return CAInfo{}, false, err
	}
	info := l.infoOf(r)
	l.audit(actor, "tls.local-ca.create", "ok", fmt.Sprintf("internal CA created: %s, SHA-256 %s, valid until %s",
		info.Subject, info.FingerprintSHA256, info.NotAfter.Format(time.RFC3339)))
	l.syncYAML(info)
	return info, true, nil
}

func (l *LocalCA) insert(a Authority, generation int, previous string) (caRow, error) {
	permitted, _ := json.Marshal(a.Cert.PermittedDNSDomains)
	r := caRow{
		certPEM: string(a.CertPEM), fingerprint: CertFingerprint(a.Cert.Raw),
		serial: a.Cert.SerialNumber.Text(16), subject: a.Cert.Subject.CommonName,
		permitted: string(permitted), notBefore: fmtTime(a.Cert.NotBefore), notAfter: fmtTime(a.Cert.NotAfter),
		generation: generation, previous: previous, created: fmtTime(l.now()),
	}
	_, err := l.Store.DB().Exec(`INSERT INTO internal_ca
		(id, cert_pem, fingerprint_sha256, serial, subject, permitted_dns, not_before, not_after, generation, previous_fingerprint, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET cert_pem = excluded.cert_pem, fingerprint_sha256 = excluded.fingerprint_sha256,
		serial = excluded.serial, subject = excluded.subject, permitted_dns = excluded.permitted_dns,
		not_before = excluded.not_before, not_after = excluded.not_after, generation = excluded.generation,
		previous_fingerprint = excluded.previous_fingerprint, created_at = excluded.created_at`,
		r.certPEM, r.fingerprint, r.serial, r.subject, r.permitted, r.notBefore, r.notAfter, r.generation, r.previous, r.created)
	if err != nil {
		return caRow{}, fmt.Errorf("cert: record internal CA: %w", err)
	}
	return r, nil
}

// Rotate replaces the root with a fresh one (an explicit owner action;
// the caller re-issues every leaf). Order is crash-safe: the new key is
// stored first, then the row flips atomically, then the old key is
// dropped. Returns the previous and new public info.
func (l *LocalCA) Rotate(actor string) (previous, current CAInfo, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	old, ok, err := l.row()
	if err != nil {
		return CAInfo{}, CAInfo{}, err
	}
	if !ok {
		return CAInfo{}, CAInfo{}, ErrNoCA
	}
	previous = l.infoOf(old)
	authority, err := l.mint()
	if err != nil {
		return CAInfo{}, CAInfo{}, err
	}
	next := old.generation + 1
	if err := l.Secrets.SetSystem(caKeyName(next), string(authority.KeyPEM)); err != nil {
		return CAInfo{}, CAInfo{}, err
	}
	r, err := l.insert(authority, next, old.fingerprint)
	if err != nil {
		return CAInfo{}, CAInfo{}, err
	}
	_ = l.Secrets.Delete(caKeyName(old.generation))
	current = l.infoOf(r)
	l.audit(actor, "tls.local-ca.rotate", "ok", fmt.Sprintf("internal CA rotated: %s replaces %s; devices must trust the new root",
		current.FingerprintSHA256, previous.FingerprintSHA256))
	l.syncYAML(current)
	return previous, current, nil
}

// yamlDoc is the inspectable public mirror (ADR-030): never a key.
type yamlDoc struct {
	Version           int       `yaml:"version"`
	Note              string    `yaml:"note"`
	Subject           string    `yaml:"subject"`
	Serial            string    `yaml:"serial"`
	FingerprintSHA256 string    `yaml:"fingerprint_sha256"`
	NotBefore         time.Time `yaml:"not_before"`
	NotAfter          time.Time `yaml:"not_after"`
	PermittedDNS      []string  `yaml:"permitted_dns"`
	Generation        int       `yaml:"generation"`
	Certificate       string    `yaml:"certificate"`
}

// SyncYAML rewrites the public YAML mirror from the database (also used
// after a restore, when the file may be absent).
func (l *LocalCA) SyncYAML() error {
	info, err := l.Info()
	if err != nil || !info.Present {
		return err
	}
	return l.writeYAML(info)
}

func (l *LocalCA) syncYAML(info CAInfo) { _ = l.writeYAML(info) }

func (l *LocalCA) writeYAML(info CAInfo) error {
	if l.YAMLPath == "" {
		return nil
	}
	body, err := yaml.Marshal(yamlDoc{
		Version: 1, Note: "Public information only. The private key is custody-sealed in the secret store and is never written here.",
		Subject: info.Subject, Serial: info.Serial, FingerprintSHA256: info.FingerprintSHA256,
		NotBefore: info.NotBefore, NotAfter: info.NotAfter, PermittedDNS: info.PermittedDNS,
		Generation: info.Generation, Certificate: info.CertificatePEM,
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.YAMLPath), ".internal-ca-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, l.YAMLPath)
}

// parseCert decodes the first certificate of a PEM chain.
func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

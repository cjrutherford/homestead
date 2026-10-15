package cert

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"homestead/internal/store"
)

// Renewal statuses (S263, ADR-024). Reporting only: nothing here issues.
const (
	// RenewalOK has time left and an unchanged approved issuance.
	RenewalOK = "ok"
	// RenewalDue is inside the renewal window with the issuance unchanged.
	RenewalDue = "renewal-due"
	// RenewalPlanRequired means the desired issuance differs from the
	// approved one; renewal needs the normal plan, preview, approve flow.
	RenewalPlanRequired = "plan-required"
	// RenewalExpired no longer validates; surfaced as an error.
	RenewalExpired = "expired"
	// RenewalError means the stored chain cannot be read (corrupt);
	// renewal cannot be judged and the record needs owner attention.
	RenewalError = "error"
	// RenewalNoIssuance means a desired certificate has no recorded
	// approved issuance: nothing to renew yet, stated explicitly.
	RenewalNoIssuance = "no-recorded-issuance"
)

// MaxRenewalWindowDays caps the renewal window (ADR-024: 30 days or fewer).
const MaxRenewalWindowDays = 30

// Fingerprint identifies an owner-approved issuance (ADR-024): the
// normalized name set, DNS provider id, and challenge method.
type Fingerprint struct {
	Names      []string `json:"names"`
	ProviderID string   `json:"provider_id"`
	Challenge  string   `json:"challenge"`
}

// NewFingerprint normalizes names (lowercased, trailing dot stripped,
// punycode form, de-duplicated, sorted) so order and spelling never
// matter. Wildcards stay literal: "*.example.com" is its own name and
// neither implies nor is implied by "example.com".
func NewFingerprint(names []string, providerID, challenge string) Fingerprint {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		n := normalizeName(name)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return Fingerprint{Names: out, ProviderID: providerID, Challenge: challenge}
}

// Equal reports an unchanged issuance: identical normalized names,
// provider id, and challenge method.
func (f Fingerprint) Equal(other Fingerprint) bool {
	// Re-normalize so hand-built values compare like constructed ones.
	a := NewFingerprint(f.Names, f.ProviderID, f.Challenge)
	b := NewFingerprint(other.Names, other.ProviderID, other.Challenge)
	if a.ProviderID != b.ProviderID || a.Challenge != b.Challenge || len(a.Names) != len(b.Names) {
		return false
	}
	for i := range a.Names {
		if a.Names[i] != b.Names[i] {
			return false
		}
	}
	return true
}

// String renders the fingerprint for details.
func (f Fingerprint) String() string {
	return fmt.Sprintf("names=[%s] provider=%q challenge=%q",
		strings.Join(f.Names, " "), f.ProviderID, f.Challenge)
}

// lookupProfile is the IDNA Lookup profile (UTS #46 mapping incl. NFC,
// non-transitional, STD3 and hyphen/joiner/bidi checks) plus strict DNS
// length rules so empty labels and over-long labels are errors.
var lookupProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.StrictDomainName(true),
	idna.CheckHyphens(true),
	idna.CheckJoiners(true),
	idna.VerifyDNSLength(true),
)

// invalidPrefix marks a name that failed IDNA validation. It can never
// equal a valid ASCII host name ("!" is not a DNS character), so a
// malformed name makes fingerprints differ instead of silently matching.
const invalidPrefix = "!invalid:"

// NormalizeName returns the ADR-024 comparison form of one name:
// UTS #46 mapping with NFC and lowercasing, ASCII (xn--) labels, one
// trailing dot stripped. A leading wildcard label stays literal. It is
// the IDNA Lookup profile (golang.org/x/net/idna) with strict DNS
// length and label rules, so empty labels, double trailing dots, and
// invalid labels are errors.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	if strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("cert: invalid name %q: empty label", name)
	}
	wildcard := strings.HasPrefix(name, "*.")
	if wildcard {
		name = strings.TrimPrefix(name, "*.")
	}
	ascii, err := lookupProfile.ToASCII(name)
	if err != nil {
		return "", fmt.Errorf("cert: invalid name %q: %w", name, err)
	}
	if ascii == "" {
		return "", fmt.Errorf("cert: invalid name %q: empty", name)
	}
	if wildcard {
		ascii = "*." + ascii
	}
	return ascii, nil
}

func normalizeName(name string) string {
	n, err := NormalizeName(name)
	if err != nil {
		return invalidPrefix + strings.ToLower(strings.TrimSpace(name))
	}
	return n
}

// InvalidNames lists names in the fingerprint that failed normalization.
func (f Fingerprint) InvalidNames() []string {
	var out []string
	for _, n := range f.Names {
		if strings.HasPrefix(n, invalidPrefix) {
			out = append(out, strings.TrimPrefix(n, invalidPrefix))
		}
	}
	return out
}

// Issuance is one stored approved issuance: the fingerprint and the
// public chain it produced. Keys are never stored here.
type Issuance struct {
	Certificate string
	Fingerprint Fingerprint
	ChainPEM    []byte
	IssuedAt    time.Time
	// Directory is the CA directory the chain was issued from; empty for
	// an issuance recorded before it was kept.
	Directory string
}

// Issuances persists approved issuances in SQLite (migration 27).
type Issuances struct {
	db *sql.DB
}

// NewIssuances returns Issuances over an open store.
func NewIssuances(st *store.Store) *Issuances { return &Issuances{db: st.DB()} }

// Record stores (replacing) the approved issuance for a certificate.
func (i *Issuances) Record(name string, fp Fingerprint, chainPEM []byte, at time.Time) error {
	return i.RecordFrom(name, fp, chainPEM, at, "")
}

// RecordFrom is Record noting the CA directory the chain came from.
func (i *Issuances) RecordFrom(name string, fp Fingerprint, chainPEM []byte, at time.Time, directory string) error {
	if name == "" {
		return fmt.Errorf("cert: issuance needs a certificate name")
	}
	encoded, err := json.Marshal(NewFingerprint(fp.Names, fp.ProviderID, fp.Challenge))
	if err != nil {
		return err
	}
	_, err = i.db.Exec(`INSERT INTO cert_issuances (name, fingerprint, chain_pem, issued_at, directory)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET fingerprint = excluded.fingerprint,
	chain_pem = excluded.chain_pem, issued_at = excluded.issued_at, directory = excluded.directory`,
		name, string(encoded), string(chainPEM), at.UTC().Format(time.RFC3339Nano), directory)
	if err != nil {
		return fmt.Errorf("cert: record issuance: %w", err)
	}
	return nil
}

// List returns every stored issuance ordered by certificate name.
func (i *Issuances) List() ([]Issuance, error) {
	rows, err := i.db.Query(`SELECT name, fingerprint, chain_pem, issued_at, directory FROM cert_issuances ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("cert: list issuances: %w", err)
	}
	defer rows.Close()
	var out []Issuance
	for rows.Next() {
		var name, fp, chain, at, directory string
		if err := rows.Scan(&name, &fp, &chain, &at, &directory); err != nil {
			return nil, err
		}
		issuance := Issuance{Certificate: name, ChainPEM: []byte(chain), Directory: directory}
		if err := json.Unmarshal([]byte(fp), &issuance.Fingerprint); err != nil {
			return nil, fmt.Errorf("cert: issuance %q fingerprint: %w", name, err)
		}
		issuance.IssuedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, issuance)
	}
	return out, rows.Err()
}

// RenewalReport is the renewal status response: the per-certificate
// rows plus an explicit statement of how many issuances are recorded, so
// "nothing recorded" is never an unexplained empty list.
type RenewalReport struct {
	Recorded     int             `json:"recorded_issuances"`
	Summary      string          `json:"summary"`
	Certificates []RenewalStatus `json:"certificates"`
}

// NoIssuancesSummary is the explicit state when no issuance is recorded.
// The daemon does not itself issue certificates yet (docs/DEFERRED.md).
const NoIssuancesSummary = "no recorded issuances: no certificate has been issued and recorded through Homestead, so there is nothing to renew (issue one through the plan, approve, apply flow: homesteadctl tls issuance)"

// NewRenewalReport wraps statuses with the recorded-issuance count.
func NewRenewalReport(statuses []RenewalStatus, recorded int) RenewalReport {
	if statuses == nil {
		statuses = []RenewalStatus{}
	}
	summary := fmt.Sprintf("%d recorded issuance(s)", recorded)
	if recorded == 0 {
		summary = NoIssuancesSummary
	}
	return RenewalReport{Recorded: recorded, Summary: summary, Certificates: statuses}
}

// RenewalStatus is the owner-visible renewal state of one certificate.
type RenewalStatus struct {
	Certificate   string    `json:"certificate"`
	Status        string    `json:"status"`
	DaysRemaining int       `json:"days_remaining"`
	NotAfter      time.Time `json:"not_after"`
	Detail        string    `json:"detail"`
	// Unattended re-issue state (S272, ADR-024): consecutive failed
	// attempts, the last failure, and when the next attempt may run.
	Attempts      int       `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     string    `json:"last_error"`
	// PendingReload is a renewed certificate placed on disk that the edge
	// has not adopted yet (the reload was refused).
	PendingReload bool `json:"pending_reload"`
	// RenewalMode is the owner's recorded choice at approval ("auto" or
	// "manual"); empty when no approval is on record.
	RenewalMode string `json:"renewal_mode"`
	// AccountRecoveryRequired means the CA forgot the ACME account and
	// only the owner may re-register it (production directory).
	AccountRecoveryRequired bool `json:"account_recovery_required"`
}

// RenewalWindowDays is the effective window: the certificate's own
// threshold, never more than MaxRenewalWindowDays.
func RenewalWindowDays(r Renewal) int {
	if days := r.threshold(); days < MaxRenewalWindowDays {
		return days
	}
	return MaxRenewalWindowDays
}

// CheckRenewals reports renewal status at now for each desired
// certificate. One with no stored approved issuance reports
// no-recorded-issuance (nothing to renew). It grades the stored chain through Check, then applies the
// ADR-024 rules: expired first, then plan-required when the desired
// issuance differs from the approved fingerprint (or the chain no longer
// serves the desired names), then renewal-due inside the window, else ok.
// It never issues.
func CheckRenewals(set Set, issuances []Issuance, now time.Time) ([]RenewalStatus, error) {
	observed := map[string][]byte{}
	byName := map[string]Issuance{}
	for _, issuance := range issuances {
		observed[issuance.Certificate] = issuance.ChainPEM
		byName[issuance.Certificate] = issuance
	}
	graded, err := Check(set, observed, now)
	if err != nil {
		return nil, err
	}
	out := []RenewalStatus{}
	for i, health := range graded {
		certificate := set.Certificates[i]
		issuance, ok := byName[certificate.Name]
		if !ok {
			out = append(out, RenewalStatus{Certificate: certificate.Name, Status: RenewalNoIssuance,
				Detail: certificate.Name + " has no recorded approved issuance; nothing to renew"})
			continue
		}
		status := RenewalStatus{Certificate: certificate.Name, DaysRemaining: health.DaysRemaining, NotAfter: health.NotAfter}
		desired := NewFingerprint(certificate.DNSNames, certificate.Issuer.DNSProvider, string(certificate.Issuer.Challenge))
		window := RenewalWindowDays(certificate.Renewal)
		switch {
		case health.Health == HealthExpired:
			status.Status = RenewalExpired
			status.Detail = health.Detail
		case health.Health == HealthInvalid:
			status.Status = RenewalError
			status.Detail = certificate.Name + " stored issuance is corrupt and cannot be read; re-issue through the plan, preview, and approve flow (" + health.Detail + ")"
		case len(desired.InvalidNames()) > 0:
			status.Status = RenewalPlanRequired
			status.Detail = fmt.Sprintf("%s has invalid desired names %v; fix the strategy and re-plan",
				certificate.Name, desired.InvalidNames())
		case !desired.Equal(issuance.Fingerprint):
			status.Status = RenewalPlanRequired
			status.Detail = fmt.Sprintf("%s desired issuance differs from the approved one (approved %s; desired %s); re-plan and approve",
				certificate.Name, issuance.Fingerprint, desired)
		case health.Health == HealthMismatch:
			status.Status = RenewalPlanRequired
			status.Detail = health.Detail
		case !now.AddDate(0, 0, window).Before(health.NotAfter) && !certificate.Renewal.Auto &&
			certificate.Issuer.Type != IssuerACME:
			// Manual semantics (S100): without auto-renew, renewal never
			// comes due on its own; the owner is told, not nagged. ACME
			// issuances recorded by the S278 flow are placed for Caddy to
			// serve, so Caddy no longer renews them: they warn inside the
			// window whatever the auto flag says (S272 re-issues dns-01 ones).
			status.Status = RenewalOK
			status.Detail = fmt.Sprintf("%s expires %s, inside the %d-day window, but auto-renew is off: renew it manually",
				certificate.Name, health.NotAfter.Format(time.DateOnly), window)
		case !now.AddDate(0, 0, window).Before(health.NotAfter):
			status.Status = RenewalDue
			status.Detail = fmt.Sprintf("%s expires %s, inside the %d-day renewal window",
				certificate.Name, health.NotAfter.Format(time.DateOnly), window)
			if isDNS01ACME(certificate) {
				// The approval's renewal mode decides (S272); the report
				// layer adds the sentence.
			} else if !certificate.Renewal.Auto {
				status.Detail += "; Caddy does not renew a certificate Homestead placed and auto-renew is off, so re-run the issuance (plan, approve, apply) before it expires"
			} else {
				status.Detail += "; Caddy does not renew a certificate Homestead placed, so re-run the issuance (plan, approve, apply) before it expires"
			}
		default:
			status.Status = RenewalOK
			status.Detail = fmt.Sprintf("%s valid until %s", certificate.Name, health.NotAfter.Format(time.DateOnly))
		}
		out = append(out, status)
	}
	return out, nil
}

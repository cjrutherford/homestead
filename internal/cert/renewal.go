package cert

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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

// normalizeName lowercases, strips one trailing dot, and converts each
// non-ASCII label to its xn-- form. A wildcard label passes through.
func normalizeName(name string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	labels := strings.Split(name, ".")
	for i, label := range labels {
		if label == "*" {
			continue
		}
		labels[i] = toASCIILabel(label)
	}
	return strings.Join(labels, ".")
}

func toASCIILabel(label string) string {
	for _, r := range label {
		if r > 127 {
			return "xn--" + punycode(label)
		}
	}
	return label
}

// punycode is the RFC 3492 encoder (bootstring, base 36).
func punycode(input string) string {
	const (
		base, tmin, tmax, skew, damp = 36, 1, 26, 38, 700
		initialBias, initialN        = 72, 128
	)
	runes := []rune(input)
	var out []byte
	for _, r := range runes {
		if r < 128 {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	adapt := func(delta, points int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	n, delta, bias := initialN, 0, initialBias
	for handled < len(runes) {
		m := int(^uint(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (handled + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tmin {
						t = tmin
					} else if t > tmax {
						t = tmax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, handled+1, handled == basic)
				delta = 0
				handled++
			}
		}
		delta++
		n++
	}
	return string(out)
}

// Issuance is one stored approved issuance: the fingerprint and the
// public chain it produced. Keys are never stored here.
type Issuance struct {
	Certificate string
	Fingerprint Fingerprint
	ChainPEM    []byte
	IssuedAt    time.Time
}

// Issuances persists approved issuances in SQLite (migration 26).
type Issuances struct {
	db *sql.DB
}

// NewIssuances returns Issuances over an open store.
func NewIssuances(st *store.Store) *Issuances { return &Issuances{db: st.DB()} }

// Record stores (replacing) the approved issuance for a certificate.
func (i *Issuances) Record(name string, fp Fingerprint, chainPEM []byte, at time.Time) error {
	if name == "" {
		return fmt.Errorf("cert: issuance needs a certificate name")
	}
	encoded, err := json.Marshal(NewFingerprint(fp.Names, fp.ProviderID, fp.Challenge))
	if err != nil {
		return err
	}
	_, err = i.db.Exec(`INSERT INTO cert_issuances (name, fingerprint, chain_pem, issued_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET fingerprint = excluded.fingerprint,
	chain_pem = excluded.chain_pem, issued_at = excluded.issued_at`,
		name, string(encoded), string(chainPEM), at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("cert: record issuance: %w", err)
	}
	return nil
}

// List returns every stored issuance ordered by certificate name.
func (i *Issuances) List() ([]Issuance, error) {
	rows, err := i.db.Query(`SELECT name, fingerprint, chain_pem, issued_at FROM cert_issuances ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("cert: list issuances: %w", err)
	}
	defer rows.Close()
	var out []Issuance
	for rows.Next() {
		var name, fp, chain, at string
		if err := rows.Scan(&name, &fp, &chain, &at); err != nil {
			return nil, err
		}
		issuance := Issuance{Certificate: name, ChainPEM: []byte(chain)}
		if err := json.Unmarshal([]byte(fp), &issuance.Fingerprint); err != nil {
			return nil, fmt.Errorf("cert: issuance %q fingerprint: %w", name, err)
		}
		issuance.IssuedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, issuance)
	}
	return out, rows.Err()
}

// RenewalStatus is the owner-visible renewal state of one certificate.
type RenewalStatus struct {
	Certificate   string    `json:"certificate"`
	Status        string    `json:"status"`
	DaysRemaining int       `json:"days_remaining"`
	NotAfter      time.Time `json:"not_after"`
	Detail        string    `json:"detail"`
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
// certificate that has a stored approved issuance (certificates never
// issued through the owner-approved flow have nothing to renew and are
// omitted). It grades the stored chain through Check, then applies the
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
			status.Status = RenewalPlanRequired
			status.Detail = health.Detail
		case !desired.Equal(issuance.Fingerprint):
			status.Status = RenewalPlanRequired
			status.Detail = fmt.Sprintf("%s desired issuance differs from the approved one (approved %s; desired %s); re-plan and approve",
				certificate.Name, issuance.Fingerprint, desired)
		case health.Health == HealthMismatch:
			status.Status = RenewalPlanRequired
			status.Detail = health.Detail
		case !now.AddDate(0, 0, window).Before(health.NotAfter):
			status.Status = RenewalDue
			status.Detail = fmt.Sprintf("%s expires %s, inside the %d-day renewal window",
				certificate.Name, health.NotAfter.Format(time.DateOnly), window)
		default:
			status.Status = RenewalOK
			status.Detail = fmt.Sprintf("%s valid until %s", certificate.Name, health.NotAfter.Format(time.DateOnly))
		}
		out = append(out, status)
	}
	return out, nil
}

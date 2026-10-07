package cert

import (
	"context"
	"fmt"
	"strings"
	"time"

	"homestead/internal/dns"
)

// challengeTTL serves TXT values briefly: challenges are short-lived by
// design, and 60 is the model's floor.
const challengeTTL = 60

// ChallengeRecords maps ACME authorizations to the TXT records proving
// them: `_acme-challenge` under each name, wildcards stripped to their
// base (`*.apps.example.com` proves at `_acme-challenge.apps`). Keys are
// certificate DNS names within zoneName; tokens are the CA-supplied
// values. Every record validates before return, so bad authorizations
// fail here instead of at the provider.
func ChallengeRecords(zoneName string, authorizations map[string]string) ([]dns.Record, error) {
	if len(authorizations) == 0 {
		return nil, fmt.Errorf("cert: authorizations must not be empty")
	}
	var out []dns.Record
	for name, token := range authorizations {
		if err := validateDNSName("cert.dns", name); err != nil {
			return nil, err
		}
		base := strings.TrimPrefix(name, "*.")
		var relative string
		switch {
		case base == zoneName:
			relative = "_acme-challenge"
		case strings.HasSuffix(base, "."+zoneName):
			relative = "_acme-challenge." + strings.TrimSuffix(base, "."+zoneName)
		default:
			return nil, fmt.Errorf("cert: %q is outside zone %q", name, zoneName)
		}
		record := dns.Record{Name: relative, Type: dns.RecordTXT, Value: token, TTLSeconds: challengeTTL}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("cert: challenge for %q: %w", name, err)
		}
		out = append(out, record)
	}
	return out, nil
}

// Placer runs the DNS-01 lifecycle against one provider and zone:
// place values, await their propagation, hand control to issuance,
// then remove exactly what was placed. Credentials arrive assembled
// (S096 holds them); this unit never touches the secret store.
type Placer struct {
	Provider dns.Provider
	Zone     string
	Creds    dns.Credentials
}

// validate checks provider, zone, and credentials presence.
func (p Placer) validate() error {
	if p.Provider == nil {
		return fmt.Errorf("cert: provider must not be nil")
	}
	if err := dns.ValidateProvider(p.Provider); err != nil {
		return err
	}
	return nil
}

// Place creates challenge TXT values without touching anything else:
// only creates from the diff apply, so pre-existing TXT records (SPF,
// DKIM, other challenges) survive placement unconditionally.
func (p Placer) Place(ctx context.Context, records []dns.Record) error {
	if err := p.validate(); err != nil {
		return err
	}
	observed, err := p.Provider.ListRecords(ctx, p.Zone, p.Creds)
	if err != nil {
		return fmt.Errorf("cert: place: %w", err)
	}
	var creates []dns.Change
	for _, change := range dns.DiffRecords(records, observed) {
		if change.Action == dns.ChangeCreate {
			creates = append(creates, change)
		}
	}
	if len(creates) == 0 {
		return nil
	}
	if err := p.Provider.ApplyChanges(ctx, p.Zone, p.Creds, creates); err != nil {
		return fmt.Errorf("cert: place: %w", err)
	}
	return nil
}

// AwaitPropagation polls the public probe until every challenge value
// answers or ctx ends, reusing the propagation verifier so placement
// and readiness share one definition of done. Poll interval is the
// caller's to own (DNS caches make seconds-long sleeps pointless to
// hard-code); ctx carries the deadline.
func AwaitPropagation(ctx context.Context, probe dns.ProbeFunc, zoneName string, records []dns.Record, interval time.Duration) error {
	if probe == nil {
		return fmt.Errorf("cert: probe must not be nil")
	}
	if interval <= 0 {
		return fmt.Errorf("cert: poll interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := dns.VerifyRecords(ctx, probe, zoneName, records); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cert: challenges not propagated: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Cleanup removes exactly the placed values: deletes compute against a
// fresh listing, so absent values skip instead of erroring and foreign
// TXT records survive. Cleanup is idempotent — rerunning after partial
// failure converges to nothing left behind.
func (p Placer) Cleanup(ctx context.Context, records []dns.Record) error {
	if err := p.validate(); err != nil {
		return err
	}
	observed, err := p.Provider.ListRecords(ctx, p.Zone, p.Creds)
	if err != nil {
		return fmt.Errorf("cert: cleanup: %w", err)
	}
	present := map[dns.Record]bool{}
	for _, r := range observed {
		present[r] = true
	}
	var deletes []dns.Change
	for _, record := range records {
		if present[record] {
			deletes = append(deletes, dns.Change{Action: dns.ChangeDelete, Record: record})
		}
	}
	if len(deletes) == 0 {
		return nil
	}
	if err := p.Provider.ApplyChanges(ctx, p.Zone, p.Creds, deletes); err != nil {
		return fmt.Errorf("cert: cleanup: %w", err)
	}
	return nil
}

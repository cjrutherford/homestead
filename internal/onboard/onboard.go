package onboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"homestead/internal/auth"
	"homestead/internal/baseline"
	"homestead/internal/claim"
	"homestead/internal/domain"
	"homestead/internal/firewall"
	"homestead/internal/store"
)

// ErrNotOnboarded reports no connected customer domain yet.
var ErrNotOnboarded = errors.New("onboard: no domain onboarded")

// Onboarded is the connected customer domain: the validated plan
// plus the acting owner. One appliance connects one domain
// (single-appliance scope), so records replace rather than
// accumulate.
type Onboarded struct {
	Provider string
	Zone     string
	Base     string
	FQDN     string
	Actor    string
}

// Service connects the wizard-selected customer domain after the
// security baseline (S222) holds and the provider proves ownership
// (S097 validation). It owns no HTTP surface: bootstrap slices
// drive it, starting here.
type Service struct {
	domains *domain.Service
	claim   *claim.Service
	auth    *auth.Service
	detect  func() firewall.State
	db      *sql.DB
}

// New returns a Service over the domain flow, identity
// services, firewall detection, and the operational store.
func New(domains *domain.Service, claimSvc *claim.Service, authSvc *auth.Service, detect func() firewall.State, st *store.Store) *Service {
	return &Service{domains: domains, claim: claimSvc, auth: authSvc, detect: detect, db: st.DB()}
}

// Baseline reports the gate state without enforcing: callers show
// this before offering onboarding, and Onboard enforces it.
func (o *Service) Baseline() baseline.Report {
	return baseline.Assess(baseline.Inputs{
		Claim: o.claim, Auth: o.auth, Firewall: o.detect, Now: time.Now(),
	})
}

// Onboard gates, proves, and records: the baseline must hold, the
// provider must list the zone (ownership proof; manual attests by
// construction, per S097), and the validated plan records as the
// connected domain under the acting owner. Re-running re-validates
// and replaces — onboarding converges, never duplicates.
func (o *Service) Onboard(ctx context.Context, actor string) (Onboarded, error) {
	if actor == "" {
		return Onboarded{}, fmt.Errorf("onboard: actor must not be empty")
	}
	report := o.Baseline()
	if !report.Ready {
		var missing []string
		for _, check := range report.Checks {
			if check.Status != baseline.StatusPass {
				missing = append(missing, fmt.Sprintf("%s: %s", check.Name, check.Detail))
			}
		}
		return Onboarded{}, fmt.Errorf("onboard: security baseline incomplete: %s", strings.Join(missing, "; "))
	}
	if err := o.domains.Validate(ctx); err != nil {
		return Onboarded{}, err
	}
	plan, err := o.domains.Plan()
	if err != nil {
		return Onboarded{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := o.db.Exec(
		`INSERT INTO domain_onboarding (id, provider, zone, base, fqdn, actor, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET provider = excluded.provider, zone = excluded.zone, base = excluded.base, fqdn = excluded.fqdn, actor = excluded.actor, updated_at = excluded.updated_at`,
		plan.Provider, plan.Zone, plan.Base, plan.FQDN, actor, now,
	); err != nil {
		return Onboarded{}, fmt.Errorf("onboard: record onboarding: %w", err)
	}
	return Onboarded{Provider: plan.Provider, Zone: plan.Zone, Base: plan.Base, FQDN: plan.FQDN, Actor: actor}, nil
}

// Current returns the connected domain, or ErrNotOnboarded when the
// appliance still serves local names only.
func (o *Service) Current() (Onboarded, error) {
	var onboarded Onboarded
	err := o.db.QueryRow(`SELECT provider, zone, base, fqdn, actor FROM domain_onboarding WHERE id = 1`).Scan(
		&onboarded.Provider, &onboarded.Zone, &onboarded.Base, &onboarded.FQDN, &onboarded.Actor,
	)
	if err == sql.ErrNoRows {
		return Onboarded{}, ErrNotOnboarded
	}
	if err != nil {
		return Onboarded{}, fmt.Errorf("onboard: read onboarding: %w", err)
	}
	return onboarded, nil
}

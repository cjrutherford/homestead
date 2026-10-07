package exposure

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"homestead/internal/audit"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// ErrNoOverride reports reads of hosts holding no override.
var ErrNoOverride = errors.New("exposure: no override")

// Override actions recorded in the audit trail.
const (
	ActionGrant  = "exposure.override"
	ActionRevoke = "exposure.revoke"
)

// Override is one live exposure exception: the host, its owner-chosen
// exposure, the required justification, who chose it, whether it
// exposes appliance management, and when. Management marks platform
// surfaces (dashboard, admin UIs) facing wider origins — the highest
// scrutiny class — and rides visibly in audit rather than hiding in a
// flag nobody reads.
type Override struct {
	Host       docker.Hostname
	To         docker.Exposure
	Reason     string
	Actor      string
	Management bool
	CreatedAt  time.Time
}

// Overrides keeps the live exception set over the operational database
// (migration v8) with every grant and revocation appended to the audit
// trail (S063). Reads enforce; the trail explains.
type Overrides struct {
	db    *sql.DB
	audit *audit.Service
}

// NewOverrides returns an Overrides over an open store with audit.
func NewOverrides(s *store.Store, trail *audit.Service) *Overrides {
	return &Overrides{db: s.DB(), audit: trail}
}

// validateGrant checks override shape: host spelling, known target,
// non-empty bounded reason and actor, and the two absolute refusals —
// private names cannot go public (physics, not policy), and closed
// package ceilings hold no matter who asks (S122).
func validateGrant(host docker.Hostname, to docker.Exposure, reason, actor string, endpoint *EndpointExposure) error {
	if err := validateExposureHost(host); err != nil {
		return err
	}
	if to != docker.ExposureLocal && to != docker.ExposureOverlay && to != docker.ExposurePublic {
		return fmt.Errorf("exposure: target must be local, overlay, or public")
	}
	if len(reason) == 0 || len(reason) > 500 {
		return fmt.Errorf("exposure: reason must be 1-500 characters")
	}
	if actor == "" {
		return fmt.Errorf("exposure: actor must not be empty")
	}
	if to == docker.ExposurePublic && docker.IsPrivateName(host) {
		return fmt.Errorf("exposure: %q is private and cannot go public", host)
	}
	if endpoint != nil {
		if err := endpoint.Validate(); err != nil {
			return err
		}
		if !endpoint.Allows(to) {
			return fmt.Errorf("exposure: %q ceiling of %s holds against overrides", endpoint.Endpoint, endpoint.Recommended)
		}
	}
	return nil
}

// validateExposureHost checks hostname spelling for override keys,
// sharing the route spelling without importing route semantics.
func validateExposureHost(host docker.Hostname) error {
	name := string(host)
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("exposure: host must be 1-253 characters")
	}
	for _, label := range splitLabels(name) {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("exposure: invalid label %q", label)
		}
	}
	return nil
}

// splitLabels splits dotted names without case opinions: overrides key
// whatever the route model accepted, verbatim.
func splitLabels(name string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			out = append(out, name[start:i])
			start = i + 1
		}
	}
	return out
}

// Grant records an exposure exception and audits it with the override
// flag and reason. Re-granting replaces: owners change their minds,
// and the trail keeps every version.
func (o *Overrides) Grant(host docker.Hostname, to docker.Exposure, reason, actor string, management bool, endpoint *EndpointExposure) (Override, error) {
	if err := validateGrant(host, to, reason, actor, endpoint); err != nil {
		return Override{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := o.db.Exec(
		`INSERT INTO exposure_overrides (host, to_exposure, reason, actor, management, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET to_exposure = excluded.to_exposure,
		 reason = excluded.reason, actor = excluded.actor,
		 management = excluded.management, created_at = excluded.created_at`,
		string(host), string(to), reason, actor, management, now,
	); err != nil {
		return Override{}, fmt.Errorf("exposure: grant %q: %w", host, err)
	}
	plan, err := marshalPlan(host, to)
	if err != nil {
		return Override{}, err
	}
	if _, err := o.audit.Record(audit.Entry{
		Actor: actor, Action: ActionGrant, Plan: plan, Outcome: audit.OutcomeOK,
		Detail: detailFor(management), Override: true, OverrideReason: reason,
	}); err != nil {
		return Override{}, fmt.Errorf("exposure: audit grant %q: %w", host, err)
	}
	return o.Get(host)
}

// marshalPlan renders the audit plan document for an override.
func marshalPlan(host docker.Hostname, to docker.Exposure) (string, error) {
	plan := fmt.Sprintf(`{"host":%q,"to":%q}`, string(host), string(to))
	return plan, nil
}

// detailFor marks management exposure distinctly in audit detail.
func detailFor(management bool) string {
	if management {
		return "management exposure override"
	}
	return "exposure override"
}

// Revoke drops an override and audits the removal. Absent hosts
// converge to nil so cleanup never trips on ordering.
func (o *Overrides) Revoke(host docker.Hostname, actor string) error {
	if actor == "" {
		return fmt.Errorf("exposure: actor must not be empty")
	}
	if _, err := o.db.Exec(`DELETE FROM exposure_overrides WHERE host = ?`, string(host)); err != nil {
		return fmt.Errorf("exposure: revoke %q: %w", host, err)
	}
	plan, err := marshalPlan(host, "")
	if err != nil {
		return err
	}
	if _, err := o.audit.Record(audit.Entry{
		Actor: actor, Action: ActionRevoke, Plan: plan, Outcome: audit.OutcomeOK,
		Detail: "exposure override revoked",
	}); err != nil {
		return fmt.Errorf("exposure: audit revoke %q: %w", host, err)
	}
	return nil
}

// Get returns the live override for a host.
func (o *Overrides) Get(host docker.Hostname) (Override, error) {
	var override Override
	var to, created string
	var management bool
	err := o.db.QueryRow(
		`SELECT host, to_exposure, reason, actor, management, created_at
		 FROM exposure_overrides WHERE host = ?`, string(host),
	).Scan(&override.Host, &to, &override.Reason, &override.Actor, &management, &created)
	if err == sql.ErrNoRows {
		return Override{}, ErrNoOverride
	}
	if err != nil {
		return Override{}, fmt.Errorf("exposure: get %q: %w", host, err)
	}
	override.To = docker.Exposure(to)
	override.Management = management
	override.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Override{}, fmt.Errorf("exposure: get %q: %w", host, err)
	}
	return override, nil
}

// List returns every live override ordered by host.
func (o *Overrides) List() ([]Override, error) {
	rows, err := o.db.Query(
		`SELECT host, to_exposure, reason, actor, management, created_at
		 FROM exposure_overrides ORDER BY host`)
	if err != nil {
		return nil, fmt.Errorf("exposure: list: %w", err)
	}
	defer rows.Close()
	var out []Override
	for rows.Next() {
		var override Override
		var to, created string
		var management bool
		if err := rows.Scan(&override.Host, &to, &override.Reason, &override.Actor, &management, &created); err != nil {
			return nil, fmt.Errorf("exposure: list: %w", err)
		}
		override.To = docker.Exposure(to)
		override.Management = management
		override.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("exposure: list: %w", err)
		}
		out = append(out, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("exposure: list: %w", err)
	}
	return out, nil
}

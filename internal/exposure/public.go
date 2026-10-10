package exposure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"homestead/internal/dns"
	"homestead/internal/docker"
	"homestead/internal/store"
)

// PublicValidation is the PUBLIC_VALIDATED verdict for one route
// (S228): the S126 five-layer report (DNS, both edge ports, TLS
// trust, served response) plus the management guard, gated on a
// public-target S227 approval. Passed holds only when the route is
// approved public, every layer holds, and no management name
// resolves to the public address. Failed names every failed check
// for owners and the dashboard alike.
type PublicValidation struct {
	Host            string
	Approved        bool
	Layers          Report
	ManagementClean bool
	Failed          []string
	Passed          bool
}

// ErrNoValidation reports no recorded public verdict for a host.
var ErrNoValidation = errors.New("exposure: route never validated")

// ValidatePublic reaches the verdict for host: approval first (only
// approved-public routes can validate — management routes without
// one never reach PUBLIC_VALIDATED by construction), then the five
// layers, then the management guard over caller-named management
// hosts. Every verdict records, pass or fail: the dashboard shows
// PUBLIC / HEALTHY only from a passing record, and failures explain
// themselves in Failed.
func ValidatePublic(ctx context.Context, st *store.Store, host docker.Hostname, target Target, probe dns.ProbeFunc, management []string) (PublicValidation, error) {
	if st == nil {
		return PublicValidation{}, fmt.Errorf("exposure: validation needs a store")
	}
	if host == "" {
		return PublicValidation{}, fmt.Errorf("exposure: validation needs a host")
	}
	verdict := PublicValidation{Host: string(host), ManagementClean: true}
	approval, err := ApprovalFor(st, host)
	if err != nil {
		return PublicValidation{}, err
	}
	if approval.Plan.To != docker.ExposurePublic {
		return PublicValidation{}, fmt.Errorf("exposure: %q approved %s, not public", host, approval.Plan.To)
	}
	if target.Host != string(host) {
		return PublicValidation{}, fmt.Errorf("exposure: target %q is not approved host %q", target.Host, host)
	}
	verdict.Approved = true
	layers, err := Validate(ctx, probe, target)
	if err != nil {
		return PublicValidation{}, err
	}
	verdict.Layers = layers
	for _, finding := range layers.Checks {
		if !finding.OK {
			verdict.Failed = append(verdict.Failed, finding.Check)
		}
	}
	for _, name := range management {
		if name == "" || name == target.Host {
			continue
		}
		exposed, err := managementExposed(ctx, probe, name, target.Address)
		if err != nil {
			return PublicValidation{}, err
		}
		if exposed {
			verdict.ManagementClean = false
			verdict.Failed = append(verdict.Failed, "management:"+name)
		}
	}
	verdict.Passed = verdict.Approved && layers.Passed && verdict.ManagementClean
	if err := recordValidation(st, verdict); err != nil {
		return PublicValidation{}, err
	}
	return verdict, nil
}

// managementExposed reports whether a management name resolves to the
// public address through public DNS: the unintended-exposure trip
// wire. Unresolvable names read as clean — names nobody can resolve
// cannot serve anyone. Probe outages therefore lean open, which the
// main DNS layer (already failed in that case) covers.
func managementExposed(ctx context.Context, probe dns.ProbeFunc, name string, public netip.Addr) (bool, error) {
	if probe == nil {
		return false, fmt.Errorf("exposure: probe must not be nil")
	}
	typ := dns.RecordA
	if public.Is6() {
		typ = dns.RecordAAAA
	}
	answers, err := probe(ctx, name, typ)
	if err != nil {
		return false, nil
	}
	for _, answer := range answers {
		if addr, err := netip.ParseAddr(answer); err == nil && addr == public {
			return true, nil
		}
	}
	return false, nil
}

// recordValidation stores the verdict, replacing earlier runs: the
// record is the last word, not history.
func recordValidation(st *store.Store, verdict PublicValidation) error {
	failed := verdict.Failed
	if failed == nil {
		failed = []string{}
	}
	encoded, err := json.Marshal(failed)
	if err != nil {
		return fmt.Errorf("exposure: encode verdict: %w", err)
	}
	passed := 0
	if verdict.Passed {
		passed = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT INTO public_validation (host, passed, failed, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET passed = excluded.passed, failed = excluded.failed, updated_at = excluded.updated_at`,
		verdict.Host, passed, string(encoded), now,
	); err != nil {
		return fmt.Errorf("exposure: record verdict: %w", err)
	}
	return nil
}

// ValidationRecord is the stored verdict without the layer detail:
// enough for status surfaces, with Failed explaining.
type ValidationRecord struct {
	Host   string
	Passed bool
	Failed []string
}

// CurrentValidation returns the recorded verdict for host, or
// ErrNoValidation when the route never validated.
func CurrentValidation(st *store.Store, host docker.Hostname) (ValidationRecord, error) {
	var record ValidationRecord
	var encoded string
	var passed int
	err := st.DB().QueryRow(`SELECT passed, failed FROM public_validation WHERE host = ?`, string(host)).Scan(&passed, &encoded)
	if err == sql.ErrNoRows {
		return ValidationRecord{}, ErrNoValidation
	}
	if err != nil {
		return ValidationRecord{}, fmt.Errorf("exposure: read verdict: %w", err)
	}
	record.Host = string(host)
	record.Passed = passed != 0
	if err := json.Unmarshal([]byte(encoded), &record.Failed); err != nil {
		return ValidationRecord{}, fmt.Errorf("exposure: decode verdict: %w", err)
	}
	if record.Failed == nil {
		record.Failed = []string{}
	}
	return record, nil
}

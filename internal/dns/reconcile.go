package dns

import (
	"context"
	"fmt"
)

// EffectiveRecords merges a zone for one audience (ADR-007): baseline
// answers stand except where the audience's view covers the same
// name/type key, which the view wins. Zones without a view for the
// audience serve baseline everywhere — single-view zones need no
// per-audience copies. Merging is replace-by-key, never union: a view
// that answers a name owns that answer for its audience.
func EffectiveRecords(zone Zone, audience Audience) ([]Record, error) {
	if err := zone.Validate(); err != nil {
		return nil, err
	}
	if !audience.Valid() {
		return nil, fmt.Errorf("dns: unknown audience %q", audience)
	}
	type key struct {
		name string
		typ  RecordType
	}
	covered := map[key]bool{}
	var viewRecords []Record
	for _, view := range zone.Views {
		if view.Audience == audience {
			viewRecords = view.Records
			for _, r := range view.Records {
				covered[key{r.Name, r.Type}] = true
			}
			break
		}
	}
	var out []Record
	for _, r := range zone.Records {
		if !covered[key{r.Name, r.Type}] {
			out = append(out, r)
		}
	}
	return append(out, viewRecords...), nil
}

// SplitChanges gates destructive application: deletes apply only when
// allowDeletes is set, otherwise they hold for preview and audit while
// creates proceed. This is the gate the provider contract (S091)
// promises — providers apply what they receive, and this decides what
// they may receive.
func SplitChanges(changes []Change, allowDeletes bool) (apply, held []Change) {
	for _, change := range changes {
		if change.Action == ChangeDelete && !allowDeletes {
			held = append(held, change)
			continue
		}
		apply = append(apply, change)
	}
	return apply, held
}

// Preview is the reconcilable plan for one audience: the effective
// table, the gated applicable changes, and the held deletes. Owners
// review this before anything mutates provider state.
type Preview struct {
	Audience  Audience
	Effective []Record
	Apply     []Change
	Held      []Change
}

// PreviewZone diffs an audience's effective table against observed
// provider state and gates the result.
func PreviewZone(zone Zone, audience Audience, observed []Record, allowDeletes bool) (Preview, error) {
	effective, err := EffectiveRecords(zone, audience)
	if err != nil {
		return Preview{}, err
	}
	apply, held := SplitChanges(DiffRecords(effective, observed), allowDeletes)
	return Preview{Audience: audience, Effective: effective, Apply: apply, Held: held}, nil
}

// Summary reports a reconciliation run: what applied and what held.
type Summary struct {
	Audience Audience
	Applied  []Change
	Held     []Change
}

// Reconcile brings one audience's provider state to the effective
// table: list observed, preview with the delete gate, apply the gated
// set (skipped when empty), and report. Deletes stay held unless the
// caller explicitly allows them — public exposure flows (S227) set that
// flag only after owner review.
func Reconcile(ctx context.Context, provider Provider, zone Zone, creds Credentials, audience Audience, allowDeletes bool) (Summary, error) {
	if err := ValidateProvider(provider); err != nil {
		return Summary{}, err
	}
	if err := zone.Validate(); err != nil {
		return Summary{}, err
	}
	observed, err := provider.ListRecords(ctx, zone.Name, creds)
	if err != nil {
		return Summary{}, fmt.Errorf("dns: reconcile %s: %w", audience, err)
	}
	preview, err := PreviewZone(zone, audience, observed, allowDeletes)
	if err != nil {
		return Summary{}, err
	}
	if len(preview.Apply) > 0 {
		if err := provider.ApplyChanges(ctx, zone.Name, creds, preview.Apply); err != nil {
			return Summary{}, fmt.Errorf("dns: reconcile %s: %w", audience, err)
		}
	}
	return Summary{Audience: audience, Applied: preview.Apply, Held: preview.Held}, nil
}

// EffectiveRewrites bridges an audience's address records to local-DNS
// answers: A/AAAA values become rewrites for the FQDN ("@" at apex).
// CNAME and TXT have no rewrite equivalent — rewrites answer IPs only —
// so they stay out, and the proxy (S083) plus provider records serve
// those names.
func EffectiveRewrites(zoneName string, records []Record) ([]Rewrite, error) {
	if err := validateDNSName("zone.name", zoneName, false); err != nil {
		return nil, err
	}
	var out []Rewrite
	for i := range records {
		if records[i].Type != RecordA && records[i].Type != RecordAAAA {
			continue
		}
		rewrite := Rewrite{Domain: fqdn(zoneName, records[i].Name), Answer: records[i].Value}
		if err := rewrite.Validate(); err != nil {
			return nil, fmt.Errorf("record #%d: %w", i, err)
		}
		out = append(out, rewrite)
	}
	return out, nil
}

package jellyfin

import (
	"context"
	"fmt"
	"net/netip"

	"homestead/internal/dns"
	"homestead/internal/docker"
)

// LocalRewrite builds the LAN answer for a media hostname: validated
// domain to validated IP, ready for the local provider. The rewrite
// carries no exposure opinions — callers decide local-only versus
// split horizons, this unit only spells the answer correctly.
func LocalRewrite(host docker.Hostname, answer netip.Addr) (dns.Rewrite, error) {
	rewrite := dns.Rewrite{Domain: string(host), Answer: answer.String()}
	if err := rewrite.Validate(); err != nil {
		return dns.Rewrite{}, fmt.Errorf("jellyfin: local rewrite: %w", err)
	}
	return rewrite, nil
}

// ApplyLocalName answers the media hostname locally through the
// provider adapter: idempotent at the server for identical entries,
// so reruns converge instead of duplicating.
func ApplyLocalName(ctx context.Context, client *dns.Client, rewrite dns.Rewrite) error {
	if client == nil {
		return fmt.Errorf("jellyfin: DNS client must not be nil")
	}
	if err := client.AddRewrite(ctx, rewrite); err != nil {
		return fmt.Errorf("jellyfin: apply local name: %w", err)
	}
	return nil
}

// VerifyLocalName proves the media hostname resolves to its answer
// through the given lookup: the name serves locally before anything
// downstream trusts it.
func VerifyLocalName(ctx context.Context, lookup docker.LookupFunc, host docker.Hostname, answer netip.Addr) error {
	if lookup == nil {
		return fmt.Errorf("jellyfin: lookup must not be nil")
	}
	got, err := lookup(ctx, string(host))
	if err != nil {
		return fmt.Errorf("jellyfin: resolve %s: %w", host, err)
	}
	for _, addr := range got {
		if addr == answer {
			return nil
		}
	}
	return fmt.Errorf("jellyfin: %s resolves to %v, want %s", host, got, answer)
}

// PublicRecord plans the split-horizon counterpart: the public A
// record for a media hostname under the zone origin, validated and
// unapplied. Application belongs to exposure flows (S166 applies
// through reconciliation); planning here keeps intent reviewable
// without touching provider state.
func PublicRecord(zone string, host docker.Hostname, publicIP netip.Addr) (dns.Record, error) {
	name := string(host)
	var relative string
	switch {
	case name == zone:
		relative = "@"
	case len(name) > len(zone)+1 && name[len(name)-len(zone)-1:] == "."+zone:
		relative = name[:len(name)-len(zone)-1]
	default:
		return dns.Record{}, fmt.Errorf("jellyfin: %q is outside zone %q", host, zone)
	}
	record := dns.Record{Name: relative, Type: dns.RecordA, Value: publicIP.String()}
	if publicIP.Is6() {
		record.Type = dns.RecordAAAA
	}
	if err := record.Validate(); err != nil {
		return dns.Record{}, fmt.Errorf("jellyfin: public record: %w", err)
	}
	return record, nil
}

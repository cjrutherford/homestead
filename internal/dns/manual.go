package dns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Manual is the human-operated public DNS provider: Homestead generates
// the exact records and verifies propagation, the owner creates them at
// their registrar or DNS host. It carries no API surface, so the API
// methods return ErrNoAPI and capabilities stay empty.
type Manual struct{}

// Name identifies the manual provider.
func (m Manual) Name() string { return "manual" }

// Capabilities reports no API automation.
func (m Manual) Capabilities() []Capability { return nil }

// RequiredCredentials reports no secrets: nothing authenticates.
func (m Manual) RequiredCredentials() []CredentialField { return nil }

// ListRecords has no API to read from.
func (m Manual) ListRecords(_ context.Context, _ string, _ Credentials) ([]Record, error) {
	return nil, ErrNoAPI
}

// ApplyChanges has no API to write through; the owner applies the
// rendered zone file by hand.
func (m Manual) ApplyChanges(_ context.Context, _ string, _ Credentials, _ []Change) error {
	return ErrNoAPI
}

// defaultManualTTL fills TTL-less records in rendered output. The value
// is a rendering default only; providers apply their own defaults for
// TTL 0 at serve time.
const defaultManualTTL = 300

// PublicRecords selects the records the owner must create: the zone's
// public view. Baseline answers never leak into public instructions —
// LAN addresses in public DNS are a misconfiguration this slice refuses
// to generate, and zones without an explicit public view fail instead of
// guessing.
func (m Manual) PublicRecords(zone Zone) ([]Record, error) {
	if err := zone.Validate(); err != nil {
		return nil, err
	}
	for _, view := range zone.Views {
		if view.Audience == AudiencePublic {
			return append([]Record(nil), view.Records...), nil
		}
	}
	return nil, fmt.Errorf("dns: zone %q has no public view", zone.Name)
}

// RenderZoneFile renders exact master-file lines the owner creates at
// their DNS provider: $ORIGIN plus name/TTL/class/type/value rows,
// deterministic with no timestamps. CNAME targets render absolute
// (trailing dot) so bare names never silently gain the origin; TXT
// values render quoted.
func RenderZoneFile(zoneName string, records []Record) (string, error) {
	if err := validateDNSName("zone.name", zoneName, false); err != nil {
		return "", err
	}
	sorted := append([]Record(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		if sorted[i].Type != sorted[j].Type {
			return sorted[i].Type < sorted[j].Type
		}
		return sorted[i].Value < sorted[j].Value
	})
	var body strings.Builder
	fmt.Fprintf(&body, "; Homestead manual DNS for %s (S092).\n", zoneName)
	body.WriteString("; Create these records exactly at your DNS provider.\n")
	fmt.Fprintf(&body, "$ORIGIN %s.\n", zoneName)
	for i := range sorted {
		if err := sorted[i].Validate(); err != nil {
			return "", err
		}
		ttl := sorted[i].TTLSeconds
		if ttl == 0 {
			ttl = defaultManualTTL
		}
		value := sorted[i].Value
		switch sorted[i].Type {
		case RecordCNAME:
			value += "."
		case RecordTXT:
			value = `"` + value + `"`
		}
		fmt.Fprintf(&body, "%s %d IN %s %s\n", sorted[i].Name, ttl, sorted[i].Type, value)
	}
	return body.String(), nil
}

// ProbeFunc answers one record lookup through public DNS. Production
// uses ProbeVia; tests inject fakes.
type ProbeFunc func(ctx context.Context, name string, typ RecordType) ([]string, error)

// ProbeVia returns a ProbeFunc dialing addr (host:port of a public
// resolver) for every query, so propagation checks never depend on host
// resolver configuration.
func ProbeVia(addr string) ProbeFunc {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return func(ctx context.Context, name string, typ RecordType) ([]string, error) {
		switch typ {
		case RecordA, RecordAAAA:
			ips, err := resolver.LookupIP(ctx, "ip", name)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(ips))
			for _, ip := range ips {
				out = append(out, ip.String())
			}
			return out, nil
		case RecordCNAME:
			target, err := resolver.LookupCNAME(ctx, name)
			if err != nil {
				return nil, err
			}
			return []string{target}, nil
		case RecordTXT:
			return resolver.LookupTXT(ctx, name)
		default:
			return nil, fmt.Errorf("dns: cannot probe type %q", typ)
		}
	}
}

// fqdn expands a zone-relative name: "@" is the apex, anything else
// hangs under the zone origin.
func fqdn(zoneName, name string) string {
	if name == "@" {
		return zoneName
	}
	return name + "." + zoneName
}

// sameValue compares a desired value against a probed answer with
// per-type normalization: IPs by parse (so ::1 equals its expanded
// forms), hostnames case-insensitively ignoring trailing dots, TXT
// exactly.
func sameValue(typ RecordType, want, got string) bool {
	switch typ {
	case RecordA, RecordAAAA:
		wantAddr, err := netip.ParseAddr(want)
		if err != nil {
			return false
		}
		gotAddr, err := netip.ParseAddr(got)
		if err != nil {
			return false
		}
		return wantAddr == gotAddr
	case RecordCNAME:
		return strings.EqualFold(strings.TrimSuffix(want, "."), strings.TrimSuffix(got, "."))
	default:
		return want == got
	}
}

// VerifyRecords proves propagation: every desired record resolves
// through probe with its value present. TTLs are ignored (caching makes
// them unobservable). Gaps aggregate into one error so one run tells the
// owner everything still missing.
func VerifyRecords(ctx context.Context, probe ProbeFunc, zoneName string, desired []Record) error {
	if err := validateDNSName("zone.name", zoneName, false); err != nil {
		return err
	}
	if probe == nil {
		return fmt.Errorf("dns: probe must not be nil")
	}
	var missing []string
	for i := range desired {
		if err := desired[i].Validate(); err != nil {
			return fmt.Errorf("record #%d: %w", i, err)
		}
		answers, err := probe(ctx, fqdn(zoneName, desired[i].Name), desired[i].Type)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s %s (lookup failed: %v)", desired[i].Name, desired[i].Type, err))
			continue
		}
		found := false
		for _, answer := range answers {
			if sameValue(desired[i].Type, desired[i].Value, answer) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf("%s %s %s (got %q)", desired[i].Name, desired[i].Type, desired[i].Value, strings.Join(answers, " ")))
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("dns: %d record(s) not propagated:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	return nil
}

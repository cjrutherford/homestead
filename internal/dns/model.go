package dns

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Audience names who sees an answer. It mirrors firewall.Zone and
// docker.Exposure in words (ADR-006, ADR-007) so DNS views, route
// exposure, and filter intent stay in one language. The type lives here
// rather than importing either package: internal/dns must stay
// importable without the proxy or firewall (internal/docker already
// imports this package).
type Audience string

const (
	// AudienceLocal answers LAN origins behind the gateway.
	AudienceLocal Audience = "local"
	// AudienceOverlay answers private overlay origins.
	AudienceOverlay Audience = "overlay"
	// AudiencePublic answers internet origins past the gateway.
	AudiencePublic Audience = "public"
)

// Valid reports a known audience.
func (a Audience) Valid() bool {
	return a == AudienceLocal || a == AudienceOverlay || a == AudiencePublic
}

// Record types the intent model covers: LAN answers plus the ACME
// DNS-01 challenge path. More types (MX, SRV, TLSA) arrive when a slice
// needs them, never speculatively.
type RecordType string

const (
	// RecordA maps a name to an IPv4 address.
	RecordA RecordType = "A"
	// RecordAAAA maps a name to an IPv6 address.
	RecordAAAA RecordType = "AAAA"
	// RecordCNAME aliases a name to another hostname.
	RecordCNAME RecordType = "CNAME"
	// RecordTXT attaches text, including ACME challenge values.
	RecordTXT RecordType = "TXT"
)

// Valid reports a covered record type.
func (t RecordType) Valid() bool {
	return t == RecordA || t == RecordAAAA || t == RecordCNAME || t == RecordTXT
}

var (
	validZoneLabel   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	validRecordLabel = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
)

// validateDNSName checks canonical shape: lowercase, no empty labels.
// Record labels additionally allow underscores for challenge and service
// names (_acme-challenge); zone origins do not take them.
func validateDNSName(field, name string, record bool) error {
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("%s: must be 1-253 characters", field)
	}
	if name == "@" {
		if record {
			return nil
		}
		return fmt.Errorf("%s: zone origin must be a name", field)
	}
	if strings.ToLower(name) != name {
		return fmt.Errorf("%s: must be lowercase", field)
	}
	if strings.HasSuffix(name, ".") {
		return fmt.Errorf("%s: must be relative to the zone, no trailing dot", field)
	}
	if strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return fmt.Errorf("%s: must not have empty labels", field)
	}
	pattern := validZoneLabel
	if record {
		pattern = validRecordLabel
	}
	for _, label := range strings.Split(name, ".") {
		if !pattern.MatchString(label) {
			return fmt.Errorf("%s: invalid label %q", field, label)
		}
	}
	return nil
}

// Record is one desired DNS answer: name relative to the zone ("@" for
// the apex), type, value, and TTL. TTLSeconds 0 means the provider
// default; otherwise 60-86400.
type Record struct {
	Name       string
	Type       RecordType
	Value      string
	TTLSeconds int
}

// Validate checks a record end to end, including per-type values.
func (r Record) Validate() error {
	if err := validateDNSName("record.name", r.Name, true); err != nil {
		return err
	}
	if !r.Type.Valid() {
		return fmt.Errorf("record.type: must be A, AAAA, CNAME, or TXT")
	}
	switch r.Type {
	case RecordA:
		addr, err := netip.ParseAddr(r.Value)
		if err != nil || !addr.Is4() {
			return fmt.Errorf("record.value: A must be an IPv4 address")
		}
	case RecordAAAA:
		addr, err := netip.ParseAddr(r.Value)
		if err != nil || !addr.Is6() {
			return fmt.Errorf("record.value: AAAA must be an IPv6 address")
		}
	case RecordCNAME:
		if err := validateDNSName("record.value", r.Value, false); err != nil {
			return err
		}
	case RecordTXT:
		if r.Value == "" || len(r.Value) > 255 {
			return fmt.Errorf("record.value: TXT must be 1-255 characters")
		}
	}
	if r.TTLSeconds != 0 && (r.TTLSeconds < 60 || r.TTLSeconds > 86400) {
		return fmt.Errorf("record.ttl: must be 0 or 60-86400 seconds")
	}
	return nil
}

// validateRecordSet checks each record, rejects duplicate name/type/value
// triples (repeated A answers for round-robin stay legal), and enforces
// CNAME exclusivity: a name with a CNAME holds nothing else.
func validateRecordSet(field string, records []Record) error {
	seen := map[Record]bool{}
	cnames := map[string]bool{}
	names := map[string]bool{}
	for i := range records {
		if err := records[i].Validate(); err != nil {
			return fmt.Errorf("%s #%d: %w", field, i, err)
		}
		if seen[records[i]] {
			return fmt.Errorf("%s #%d: duplicate record", field, i)
		}
		seen[records[i]] = true
		if records[i].Type == RecordCNAME {
			cnames[records[i].Name] = true
		} else {
			names[records[i].Name] = true
		}
	}
	for name := range cnames {
		if names[name] {
			return fmt.Errorf("%s: CNAME at %q excludes other records", field, name)
		}
	}
	return nil
}

// View is the answer set one audience sees (ADR-007): LAN, overlay, and
// public views of the same names may differ. Each view validates
// self-consistent; merging baseline with views belongs to split-DNS
// reconciliation (S098), not the model.
type View struct {
	Name     string
	Audience Audience
	Records  []Record
}

// Validate checks a view end to end.
func (v View) Validate() error {
	if v.Name == "" || len(v.Name) > 64 {
		return fmt.Errorf("view.name: must be 1-64 characters")
	}
	if !v.Audience.Valid() {
		return fmt.Errorf("view.audience: must be local, overlay, or public")
	}
	if err := validateRecordSet("view.records", v.Records); err != nil {
		return err
	}
	return nil
}

// Zone is the desired state of one DNS origin: baseline answers plus
// per-audience views. Providers (S091+) render from this shape; none may
// demand more than it carries.
type Zone struct {
	Name    string
	Records []Record
	Views   []View
}

// Validate checks the zone end to end: origin shape, baseline records,
// and distinct audiences across views (one view per audience, mirroring
// the firewall ServicePolicy distinct-sources rule).
func (z Zone) Validate() error {
	if err := validateDNSName("zone.name", z.Name, false); err != nil {
		return err
	}
	if err := validateRecordSet("zone.records", z.Records); err != nil {
		return err
	}
	seen := map[Audience]bool{}
	for i := range z.Views {
		if err := z.Views[i].Validate(); err != nil {
			return fmt.Errorf("view #%d: %w", i, err)
		}
		if seen[z.Views[i].Audience] {
			return fmt.Errorf("duplicate view audience %q", z.Views[i].Audience)
		}
		seen[z.Views[i].Audience] = true
	}
	return nil
}

// Sorted returns baseline records ordered by name, type, then value for
// deterministic rendering.
func (z Zone) Sorted() []Record {
	out := append([]Record(nil), z.Records...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	return out
}

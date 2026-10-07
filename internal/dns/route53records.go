package dns

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// defaultRoute53TTL fills TTL-less records in change batches. Route 53
// accepts no "automatic" TTL, so intent default becomes explicit here.
const defaultRoute53TTL = 300

// route53HostedZone is the zone-list result subset.
type route53HostedZone struct {
	ID          string `xml:"Id"`
	Name        string `xml:"Name"`
	PrivateZone bool   `xml:"Config>PrivateZone"`
}

type route53ZoneList struct {
	XMLName     xml.Name            `xml:"ListHostedZonesResponse"`
	HostedZones []route53HostedZone `xml:"HostedZones>HostedZone"`
	IsTruncated bool                `xml:"IsTruncated"`
	NextMarker  string              `xml:"NextMarker"`
}

// route53RRSet is a record-set result: one name/type with its values.
type route53RRSet struct {
	Name            string   `xml:"Name"`
	Type            string   `xml:"Type"`
	SetIdentifier   string   `xml:"SetIdentifier"`
	TTL             int      `xml:"TTL"`
	ResourceRecords []string `xml:"ResourceRecords>ResourceRecord>Value"`
}

type route53RRSetList struct {
	XMLName              xml.Name       `xml:"ListResourceRecordSetsResponse"`
	ResourceRecordSets   []route53RRSet `xml:"ResourceRecordSets>ResourceRecordSet"`
	IsTruncated          bool           `xml:"IsTruncated"`
	NextRecordName       string         `xml:"NextRecordName"`
	NextRecordType       string         `xml:"NextRecordType"`
	NextRecordIdentifier string         `xml:"NextRecordIdentifier"`
}

// zoneID resolves a zone origin to its hosted-zone identifier, following
// zone-list pagination. Same-name public and private zones disambiguate
// toward public: several public zones, or only private ones, are errors
// rather than silent guesses at where public records would land.
func (r *Route53) zoneID(ctx context.Context, creds Credentials, zone string) (string, error) {
	if err := validateDNSName("zone.name", zone, false); err != nil {
		return "", err
	}
	want := zone + "."
	var public, private []string
	marker := ""
	for {
		query := url.Values{"maxitems": {"100"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		raw, err := r.route53call(ctx, creds, "GET", "/hostedzone", query, nil)
		if err != nil {
			return "", err
		}
		var list route53ZoneList
		if err := xml.Unmarshal(raw, &list); err != nil {
			return "", fmt.Errorf("dns: route53 decode zones: %w", err)
		}
		for _, z := range list.HostedZones {
			if z.Name != want {
				continue
			}
			if z.PrivateZone {
				private = append(private, z.ID)
			} else {
				public = append(public, z.ID)
			}
		}
		if !list.IsTruncated {
			break
		}
		marker = list.NextMarker
	}
	switch {
	case len(public) == 1:
		return public[0], nil
	case len(public) > 1:
		return "", fmt.Errorf("dns: route53 zone %q matched %d public zones", zone, len(public))
	case len(private) > 0:
		return "", fmt.Errorf("dns: route53 zone %q exists only as private", zone)
	default:
		return "", fmt.Errorf("dns: route53 zone %q not found", zone)
	}
}

// unquoteTXT joins Route 53's quoted TXT chunks into the intent value:
// `"token"` and `"a""b"` become token and ab.
func unquoteTXT(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) || len(value) < 2 {
		return value
	}
	return strings.ReplaceAll(value[1:len(value)-1], `""`, "")
}

// quoteTXT renders an intent value as one quoted chunk.
func quoteTXT(value string) string {
	return `"` + value + `"`
}

// rrsetKey groups values into Route 53 record sets.
type rrsetKey struct {
	name string
	typ  RecordType
}

// expandRRSet maps one record set to intent records: FQDN names to
// zone-relative ("@" at apex), TXT unquoted, foreign types and
// non-simple (identifier/alias/empty) sets skipped. Invalid members drop
// individually so one odd value never hides a whole set.
func expandRRSet(zone string, set route53RRSet) []Record {
	typ := RecordType(set.Type)
	if !typ.Valid() || set.SetIdentifier != "" || len(set.ResourceRecords) == 0 {
		return nil
	}
	name := strings.TrimSuffix(set.Name, ".")
	if name == zone {
		name = "@"
	} else if strings.HasSuffix(name, "."+zone) {
		name = strings.TrimSuffix(name, "."+zone)
	} else {
		return nil
	}
	var out []Record
	for _, value := range set.ResourceRecords {
		if typ == RecordTXT {
			value = unquoteTXT(value)
		}
		intent := Record{Name: name, Type: typ, Value: value, TTLSeconds: set.TTL}
		if err := intent.Validate(); err != nil {
			continue
		}
		out = append(out, intent)
	}
	return out
}

// listRRSets returns every record set in a hosted zone, following record
// pagination.
func (r *Route53) listRRSets(ctx context.Context, creds Credentials, zoneID string) ([]route53RRSet, error) {
	var out []route53RRSet
	var name, typ, identifier string
	for {
		query := url.Values{"maxitems": {"100"}}
		if name != "" {
			query.Set("name", name)
			query.Set("type", typ)
			query.Set("identifier", identifier)
		}
		raw, err := r.route53call(ctx, creds, "GET", "/hostedzone/"+strings.TrimPrefix(zoneID, "/hostedzone/")+"/rrset", query, nil)
		if err != nil {
			return nil, err
		}
		var list route53RRSetList
		if err := xml.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("dns: route53 decode records: %w", err)
		}
		out = append(out, list.ResourceRecordSets...)
		if !list.IsTruncated {
			break
		}
		name, typ, identifier = list.NextRecordName, list.NextRecordType, list.NextRecordIdentifier
	}
	return out, nil
}

// ListRecords returns the zone's covered-type records normalized to
// intent, expanding multi-value sets into per-value records.
func (r *Route53) ListRecords(ctx context.Context, zone string, creds Credentials) ([]Record, error) {
	id, err := r.zoneID(ctx, creds, zone)
	if err != nil {
		return nil, err
	}
	sets, err := r.listRRSets(ctx, creds, id)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, set := range sets {
		out = append(out, expandRRSet(zone, set)...)
	}
	return out, nil
}

// route53Change is one batch element, rendered by hand below.
type route53Change struct {
	Action string
	Name   string
	Type   string
	TTL    int
	Values []string
}

// ApplyChanges reconciles record sets in one transactional ChangeBatch:
// per-key final values compute from a fresh listing (actual minus
// deletes plus creates), unchanged sets skip, emptied sets delete, and
// the batch posts atomically — all-or-none, so failures retry cleanly.
// TTL prefers created values, then actual, then the explicit default.
func (r *Route53) ApplyChanges(ctx context.Context, zone string, creds Credentials, changes []Change) error {
	id, err := r.zoneID(ctx, creds, zone)
	if err != nil {
		return err
	}
	sets, err := r.listRRSets(ctx, creds, id)
	if err != nil {
		return err
	}
	type setState struct {
		ttl    int
		values map[string]bool
	}
	actual := map[rrsetKey]*setState{}
	for _, set := range sets {
		typ := RecordType(set.Type)
		if !typ.Valid() || set.SetIdentifier != "" || len(set.ResourceRecords) == 0 {
			continue
		}
		name := strings.TrimSuffix(set.Name, ".")
		if name == zone {
			name = "@"
		} else if strings.HasSuffix(name, "."+zone) {
			name = strings.TrimSuffix(name, "."+zone)
		} else {
			continue
		}
		key := rrsetKey{name: name, typ: typ}
		state, ok := actual[key]
		if !ok {
			state = &setState{ttl: set.TTL, values: map[string]bool{}}
			actual[key] = state
		}
		for _, value := range set.ResourceRecords {
			if typ == RecordTXT {
				value = unquoteTXT(value)
			}
			state.values[value] = true
		}
	}
	creates := map[rrsetKey]map[string]Record{}
	deletes := map[rrsetKey]map[string]bool{}
	touched := map[rrsetKey]bool{}
	for i := range changes {
		rec := changes[i].Record
		if err := rec.Validate(); err != nil {
			return fmt.Errorf("change #%d: %w", i, err)
		}
		key := rrsetKey{name: rec.Name, typ: rec.Type}
		touched[key] = true
		switch changes[i].Action {
		case ChangeCreate:
			if creates[key] == nil {
				creates[key] = map[string]Record{}
			}
			creates[key][rec.Value] = rec
		case ChangeDelete:
			if deletes[key] == nil {
				deletes[key] = map[string]bool{}
			}
			deletes[key][rec.Value] = true
		default:
			return fmt.Errorf("change #%d: unknown action %q", i, changes[i].Action)
		}
	}
	var batch []route53Change
	for key := range touched {
		state := actual[key]
		final := map[string]bool{}
		if state != nil {
			for value := range state.values {
				final[value] = true
			}
		}
		for value := range deletes[key] {
			delete(final, value)
		}
		for value := range creates[key] {
			final[value] = true
		}
		before := map[string]bool{}
		if state != nil {
			before = state.values
		}
		if equalSets(before, final) {
			continue
		}
		if len(before) > 0 {
			batch = append(batch, route53Change{
				Action: "DELETE", Name: fqdn(zone, key.name) + ".",
				Type: string(key.typ), TTL: state.ttl, Values: rawValues(key.typ, before),
			})
		}
		if len(final) > 0 {
			ttl := 0
			for _, rec := range creates[key] {
				if rec.TTLSeconds != 0 {
					ttl = rec.TTLSeconds
					break
				}
			}
			if ttl == 0 && state != nil {
				ttl = state.ttl
			}
			if ttl == 0 {
				ttl = defaultRoute53TTL
			}
			var raw []string
			for _, value := range sortedKeys(final) {
				if key.typ == RecordTXT {
					value = quoteTXT(value)
				}
				raw = append(raw, value)
			}
			batch = append(batch, route53Change{
				Action: "CREATE", Name: fqdn(zone, key.name) + ".",
				Type: string(key.typ), TTL: ttl, Values: raw,
			})
		}
	}
	if len(batch) == 0 {
		return nil
	}
	var body strings.Builder
	body.WriteString(`<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ChangeBatch><Changes>`)
	for _, change := range batch {
		var element strings.Builder
		element.WriteString("<Change><Action>" + change.Action + "</Action><ResourceRecordSet>")
		element.WriteString("<Name>" + xmlEscape(change.Name) + "</Name>")
		element.WriteString("<Type>" + change.Type + "</Type>")
		element.WriteString("<TTL>" + strconv.Itoa(change.TTL) + "</TTL><ResourceRecords>")
		for _, value := range change.Values {
			element.WriteString("<ResourceRecord><Value>" + xmlEscape(value) + "</Value></ResourceRecord>")
		}
		element.WriteString("</ResourceRecords></ResourceRecordSet></Change>")
		body.WriteString(element.String())
	}
	body.WriteString("</Changes></ChangeBatch></ChangeResourceRecordSetsRequest>")
	_, err = r.route53call(ctx, creds, "POST", "/hostedzone/"+strings.TrimPrefix(id, "/hostedzone/")+"/rrset", nil, []byte(body.String()))
	if err != nil {
		return err
	}
	return nil
}

// equalSets reports identical string sets.
func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for value := range a {
		if !b[value] {
			return false
		}
	}
	return true
}

// sortedKeys orders set members deterministically for batches.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// rawValues renders intent values back to wire form for deletes.
func rawValues(typ RecordType, values map[string]bool) []string {
	raw := make([]string, 0, len(values))
	for _, value := range sortedKeys(values) {
		if typ == RecordTXT {
			value = quoteTXT(value)
		}
		raw = append(raw, value)
	}
	return raw
}

// xmlEscape escapes text for the hand-built batch document.
func xmlEscape(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(text)
}

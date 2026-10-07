package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DigitalOceanAPI is the production API origin. Tests override it.
const DigitalOceanAPI = "https://api.digitalocean.com/v2"

// digitaloceanTokenField is the sole credential: a personal access token
// with read/write scope.
const digitaloceanTokenField = "api_token"

// DigitalOcean is the S091 provider for DigitalOcean-managed public
// zones: record management plus ACME DNS-01 through the v2 JSON API with
// a bearer token. Only intent-covered types (A/AAAA/CNAME/TXT) map;
// foreign types stay unmanaged and never surface as observed state.
// Record names arrive zone-relative ("@" at apex), matching intent
// directly; CNAME targets normalize trailing dots. TTL 0 (provider
// default) omits the field on create so the API default applies.
type DigitalOcean struct {
	base *url.URL
	http *http.Client
}

// NewDigitalOcean builds the provider against baseURL (DigitalOceanAPI
// in production, httptest doubles in tests).
func NewDigitalOcean(baseURL string) (*DigitalOcean, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("dns: digitalocean base URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("dns: digitalocean base URL must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("dns: digitalocean base URL must have a host")
	}
	return &DigitalOcean{base: base, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Name identifies the DigitalOcean provider.
func (d *DigitalOcean) Name() string { return "digitalocean" }

// Capabilities reports record management and DNS-01.
func (d *DigitalOcean) Capabilities() []Capability {
	return []Capability{CapabilityRecords, CapabilityDNS01}
}

// RequiredCredentials reports the API token field.
func (d *DigitalOcean) RequiredCredentials() []CredentialField {
	return []CredentialField{{Name: digitaloceanTokenField, Label: "DigitalOcean API token", Secret: true}}
}

// digitaloceanDomain is the domain-list result subset.
type digitaloceanDomain struct {
	Name string `json:"name"`
}

// digitaloceanRecord is the record result subset.
type digitaloceanRecord struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
	TTL  int    `json:"ttl"`
}

// digitaloceanPages follows v2 pagination: links.pages.next while present.
type digitaloceanPages struct {
	Links struct {
		Pages struct {
			Next string `json:"next"`
		} `json:"pages"`
	} `json:"links"`
}

// digitaloceanError carries the v2 failure message.
type digitaloceanError struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// call performs one authenticated v2 call, decoding resultKey from the
// response object into out (each endpoint carries exactly one data
// key). A nil out skips decoding for empty responses such as 204
// deletes. Failures report the API message when present.
func (d *DigitalOcean) call(ctx context.Context, token, method, path string, query url.Values, body any, resultKey string, out any) (string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("dns: digitalocean encode: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	target := *d.base
	target.Path = strings.TrimSuffix(d.base.Path, "/") + path
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return "", fmt.Errorf("dns: digitalocean request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("dns: digitalocean %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("dns: digitalocean read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message := strings.TrimSpace(string(raw))
		var apiErr digitaloceanError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Message != "" {
			message = apiErr.Message
		}
		return "", fmt.Errorf("dns: digitalocean %s %s: status %s: %s", method, path, resp.Status, message)
	}
	var envelope map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return "", fmt.Errorf("dns: digitalocean decode: %w", err)
		}
	}
	if out != nil {
		rawResult, ok := envelope[resultKey]
		if !ok {
			return "", fmt.Errorf("dns: digitalocean %s %s: missing %q", method, path, resultKey)
		}
		if err := json.Unmarshal(rawResult, out); err != nil {
			return "", fmt.Errorf("dns: digitalocean decode result: %w", err)
		}
	}
	var pages digitaloceanPages
	_ = json.Unmarshal(raw, &pages)
	return pages.Links.Pages.Next, nil
}

// splitNext resolves a v2 next-page URL back into a local path plus
// query for the following call.
func splitNext(next string) (string, url.Values, error) {
	u, err := url.Parse(next)
	if err != nil {
		return "", nil, fmt.Errorf("dns: digitalocean next page: %w", err)
	}
	return strings.TrimPrefix(u.Path, "/v2"), u.Query(), nil
}

// domainPath escapes a zone origin for URL paths.
func domainPath(zone string) string {
	return "/domains/" + url.PathEscape(zone)
}

// checkDomain confirms the zone exists under this account: exactly one
// name match. DigitalOcean names are account-unique, so absence is an
// error rather than a guess.
func (d *DigitalOcean) checkDomain(ctx context.Context, token, zone string) error {
	if err := validateDNSName("zone.name", zone, false); err != nil {
		return err
	}
	next := ""
	for {
		var domains []digitaloceanDomain
		path, query := "/domains", url.Values{}
		if next != "" {
			var err error
			path, query, err = splitNext(next)
			if err != nil {
				return err
			}
		}
		follow, err := d.call(ctx, token, http.MethodGet, path, query, nil, "domains", &domains)
		if err != nil {
			return err
		}
		for _, domain := range domains {
			if domain.Name == zone {
				return nil
			}
		}
		if follow == "" {
			break
		}
		next = follow
	}
	return fmt.Errorf("dns: digitalocean zone %q not found", zone)
}

// toIntent maps a DigitalOcean record to the intent shape: names arrive
// relative already, CNAME targets trim trailing dots.
func toDigitalIntent(r digitaloceanRecord) (Record, bool) {
	typ := RecordType(r.Type)
	if !typ.Valid() {
		return Record{}, false
	}
	value := r.Data
	if typ == RecordCNAME {
		value = strings.TrimSuffix(value, ".")
	}
	intent := Record{Name: r.Name, Type: typ, Value: value, TTLSeconds: r.TTL}
	if err := intent.Validate(); err != nil {
		return Record{}, false
	}
	return intent, true
}

// listIndexed returns covered-type records plus an intent-to-ID index
// for deletes, following pagination to the last page.
func (d *DigitalOcean) listIndexed(ctx context.Context, token, zone string) ([]Record, map[Record]int, error) {
	var out []Record
	ids := map[Record]int{}
	next := ""
	for {
		var records []digitaloceanRecord
		path := domainPath(zone) + "/records"
		query := url.Values{"per_page": {"200"}}
		if next != "" {
			var err error
			path, query, err = splitNext(next)
			if err != nil {
				return nil, nil, err
			}
		}
		follow, err := d.call(ctx, token, http.MethodGet, path, query, nil, "domain_records", &records)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range records {
			if intent, ok := toDigitalIntent(r); ok {
				out = append(out, intent)
				ids[intent] = r.ID
			}
		}
		if follow == "" {
			break
		}
		next = follow
	}
	return out, ids, nil
}

// ListRecords returns the zone's covered-type records normalized to
// intent.
func (d *DigitalOcean) ListRecords(ctx context.Context, zone string, creds Credentials) ([]Record, error) {
	token, err := creds.Get(digitaloceanTokenField)
	if err != nil {
		return nil, err
	}
	if err := d.checkDomain(ctx, token, zone); err != nil {
		return nil, err
	}
	records, _, err := d.listIndexed(ctx, token, zone)
	return records, err
}

// toDigital maps an intent record to the v2 create shape: TTL 0 omits
// the field for the API default, CNAME targets go absolute.
func toDigital(r Record) map[string]any {
	body := map[string]any{
		"type": string(r.Type),
		"name": r.Name,
		"data": r.Value,
	}
	if r.Type == RecordCNAME {
		body["data"] = r.Value + "."
	}
	if r.TTLSeconds != 0 {
		body["ttl"] = r.TTLSeconds
	}
	return body
}

// ApplyChanges carries out record changes in order: creates POST,
// deletes resolve through a fresh listing to record IDs. A delete
// target missing from the listing is an error — apply stays exact and
// retries converge.
func (d *DigitalOcean) ApplyChanges(ctx context.Context, zone string, creds Credentials, changes []Change) error {
	token, err := creds.Get(digitaloceanTokenField)
	if err != nil {
		return err
	}
	if err := d.checkDomain(ctx, token, zone); err != nil {
		return err
	}
	_, ids, err := d.listIndexed(ctx, token, zone)
	if err != nil {
		return err
	}
	for i := range changes {
		switch changes[i].Action {
		case ChangeCreate:
			if err := changes[i].Record.Validate(); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
			if _, err := d.call(ctx, token, http.MethodPost, domainPath(zone)+"/records",
				nil, toDigital(changes[i].Record), "domain_record", nil); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
		case ChangeDelete:
			id, ok := ids[changes[i].Record]
			if !ok {
				return fmt.Errorf("change #%d: delete target not found: %+v", i, changes[i].Record)
			}
			if _, err := d.call(ctx, token, http.MethodDelete,
				fmt.Sprintf("%s/records/%d", domainPath(zone), id), nil, nil, "", nil); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
		default:
			return fmt.Errorf("change #%d: unknown action %q", i, changes[i].Action)
		}
	}
	return nil
}

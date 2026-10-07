package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CloudflareAPI is the production API origin. Tests override it; the
// client never hard-codes any other origin.
const CloudflareAPI = "https://api.cloudflare.com/client/v4"

// cloudflareTokenField is the sole credential: a scoped API token.
const cloudflareTokenField = "api_token"

// Cloudflare is the S091 provider for Cloudflare-managed public zones:
// record management plus ACME DNS-01 through the v4 API with a token.
// Only intent-covered types (A/AAAA/CNAME/TXT) map; foreign types stay
// unmanaged and never surface as observed state. Proxied (CDN) records
// are a known caveat: their served content is Cloudflare's, so listing
// reports them as-is and reconciliation policy (S098) decides their fate.
type Cloudflare struct {
	base *url.URL
	http *http.Client
}

// NewCloudflare builds the provider against baseURL (CloudflareAPI in
// production, httptest doubles in tests).
func NewCloudflare(baseURL string) (*Cloudflare, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("dns: cloudflare base URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("dns: cloudflare base URL must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("dns: cloudflare base URL must have a host")
	}
	return &Cloudflare{base: base, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Name identifies the Cloudflare provider.
func (c *Cloudflare) Name() string { return "cloudflare" }

// Capabilities reports record management and DNS-01.
func (c *Cloudflare) Capabilities() []Capability {
	return []Capability{CapabilityRecords, CapabilityDNS01}
}

// RequiredCredentials reports the API token field.
func (c *Cloudflare) RequiredCredentials() []CredentialField {
	return []CredentialField{{Name: cloudflareTokenField, Label: "Cloudflare API token", Secret: true}}
}

// cloudflareEnvelope is the v4 response wrapper. Success false carries
// errors; result decodes per call.
type cloudflareEnvelope struct {
	Success    bool              `json:"success"`
	Errors     []cloudflareError `json:"errors"`
	ResultInfo *cloudflarePages  `json:"result_info"`
	Result     json.RawMessage   `json:"result"`
}

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflarePages struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

// cloudflareZone is the zone-lookup result subset.
type cloudflareZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// cloudflareRecord is the DNS-record result subset.
type cloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// call performs one authenticated v4 call, decoding result into out.
// API-level failure reports Cloudflare's error messages.
func (c *Cloudflare) call(ctx context.Context, token, method, path string, query url.Values, body, out any) (*cloudflarePages, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("dns: cloudflare encode: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + path
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("dns: cloudflare request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dns: cloudflare %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("dns: cloudflare read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("dns: cloudflare %s %s: status %s", method, path, resp.Status)
	}
	var envelope cloudflareEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("dns: cloudflare decode: %w", err)
	}
	if !envelope.Success {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		return nil, fmt.Errorf("dns: cloudflare %s %s: %s", method, path, strings.Join(msgs, "; "))
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return nil, fmt.Errorf("dns: cloudflare decode result: %w", err)
		}
	}
	return envelope.ResultInfo, nil
}

// token resolves the API token credential.
func token(creds Credentials) (string, error) {
	return creds.Get(cloudflareTokenField)
}

// zoneID resolves a zone origin to its Cloudflare identifier. Exactly
// one match is required: zero or several is an error naming the zone.
func (c *Cloudflare) zoneID(ctx context.Context, token, zone string) (string, error) {
	if err := validateDNSName("zone.name", zone, false); err != nil {
		return "", err
	}
	var zones []cloudflareZone
	_, err := c.call(ctx, token, http.MethodGet, "/zones",
		url.Values{"name": {zone}}, nil, &zones)
	if err != nil {
		return "", err
	}
	if len(zones) != 1 {
		return "", fmt.Errorf("dns: cloudflare zone %q matched %d zones", zone, len(zones))
	}
	return zones[0].ID, nil
}

// toIntent maps a Cloudflare record to the intent shape: FQDN names to
// zone-relative ("@" at apex), TTL 1 (automatic) to provider default,
// foreign types skipped (false).
func toIntent(zone string, r cloudflareRecord) (Record, bool) {
	typ := RecordType(r.Type)
	if !typ.Valid() {
		return Record{}, false
	}
	name := r.Name
	if name == zone {
		name = "@"
	} else if strings.HasSuffix(name, "."+zone) {
		name = strings.TrimSuffix(name, "."+zone)
	} else {
		return Record{}, false
	}
	content := r.Content
	if typ == RecordTXT {
		content = strings.Trim(content, `"`)
	}
	ttl := r.TTL
	if ttl == 1 {
		ttl = 0
	}
	intent := Record{Name: name, Type: typ, Value: content, TTLSeconds: ttl}
	if err := intent.Validate(); err != nil {
		return Record{}, false
	}
	return intent, true
}

// ListRecords returns the zone's covered-type records normalized to
// intent, following pagination to the last page.
func (c *Cloudflare) ListRecords(ctx context.Context, zone string, creds Credentials) ([]Record, error) {
	token, err := token(creds)
	if err != nil {
		return nil, err
	}
	id, err := c.zoneID(ctx, token, zone)
	if err != nil {
		return nil, err
	}
	var out []Record
	for page := 1; ; page++ {
		var records []cloudflareRecord
		pages, err := c.call(ctx, token, http.MethodGet, "/zones/"+id+"/dns_records",
			url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}, nil, &records)
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if intent, ok := toIntent(zone, r); ok {
				out = append(out, intent)
			}
		}
		if pages == nil || pages.Page >= pages.TotalPages || pages.TotalPages == 0 {
			break
		}
	}
	return out, nil
}

// toCloudflare maps an intent record to the v4 create/update shape:
// TTL 0 (provider default) becomes automatic, TXT values pass raw.
func toCloudflare(zone string, r Record) map[string]any {
	ttl := r.TTLSeconds
	if ttl == 0 {
		ttl = 1
	}
	return map[string]any{
		"type":    string(r.Type),
		"name":    fqdn(zone, r.Name),
		"content": r.Value,
		"ttl":     ttl,
	}
}

// ApplyChanges carries out record changes in order: creates POST, deletes
// resolve through a fresh listing to provider IDs and DELETE by ID. A
// delete target missing from the listing is an error — apply stays exact
// and retries converge.
func (c *Cloudflare) ApplyChanges(ctx context.Context, zone string, creds Credentials, changes []Change) error {
	token, err := token(creds)
	if err != nil {
		return err
	}
	id, err := c.zoneID(ctx, token, zone)
	if err != nil {
		return err
	}
	// Deletes address provider IDs, so index the current listing first.
	ids := map[Record]string{}
	for page := 1; ; page++ {
		var records []cloudflareRecord
		pages, err := c.call(ctx, token, http.MethodGet, "/zones/"+id+"/dns_records",
			url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}, nil, &records)
		if err != nil {
			return err
		}
		for _, r := range records {
			if intent, ok := toIntent(zone, r); ok {
				ids[intent] = r.ID
			}
		}
		if pages == nil || pages.Page >= pages.TotalPages || pages.TotalPages == 0 {
			break
		}
	}
	for i := range changes {
		switch changes[i].Action {
		case ChangeCreate:
			if err := changes[i].Record.Validate(); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
			if _, err := c.call(ctx, token, http.MethodPost, "/zones/"+id+"/dns_records",
				nil, toCloudflare(zone, changes[i].Record), nil); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
		case ChangeDelete:
			recordID, ok := ids[changes[i].Record]
			if !ok {
				return fmt.Errorf("change #%d: delete target not found: %+v", i, changes[i].Record)
			}
			if _, err := c.call(ctx, token, http.MethodDelete,
				"/zones/"+id+"/dns_records/"+recordID, nil, nil, nil); err != nil {
				return fmt.Errorf("change #%d: %w", i, err)
			}
		default:
			return fmt.Errorf("change #%d: unknown action %q", i, changes[i].Action)
		}
	}
	return nil
}

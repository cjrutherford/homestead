package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cloudflareDouble serves canned v4 responses, recording calls.
type cloudflareDouble struct {
	t       *testing.T
	zones   []cloudflareZone
	pages   [][]cloudflareRecord
	calls   []string
	auth    []string
	created []map[string]any
	deleted []string
	fail    map[string]int
}

func (d *cloudflareDouble) handler(w http.ResponseWriter, r *http.Request) {
	d.calls = append(d.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	d.auth = append(d.auth, r.Header.Get("Authorization"))
	write := func(success bool, result any, page, total int) {
		envelope := map[string]any{"success": success, "errors": []any{}, "result": result}
		if total > 0 {
			envelope["result_info"] = map[string]any{
				"page": page, "per_page": 100, "total_pages": total,
			}
		}
		_ = json.NewEncoder(w).Encode(envelope)
	}
	if code, ok := d.fail[r.Method+" "+r.URL.Path]; ok {
		w.WriteHeader(code)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		write(true, d.zones, 0, 0)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/dns_records"):
		page := 1
		for _, q := range r.URL.Query()["page"] {
			if q == "2" {
				page = 2
			}
		}
		idx := page - 1
		if idx >= len(d.pages) {
			write(true, []cloudflareRecord{}, page, len(d.pages))
			return
		}
		write(true, d.pages[idx], page, len(d.pages))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			d.t.Fatalf("decode: %v", err)
		}
		d.created = append(d.created, body)
		write(true, map[string]any{"id": "new-id"}, 0, 0)
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/dns_records/"):
		parts := strings.Split(r.URL.Path, "/")
		d.deleted = append(d.deleted, parts[len(parts)-1])
		write(true, map[string]any{"id": parts[len(parts)-1]}, 0, 0)
	default:
		d.t.Fatalf("unexpected call %s %s", r.Method, r.URL.Path)
	}
}

func serveCloudflare(t *testing.T, d *cloudflareDouble) *Cloudflare {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	c, err := NewCloudflare(srv.URL)
	if err != nil {
		t.Fatalf("NewCloudflare: %v", err)
	}
	return c
}

func cfCreds() Credentials { return Credentials{"api_token": "token"} }

func populatedDouble() *cloudflareDouble {
	return &cloudflareDouble{
		zones: []cloudflareZone{{ID: "zone-id", Name: "example.com"}},
		pages: [][]cloudflareRecord{
			{
				{ID: "a1", Type: "A", Name: "example.com", Content: "203.0.113.7", TTL: 300},
				{ID: "w1", Type: "CNAME", Name: "www.example.com", Content: "example.com", TTL: 1},
			},
			{
				{ID: "t1", Type: "TXT", Name: "_acme-challenge.example.com", Content: `"token"`, TTL: 120},
				{ID: "m1", Type: "MX", Name: "example.com", Content: "mail.example.com", TTL: 300},
				{ID: "x1", Type: "A", Name: "other.example.net", Content: "10.0.0.1", TTL: 300},
			},
		},
	}
}

func TestCloudflareContract(t *testing.T) {
	var _ Provider = (*Cloudflare)(nil)
	c, err := NewCloudflare(CloudflareAPI)
	if err != nil {
		t.Fatalf("NewCloudflare: %v", err)
	}
	if c.Name() != "cloudflare" {
		t.Fatalf("name = %q", c.Name())
	}
	caps := c.Capabilities()
	if len(caps) != 2 || caps[0] != CapabilityRecords || caps[1] != CapabilityDNS01 {
		t.Fatalf("caps = %v", caps)
	}
	creds := c.RequiredCredentials()
	if len(creds) != 1 || creds[0].Name != "api_token" || !creds[0].Secret {
		t.Fatalf("creds = %+v", creds)
	}
	if err := ValidateProvider(c); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	if _, err := NewCloudflare("ftp://x/"); err == nil {
		t.Fatal("accepted ftp base")
	}
}

func TestCloudflareList(t *testing.T) {
	d := populatedDouble()
	c := serveCloudflare(t, d)
	got, err := c.ListRecords(context.Background(), "example.com", cfCreds())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	want := []Record{
		{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		{Name: "www", Type: RecordCNAME, Value: "example.com"},
		{Name: "_acme-challenge", Type: RecordTXT, Value: "token", TTLSeconds: 120},
	}
	if len(got) != len(want) {
		t.Fatalf("records = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if d.auth[0] != "Bearer token" {
		t.Fatalf("auth = %v", d.auth)
	}
	// Missing token fails before any call.
	if _, err := c.ListRecords(context.Background(), "example.com", nil); err == nil {
		t.Fatal("listed without token")
	}
	// Unknown zones fail.
	empty := &cloudflareDouble{zones: []cloudflareZone{}}
	c2 := serveCloudflare(t, empty)
	if _, err := c2.ListRecords(context.Background(), "example.com", cfCreds()); err == nil ||
		!strings.Contains(err.Error(), "matched 0 zones") {
		t.Fatalf("err = %v", err)
	}
}

func TestCloudflareApply(t *testing.T) {
	d := populatedDouble()
	c := serveCloudflare(t, d)
	changes := []Change{
		{Action: ChangeCreate, Record: Record{Name: "new", Type: RecordA, Value: "203.0.113.8", TTLSeconds: 300}},
		{Action: ChangeCreate, Record: Record{Name: "auto", Type: RecordA, Value: "203.0.113.9"}},
		{Action: ChangeDelete, Record: Record{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300}},
	}
	if err := c.ApplyChanges(context.Background(), "example.com", cfCreds(), changes); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	if len(d.created) != 2 {
		t.Fatalf("created = %v", d.created)
	}
	if d.created[0]["name"] != "new.example.com" || d.created[0]["content"] != "203.0.113.8" {
		t.Fatalf("create = %v", d.created[0])
	}
	if d.created[1]["ttl"] != float64(1) {
		t.Fatalf("default ttl not automatic: %v", d.created[1])
	}
	if len(d.deleted) != 1 || d.deleted[0] != "a1" {
		t.Fatalf("deleted = %v", d.deleted)
	}
	// Unknown delete targets are exact errors.
	missing := []Change{{Action: ChangeDelete, Record: Record{Name: "ghost", Type: RecordA, Value: "10.0.0.1"}}}
	if err := c.ApplyChanges(context.Background(), "example.com", cfCreds(), missing); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	// API failures surface with messages.
	broken := populatedDouble()
	broken.fail = map[string]int{"POST /zones/zone-id/dns_records": http.StatusUnauthorized}
	c3 := serveCloudflare(t, broken)
	solo := []Change{{Action: ChangeCreate, Record: Record{Name: "n", Type: RecordA, Value: "10.0.0.2"}}}
	if err := c3.ApplyChanges(context.Background(), "example.com", cfCreds(), solo); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

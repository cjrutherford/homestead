package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// digitaloceanDouble serves canned v2 JSON, recording calls and writes.
type digitaloceanDouble struct {
	t       *testing.T
	domains []digitaloceanDomain
	pages   [][]digitaloceanRecord
	calls   []string
	auth    []string
	created []map[string]any
	deleted []string
	fail    map[string]int
}

func (d *digitaloceanDouble) handler(w http.ResponseWriter, r *http.Request) {
	d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	d.auth = append(d.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	if code, ok := d.fail[r.Method+" "+r.URL.Path]; ok {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "unauthorized", "message": "bad token"})
		return
	}
	page := func(idx int, total int, key string, value any) {
		envelope := map[string]any{key: value, "links": map[string]any{"pages": map[string]any{}}}
		if idx+1 < total {
			envelope["links"] = map[string]any{"pages": map[string]any{
				"next": "https://api.digitalocean.com/v2" + r.URL.Path + "?page=" + string(rune('0'+idx+2)),
			}}
		}
		envelope["meta"] = map[string]any{"total": total}
		_ = json.NewEncoder(w).Encode(envelope)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/domains":
		page(0, 1, "domains", d.domains)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/records"):
		idx := 0
		if r.URL.Query().Get("page") == "2" {
			idx = 1
		}
		page(idx, len(d.pages), "domain_records", d.pages[idx])
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/records"):
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			d.t.Fatalf("decode: %v", err)
		}
		d.created = append(d.created, body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain_record": map[string]any{"id": 999, "type": body["type"]},
		})
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/records/"):
		parts := strings.Split(r.URL.Path, "/")
		d.deleted = append(d.deleted, parts[len(parts)-1])
		w.WriteHeader(http.StatusNoContent)
	default:
		d.t.Fatalf("unexpected call %s %s", r.Method, r.URL.Path)
	}
}

func serveDigitalOcean(t *testing.T, d *digitaloceanDouble) *DigitalOcean {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	c, err := NewDigitalOcean(srv.URL)
	if err != nil {
		t.Fatalf("NewDigitalOcean: %v", err)
	}
	return c
}

func doCreds() Credentials { return Credentials{"api_token": "token"} }

func populatedDO() *digitaloceanDouble {
	return &digitaloceanDouble{
		domains: []digitaloceanDomain{{Name: "example.com"}},
		pages: [][]digitaloceanRecord{
			{
				{ID: 1, Type: "A", Name: "@", Data: "203.0.113.7", TTL: 3600},
				{ID: 2, Type: "CNAME", Name: "www", Data: "example.com.", TTL: 300},
			},
			{
				{ID: 3, Type: "TXT", Name: "_acme-challenge", Data: "token", TTL: 60},
				{ID: 4, Type: "MX", Name: "@", Data: "mail.example.com.", TTL: 3600},
			},
		},
	}
}

func TestDigitalOceanContract(t *testing.T) {
	var _ Provider = (*DigitalOcean)(nil)
	c, err := NewDigitalOcean(DigitalOceanAPI)
	if err != nil {
		t.Fatalf("NewDigitalOcean: %v", err)
	}
	if c.Name() != "digitalocean" {
		t.Fatalf("name = %q", c.Name())
	}
	if caps := c.Capabilities(); len(caps) != 2 {
		t.Fatalf("caps = %v", caps)
	}
	fields := c.RequiredCredentials()
	if len(fields) != 1 || fields[0].Name != "api_token" || !fields[0].Secret {
		t.Fatalf("fields = %+v", fields)
	}
	if err := ValidateProvider(c); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	if _, err := NewDigitalOcean("ftp://x/"); err == nil {
		t.Fatal("accepted ftp base")
	}
}

func TestDigitalOceanList(t *testing.T) {
	d := populatedDO()
	c := serveDigitalOcean(t, d)
	got, err := c.ListRecords(context.Background(), "example.com", doCreds())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	want := []Record{
		{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 3600},
		{Name: "www", Type: RecordCNAME, Value: "example.com", TTLSeconds: 300},
		{Name: "_acme-challenge", Type: RecordTXT, Value: "token", TTLSeconds: 60},
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
	if _, err := c.ListRecords(context.Background(), "example.com", nil); err == nil {
		t.Fatal("listed without token")
	}
	absent := &digitaloceanDouble{domains: []digitaloceanDomain{{Name: "other.com"}}}
	c2 := serveDigitalOcean(t, absent)
	if _, err := c2.ListRecords(context.Background(), "example.com", doCreds()); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestDigitalOceanApply(t *testing.T) {
	d := populatedDO()
	c := serveDigitalOcean(t, d)
	changes := []Change{
		{Action: ChangeCreate, Record: Record{Name: "new", Type: RecordA, Value: "203.0.113.8", TTLSeconds: 300}},
		{Action: ChangeCreate, Record: Record{Name: "auto", Type: RecordA, Value: "203.0.113.9"}},
		{Action: ChangeDelete, Record: Record{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 3600}},
	}
	if err := c.ApplyChanges(context.Background(), "example.com", doCreds(), changes); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	if len(d.created) != 2 {
		t.Fatalf("created = %v", d.created)
	}
	if d.created[0]["name"] != "new" || d.created[0]["data"] != "203.0.113.8" || d.created[0]["ttl"] != float64(300) {
		t.Fatalf("create = %v", d.created[0])
	}
	if _, hasTTL := d.created[1]["ttl"]; hasTTL {
		t.Fatalf("default ttl sent explicitly: %v", d.created[1])
	}
	if len(d.deleted) != 1 || d.deleted[0] != "1" {
		t.Fatalf("deleted = %v", d.deleted)
	}
	missing := []Change{{Action: ChangeDelete, Record: Record{Name: "ghost", Type: RecordA, Value: "10.0.0.1"}}}
	if err := c.ApplyChanges(context.Background(), "example.com", doCreds(), missing); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	broken := populatedDO()
	broken.fail = map[string]int{"POST /domains/example.com/records": http.StatusUnauthorized}
	c3 := serveDigitalOcean(t, broken)
	solo := []Change{{Action: ChangeCreate, Record: Record{Name: "n", Type: RecordA, Value: "10.0.0.2"}}}
	if err := c3.ApplyChanges(context.Background(), "example.com", doCreds(), solo); err == nil ||
		!strings.Contains(err.Error(), "bad token") {
		t.Fatalf("err = %v", err)
	}
}

package setup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"homestead/internal/dns"
)

// The permission copy is pinned to what each adapter really calls. The
// adapters run against a recording server; the set of operations they make
// (zone read, record read, record create, record delete) must be exactly the
// set the listed grants cover, and the listed grants must be exactly those.

const r53XMLNS = `xmlns="https://route53.amazonaws.com/doc/2013-04-01/"`

func recorder(t *testing.T, provider string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	h := func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_, _ = io.Copy(io.Discard, r.Body)
		cf := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
		}
		switch provider {
		case "cloudflare":
			switch {
			case r.URL.Path == "/zones":
				cf([]map[string]any{{"id": "z1", "name": "example.com"}})
			case r.Method == http.MethodGet:
				cf([]map[string]any{{"id": "r1", "type": "CNAME", "name": "www.example.com", "content": "example.com", "ttl": 1}})
			default:
				cf(map[string]any{"id": "r1"})
			}
		case "digitalocean":
			switch {
			case r.URL.Path == "/domains":
				_ = json.NewEncoder(w).Encode(map[string]any{"domains": []map[string]any{{"name": "example.com"}}})
			case r.Method == http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{"domain_records": []map[string]any{
					{"id": 5, "type": "CNAME", "name": "www", "data": "example.com.", "ttl": 1800}}})
			case r.Method == http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"domain_record":{"id":9}}`)
			}
		case "route53":
			w.Header().Set("Content-Type", "application/xml")
			switch {
			case r.URL.Path == "/2013-04-01/hostedzone":
				_, _ = io.WriteString(w, `<ListHostedZonesResponse `+r53XMLNS+`><HostedZones><HostedZone><Id>/hostedzone/Z1</Id><Name>example.com.</Name><Config><PrivateZone>false</PrivateZone></Config></HostedZone></HostedZones><IsTruncated>false</IsTruncated></ListHostedZonesResponse>`)
			case r.Method == http.MethodGet:
				_, _ = io.WriteString(w, `<ListResourceRecordSetsResponse `+r53XMLNS+`><ResourceRecordSets><ResourceRecordSet><Name>www.example.com.</Name><Type>CNAME</Type><TTL>60</TTL><ResourceRecords><ResourceRecord><Value>example.com</Value></ResourceRecord></ResourceRecords></ResourceRecordSet></ResourceRecordSets><IsTruncated>false</IsTruncated></ListResourceRecordSetsResponse>`)
			default:
				_, _ = io.WriteString(w, `<ChangeResourceRecordSetsResponse `+r53XMLNS+`><ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`)
			}
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// shape turns concrete calls into operation shapes.
func shape(calls []string) []string {
	seen := map[string]bool{}
	for _, c := range calls {
		method, path, _ := strings.Cut(c, " ")
		segs := strings.Split(strings.Trim(path, "/"), "/")
		var op string
		switch {
		case method == "GET" && (path == "/zones" || path == "/domains" || path == "/2013-04-01/hostedzone"):
			op = "zones:read"
		case method == "GET":
			op = "records:read"
		case method == "POST":
			op = "records:write"
		case method == "DELETE":
			op = "records:delete"
		default:
			op = "other:" + c
		}
		_ = segs
		seen[op] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPermissionCopyMatchesAdapterCalls(t *testing.T) {
	creds := map[string]dns.Credentials{
		"cloudflare":   {"api_token": "tok"},
		"digitalocean": {"api_token": "tok"},
		"route53":      {"access_key_id": "AKID", "secret_access_key": "secret"},
	}
	// Every operation an adapter makes maps to the grant that covers it.
	covers := map[string]map[string]string{
		"cloudflare": {
			"zones:read": "Zone: Zone: Read", "records:read": "Zone: DNS: Edit",
			"records:write": "Zone: DNS: Edit", "records:delete": "Zone: DNS: Edit",
		},
		"digitalocean": {
			"zones:read": "Read scope", "records:read": "Read scope",
			"records:write": "Write scope", "records:delete": "Write scope",
		},
		"route53": {
			"zones:read": "route53:ListHostedZones", "records:read": "route53:ListResourceRecordSets",
			"records:write": "route53:ChangeResourceRecordSets", "records:delete": "route53:ChangeResourceRecordSets",
		},
	}
	for name, cred := range creds {
		t.Run(name, func(t *testing.T) {
			srv, calls := recorder(t, name)
			var p dns.Provider
			var err error
			switch name {
			case "cloudflare":
				p, err = dns.NewCloudflare(srv.URL)
			case "digitalocean":
				p, err = dns.NewDigitalOcean(srv.URL)
			case "route53":
				p, err = dns.NewRoute53(srv.URL)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			existing, err := p.ListRecords(ctx, "example.com", cred)
			if err != nil || len(existing) == 0 {
				t.Fatalf("list: %v %v", existing, err)
			}
			err = p.ApplyChanges(ctx, "example.com", cred, []dns.Change{
				{Action: dns.ChangeCreate, Record: dns.Record{Name: "app", Type: dns.RecordCNAME, Value: "example.com", TTLSeconds: 300}},
				{Action: dns.ChangeDelete, Record: existing[0]},
			})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			ops := shape(*calls)
			needed := map[string]bool{}
			for _, op := range ops {
				g, ok := covers[name][op]
				if !ok {
					t.Fatalf("adapter made an operation the copy does not cover: %s (calls %v)", op, *calls)
				}
				needed[g] = true
			}
			prov, _ := ProviderNamed(name)
			listed := map[string]bool{}
			for _, perm := range prov.Permissions {
				listed[perm.Grant] = true
				if perm.Reason == "" {
					t.Fatalf("grant %q has no reason", perm.Grant)
				}
			}
			for g := range needed {
				if !listed[g] {
					t.Fatalf("adapter needs %q but the copy does not list it", g)
				}
			}
			for g := range listed {
				if !needed[g] {
					t.Fatalf("the copy lists %q but no adapter call needs it", g)
				}
			}
		})
	}
}

func TestManualNeedsNoPermissionsAndNoCredentials(t *testing.T) {
	p, ok := ProviderNamed("manual")
	if !ok || len(p.Permissions) != 0 || len(p.Credentials) != 0 {
		t.Fatalf("manual = %+v", p)
	}
	if _, err := (dns.Manual{}).ListRecords(context.Background(), "example.com", nil); err == nil {
		t.Fatal("manual adapter made an API call")
	}
}

func TestCredentialFieldsComeFromAdapters(t *testing.T) {
	for _, p := range Providers() {
		a := adapter(p.Name)
		if a == nil {
			t.Fatalf("no adapter for %s", p.Name)
		}
		want := a.RequiredCredentials()
		if len(p.Credentials) != len(want) {
			t.Fatalf("%s credentials = %+v, adapter %+v", p.Name, p.Credentials, want)
		}
		for i, f := range want {
			if p.Credentials[i].Name != f.Name || p.Credentials[i].Secret != f.Secret {
				t.Fatalf("%s field %d = %+v, adapter %+v", p.Name, i, p.Credentials[i], f)
			}
		}
	}
}

// Only Cloudflare has a tunnel client (internal/router/tunnel.go), and the
// copy names the Account tunnel grant the tunnel client really calls.
func TestTunnelOptionAndGrant(t *testing.T) {
	for _, p := range Providers() {
		opts := AccessOptions(p.Name)
		var tunnel AccessOption
		for _, o := range opts {
			if o.ID == AccessTunnel {
				tunnel = o
			}
		}
		if tunnel.Available != (p.Name == "cloudflare") {
			t.Fatalf("%s tunnel available = %v", p.Name, tunnel.Available)
		}
		if !tunnel.Available && tunnel.Reason == "" {
			t.Fatalf("%s tunnel unavailable without a reason", p.Name)
		}
		if (len(p.TunnelPermissions) > 0) != (p.Name == "cloudflare") {
			t.Fatalf("%s tunnel permissions = %+v", p.Name, p.TunnelPermissions)
		}
	}
}

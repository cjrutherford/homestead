package dns

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// route53Double serves canned XML, recording calls and batch bodies.
type route53Double struct {
	t       *testing.T
	zones   string
	pages   []string
	calls   []string
	auth    []string
	amzDate []string
	bodies  []string
	fail    map[string]int
}

func (d *route53Double) handler(w http.ResponseWriter, r *http.Request) {
	d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	d.auth = append(d.auth, r.Header.Get("Authorization"))
	d.amzDate = append(d.amzDate, r.Header.Get("x-amz-date"))
	w.Header().Set("Content-Type", "application/xml")
	if code, ok := d.fail[r.Method+" "+r.URL.Path]; ok {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, `<ErrorResponse><Error><Message>denied</Message></Error></ErrorResponse>`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/2013-04-01/hostedzone":
		_, _ = io.WriteString(w, d.zones)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rrset"):
		page := 0
		if r.URL.Query().Get("name") != "" {
			page = 1
		}
		_, _ = io.WriteString(w, d.pages[page])
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rrset"):
		raw, _ := io.ReadAll(r.Body)
		d.bodies = append(d.bodies, string(raw))
		_, _ = io.WriteString(w, `<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`)
	default:
		d.t.Fatalf("unexpected call %s %s", r.Method, r.URL.Path)
	}
}

const r53Zones = `<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones><HostedZone><Id>/hostedzone/Z1</Id><Name>example.com.</Name><Config><PrivateZone>false</PrivateZone></Config></HostedZone><HostedZone><Id>/hostedzone/Z2</Id><Name>example.com.</Name><Config><PrivateZone>true</PrivateZone></Config></HostedZone></HostedZones><IsTruncated>false</IsTruncated></ListHostedZonesResponse>`

const r53Page1 = `<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ResourceRecordSets><ResourceRecordSet><Name>example.com.</Name><Type>A</Type><TTL>300</TTL><ResourceRecords><ResourceRecord><Value>203.0.113.7</Value></ResourceRecord><ResourceRecord><Value>203.0.113.8</Value></ResourceRecord></ResourceRecords></ResourceRecordSet><ResourceRecordSet><Name>www.example.com.</Name><Type>CNAME</Type><TTL>60</TTL><ResourceRecords><ResourceRecord><Value>example.com</Value></ResourceRecord></ResourceRecords></ResourceRecordSet></ResourceRecordSets><IsTruncated>true</IsTruncated><NextRecordName>www.example.com.</NextRecordName><NextRecordType>CNAME</NextRecordType></ListResourceRecordSetsResponse>`

const r53Page2 = `<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ResourceRecordSets><ResourceRecordSet><Name>_acme-challenge.example.com.</Name><Type>TXT</Type><TTL>120</TTL><ResourceRecords><ResourceRecord><Value>"token"</Value></ResourceRecord></ResourceRecords></ResourceRecordSet><ResourceRecordSet><Name>example.com.</Name><Type>MX</Type><TTL>300</TTL><ResourceRecords><ResourceRecord><Value>10 mail.example.com</Value></ResourceRecord></ResourceRecords></ResourceRecordSet><ResourceRecordSet><Name>geo.example.com.</Name><Type>A</Type><SetIdentifier>us</SetIdentifier><TTL>300</TTL><ResourceRecords><ResourceRecord><Value>10.0.0.1</Value></ResourceRecord></ResourceRecords></ResourceRecordSet></ResourceRecordSets><IsTruncated>false</IsTruncated></ListResourceRecordSetsResponse>`

func serveRoute53(t *testing.T, d *route53Double) *Route53 {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	c, err := NewRoute53(srv.URL)
	if err != nil {
		t.Fatalf("NewRoute53: %v", err)
	}
	return c
}

func r53Creds() Credentials {
	return Credentials{"access_key_id": "AKID", "secret_access_key": "secret"}
}

func TestRoute53Contract(t *testing.T) {
	var _ Provider = (*Route53)(nil)
	c, err := NewRoute53(Route53Endpoint)
	if err != nil {
		t.Fatalf("NewRoute53: %v", err)
	}
	if c.Name() != "route53" {
		t.Fatalf("name = %q", c.Name())
	}
	if caps := c.Capabilities(); len(caps) != 2 {
		t.Fatalf("caps = %v", caps)
	}
	fields := c.RequiredCredentials()
	if len(fields) != 2 || fields[0].Name != "access_key_id" || !fields[1].Secret {
		t.Fatalf("fields = %+v", fields)
	}
	if err := ValidateProvider(c); err != nil {
		t.Fatalf("ValidateProvider: %v", err)
	}
	if _, err := NewRoute53("ftp://x/"); err == nil {
		t.Fatal("accepted ftp base")
	}
}

func TestRoute53List(t *testing.T) {
	d := &route53Double{zones: r53Zones, pages: []string{r53Page1, r53Page2}}
	c := serveRoute53(t, d)
	got, err := c.ListRecords(context.Background(), "example.com", r53Creds())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	want := []Record{
		{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300},
		{Name: "@", Type: RecordA, Value: "203.0.113.8", TTLSeconds: 300},
		{Name: "www", Type: RecordCNAME, Value: "example.com", TTLSeconds: 60},
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
	if !strings.HasPrefix(d.auth[0], "AWS4-HMAC-SHA256 Credential=AKID/") ||
		!strings.Contains(d.auth[0], "us-east-1/route53/aws4_request") ||
		!strings.Contains(d.auth[0], "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("auth = %q", d.auth[0])
	}
	if len(d.auth[0]) < 200 || d.amzDate[0] == "" {
		t.Fatalf("auth/date = %q %q", d.auth[0], d.amzDate[0])
	}
	if _, err := c.ListRecords(context.Background(), "example.com", nil); err == nil {
		t.Fatal("listed without credentials")
	}
}

func TestRoute53ZoneDisambiguation(t *testing.T) {
	none := &route53Double{zones: strings.Replace(r53Zones, "example.com.", "other.com.", -1)}
	c := serveRoute53(t, none)
	if _, err := c.ListRecords(context.Background(), "example.com", r53Creds()); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	onlyPrivate := &route53Double{zones: `<ListHostedZonesResponse><HostedZones><HostedZone><Id>/hostedzone/Z9</Id><Name>example.com.</Name><Config><PrivateZone>true</PrivateZone></Config></HostedZone></HostedZones><IsTruncated>false</IsTruncated></ListHostedZonesResponse>`}
	c2 := serveRoute53(t, onlyPrivate)
	if _, err := c2.ListRecords(context.Background(), "example.com", r53Creds()); err == nil ||
		!strings.Contains(err.Error(), "only as private") {
		t.Fatalf("err = %v", err)
	}
}

func TestRoute53ApplyGrouped(t *testing.T) {
	d := &route53Double{zones: r53Zones, pages: []string{r53Page1, r53Page2}}
	c := serveRoute53(t, d)
	changes := []Change{
		// Round-robin widen: one more A alongside two actual.
		{Action: ChangeCreate, Record: Record{Name: "@", Type: RecordA, Value: "203.0.113.9", TTLSeconds: 300}},
		// Shrink the www CNAME away entirely.
		{Action: ChangeDelete, Record: Record{Name: "www", Type: RecordCNAME, Value: "example.com", TTLSeconds: 60}},
		// Untouched TXT must not appear.
	}
	if err := c.ApplyChanges(context.Background(), "example.com", r53Creds(), changes); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}
	if len(d.bodies) != 1 {
		t.Fatalf("batches = %d", len(d.bodies))
	}
	body := d.bodies[0]
	for _, want := range []string{
		"<Action>DELETE</Action>",
		"<Name>www.example.com.</Name><Type>CNAME</Type><TTL>60</TTL>",
		"<Value>example.com</Value>",
		"<Action>CREATE</Action>",
		"<Name>example.com.</Name><Type>A</Type><TTL>300</TTL>",
		"<Value>203.0.113.7</Value><ResourceRecord><Value>203.0.113.8</Value>",
		"<Value>203.0.113.9</Value>",
	} {
		compact := strings.ReplaceAll(body, "</ResourceRecord><ResourceRecord>", "<ResourceRecord>")
		if !strings.Contains(body, want) && !strings.Contains(compact, want) {
			t.Fatalf("batch missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "_acme-challenge") {
		t.Fatalf("untouched set in batch:\n%s", body)
	}
}

func TestRoute53ApplyNoopAndErrors(t *testing.T) {
	d := &route53Double{zones: r53Zones, pages: []string{r53Page1, r53Page2}}
	c := serveRoute53(t, d)
	noop := []Change{
		{Action: ChangeCreate, Record: Record{Name: "@", Type: RecordA, Value: "203.0.113.7", TTLSeconds: 300}},
		{Action: ChangeDelete, Record: Record{Name: "ghost", Type: RecordA, Value: "10.0.0.1", TTLSeconds: 300}},
	}
	// ghost delete on an untouched key plus redundant create: create is
	// redundant, ghost delete targets nothing actual — net effect on @ is
	// nil, ghost key has no actual set... deleting absent values from an
	// absent set is a no-op, so nothing posts.
	if err := c.ApplyChanges(context.Background(), "example.com", r53Creds(), noop); err != nil {
		t.Fatalf("noop err = %v", err)
	}
	if len(d.bodies) != 0 {
		t.Fatalf("noop posted:\n%s", d.bodies[0])
	}
	broken := &route53Double{
		zones: r53Zones,
		pages: []string{r53Page1, r53Page2},
		fail:  map[string]int{"POST /2013-04-01/hostedzone/Z1/rrset": http.StatusForbidden},
	}
	c2 := serveRoute53(t, broken)
	solo := []Change{{Action: ChangeCreate, Record: Record{Name: "n", Type: RecordA, Value: "10.0.0.2"}}}
	if err := c2.ApplyChanges(context.Background(), "example.com", r53Creds(), solo); err == nil ||
		!strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

package dns

import (
	"strings"
	"testing"
)

func validZone() Zone {
	return Zone{
		Name: "lan",
		Records: []Record{
			{Name: "app", Type: RecordA, Value: "192.168.1.10", TTLSeconds: 300},
			{Name: "app", Type: RecordA, Value: "192.168.1.11"},
			{Name: "v6", Type: RecordAAAA, Value: "::1"},
			{Name: "docs", Type: RecordCNAME, Value: "app.lan"},
			{Name: "_acme-challenge.app", Type: RecordTXT, Value: "token"},
		},
		Views: []View{
			{Name: "lan", Audience: AudienceLocal, Records: []Record{
				{Name: "app", Type: RecordA, Value: "192.168.1.10"},
			}},
			{Name: "public", Audience: AudiencePublic, Records: []Record{
				{Name: "app", Type: RecordA, Value: "203.0.113.7"},
			}},
		},
	}
}

func TestZoneValid(t *testing.T) {
	z := validZone()
	if err := z.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sorted := z.Sorted()
	if len(sorted) != 5 || sorted[0].Name != "_acme-challenge.app" || sorted[4].Name != "v6" {
		t.Fatalf("sorted = %+v", sorted)
	}
}

func TestZoneInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Zone)
		want   string
	}{
		{"empty zone", func(z *Zone) { z.Name = "" }, "zone.name"},
		{"uppercase zone", func(z *Zone) { z.Name = "LAN" }, "lowercase"},
		{"fqdn record", func(z *Zone) { z.Records[0].Name = "app.lan." }, "relative"},
		{"bad type", func(z *Zone) { z.Records[0].Type = "MX" }, "record.type"},
		{"v6 in A", func(z *Zone) { z.Records[0].Value = "::1" }, "IPv4"},
		{"v4 in AAAA", func(z *Zone) { z.Records[2].Value = "10.0.0.1" }, "IPv6"},
		{"name in CNAME", func(z *Zone) { z.Records[3].Value = "not a host!" }, "record.value"},
		{"empty TXT", func(z *Zone) { z.Records[4].Value = "" }, "record.value"},
		{"low TTL", func(z *Zone) { z.Records[0].TTLSeconds = 5 }, "record.ttl"},
		{"dup triple", func(z *Zone) {
			z.Records = append(z.Records, z.Records[0])
		}, "duplicate record"},
		{"cname clash", func(z *Zone) {
			z.Records = append(z.Records, Record{Name: "docs", Type: RecordTXT, Value: "x"})
		}, "excludes other records"},
		{"dup audience", func(z *Zone) {
			z.Views = append(z.Views, View{Name: "extra", Audience: AudienceLocal})
		}, "duplicate view audience"},
		{"bad audience", func(z *Zone) { z.Views[0].Audience = "dmz" }, "view.audience"},
		{"view clash", func(z *Zone) {
			z.Views[0].Records = append(z.Views[0].Records,
				Record{Name: "web", Type: RecordCNAME, Value: "app.lan"},
				Record{Name: "web", Type: RecordTXT, Value: "x"})
		}, "excludes other records"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z := validZone()
			tc.mutate(&z)
			err := z.Validate()
			if err == nil {
				t.Fatal("accepted invalid zone")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestAudienceAndTypeValues(t *testing.T) {
	for _, a := range []Audience{AudienceLocal, AudienceOverlay, AudiencePublic} {
		if !a.Valid() {
			t.Fatalf("%q not valid", a)
		}
	}
	if (Audience("dmz")).Valid() {
		t.Fatal("dmz valid")
	}
	for _, rt := range []RecordType{RecordA, RecordAAAA, RecordCNAME, RecordTXT} {
		if !rt.Valid() {
			t.Fatalf("%q not valid", rt)
		}
	}
	if (RecordType("MX")).Valid() {
		t.Fatal("MX valid")
	}
}

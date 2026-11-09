package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUniversalCovers(t *testing.T) {
	for _, c := range []struct {
		name, zone string
		want       bool
	}{
		{"example.com", "example.com", true},                 // apex
		{"auth.example.com", "example.com", true},            // one level
		{"auth.homestead.example.com", "example.com", false}, // two levels
		{"a.b.c.example.com", "example.com", false},          // three levels
		{"homestead.example.com", "example.com", true},       // a domain below its zone
		{"auth.homestead.example.com", "homestead.example.com", true},
		{"Auth.Example.COM.", "example.com", true}, // case and trailing dot
		{"auth.example.com", "EXAMPLE.com.", true},
		{"AUTH.Homestead.example.com.", "example.com.", false},
		{"auth.example.org", "example.com", false}, // outside the zone
		{"badexample.com", "example.com", false},
		{"auth.example.com", "", false},
	} {
		if got := UniversalCovers(c.name, c.zone); got != c.want {
			t.Errorf("UniversalCovers(%q, %q) = %v, want %v", c.name, c.zone, got, c.want)
		}
	}
}

func TestHostCovers(t *testing.T) {
	for _, c := range []struct {
		host, name string
		want       bool
	}{
		{"auth.homestead.example.com", "auth.homestead.example.com", true},
		{"*.homestead.example.com", "auth.homestead.example.com", true},
		{"*.Homestead.example.com.", "AUTH.homestead.example.com", true},
		{"*.homestead.example.com", "a.b.homestead.example.com", false},
		{"*.homestead.example.com", "homestead.example.com", false},
		{"*.example.com", "auth.homestead.example.com", false},
		{"photos.example.com", "auth.example.com", false},
	} {
		if got := HostCovers(c.host, c.name); got != c.want {
			t.Errorf("HostCovers(%q, %q) = %v, want %v", c.host, c.name, got, c.want)
		}
	}
}

func TestZoneCertificatesCovers(t *testing.T) {
	free := ZoneCertificates{Zone: "example.com"}
	if got := free.Covers("auth.example.com"); !got.Covered || got.By != "universal" {
		t.Fatalf("one level = %+v", got)
	}
	if got := free.Covers("auth.homestead.example.com"); got.Covered {
		t.Fatalf("two levels on a free zone = %+v", got)
	}
	pack := ZoneCertificates{Zone: "example.com", Hosts: []string{"example.com", "*.example.com", "*.homestead.example.com"}}
	if got := pack.Covers("auth.homestead.example.com"); !got.Covered || got.By != "certificate pack" {
		t.Fatalf("pack = %+v", got)
	}
	if got := pack.Covers("a.b.homestead.example.com"); got.Covered {
		t.Fatalf("pack wildcard spans two labels: %+v", got)
	}
	total := ZoneCertificates{Zone: "example.com", TotalTLS: true}
	if got := total.Covers("a.b.homestead.example.com"); !got.Covered || got.By != "total tls" {
		t.Fatalf("total tls = %+v", got)
	}
	unreadable := ZoneCertificates{Zone: "example.com", Hosts: []string{"*.homestead.example.com"}, TotalTLS: true, Unreadable: true}
	if got := unreadable.Covers("auth.homestead.example.com"); got.Covered {
		t.Fatalf("unreadable applies the universal rule only: %+v", got)
	}
	if got := unreadable.Covers("auth.example.com"); !got.Covered {
		t.Fatalf("unreadable one level = %+v", got)
	}
}

func TestCoverageTexts(t *testing.T) {
	want := "Cloudflare has no certificate for auth.homestead.example.com yet: browsers will fail to connect. " +
		"Names more than one level below example.com need Advanced Certificate Manager on the zone " +
		"(a certificate for *.homestead.example.com, or Total TLS). Names one level below, such as auth.example.com, are covered for free."
	if got := UncoveredText("Auth.Homestead.Example.com.", "example.com"); got != want {
		t.Fatalf("text = %q", got)
	}
	if got := UnreadableText("example.com"); !strings.Contains(got, CertificatePermission) {
		t.Fatalf("unreadable text = %q", got)
	}
}

func TestZoneCertificatesRead(t *testing.T) {
	deny := false
	totalFails := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
			return
		}
		switch r.URL.Path {
		case "/zones/zone1/ssl/certificate_packs":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[
				{"status":"active","hosts":["example.com","*.example.com"]},
				{"status":"active","hosts":["*.homestead.example.com"]},
				{"status":"pending_validation","hosts":["*.other.example.com"]}]}`))
		case "/zones/zone1/acm/total_tls":
			if totalFails {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1450,"message":"needs Advanced Certificate Manager"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"enabled":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewTunnelClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	zone := Zone{ID: "zone1", Name: "example.com"}
	certs, err := client.ZoneCertificates(context.Background(), "token", zone)
	if err != nil || certs.Unreadable || certs.TotalTLS {
		t.Fatalf("certs = %+v, %v", certs, err)
	}
	if !certs.Covers("auth.homestead.example.com").Covered || certs.Covers("auth.other.example.com").Covered {
		t.Fatalf("active packs only: %+v", certs)
	}
	totalFails = true
	if certs, err := client.ZoneCertificates(context.Background(), "token", zone); err != nil || certs.Unreadable {
		t.Fatalf("a refused Total TLS read means off: %+v, %v", certs, err)
	}
	deny = true
	certs, err = client.ZoneCertificates(context.Background(), "token", zone)
	if err != nil || !certs.Unreadable {
		t.Fatalf("denied read = %+v, %v", certs, err)
	}
}

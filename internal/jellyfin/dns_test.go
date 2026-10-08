package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/docker"
)

func TestLocalRewrite(t *testing.T) {
	rewrite, err := LocalRewrite("jellyfin.lan", netip.MustParseAddr("192.168.1.10"))
	if err != nil {
		t.Fatalf("LocalRewrite: %v", err)
	}
	if rewrite.Domain != "jellyfin.lan" || rewrite.Answer != "192.168.1.10" {
		t.Fatalf("rewrite = %+v", rewrite)
	}
	if _, err := LocalRewrite("Bad!", netip.MustParseAddr("192.168.1.10")); err == nil {
		t.Fatal("rewrote bad hostname")
	}
	if _, err := LocalRewrite("jellyfin.lan", netip.Addr{}); err == nil {
		t.Fatal("rewrote bad answer")
	}
}

// adguardDouble answers rewrite adds for apply tests.
func adguardDouble(t *testing.T, stored *[]dns.Rewrite) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/control/rewrite/add" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		var entry dns.Rewrite
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			t.Fatalf("decode: %v", err)
		}
		*stored = append(*stored, entry)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestApplyLocalName(t *testing.T) {
	var stored []dns.Rewrite
	client, err := dns.NewAdGuard(adguardDouble(t, &stored), "owner", "secret")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	rewrite, err := LocalRewrite("jellyfin.lan", netip.MustParseAddr("192.168.1.10"))
	if err != nil {
		t.Fatalf("LocalRewrite: %v", err)
	}
	if err := ApplyLocalName(context.Background(), client, rewrite); err != nil {
		t.Fatalf("ApplyLocalName: %v", err)
	}
	if len(stored) != 1 || stored[0] != rewrite {
		t.Fatalf("stored = %+v", stored)
	}
	if err := ApplyLocalName(context.Background(), nil, rewrite); err == nil {
		t.Fatal("applied without client")
	}
}

func fakeLookup(addrs []netip.Addr, err error) docker.LookupFunc {
	return func(_ context.Context, _ string) ([]netip.Addr, error) {
		return addrs, err
	}
}

func TestVerifyLocalName(t *testing.T) {
	answer := netip.MustParseAddr("192.168.1.10")
	ctx := context.Background()
	if err := VerifyLocalName(ctx, fakeLookup([]netip.Addr{answer}, nil), "jellyfin.lan", answer); err != nil {
		t.Fatalf("match: %v", err)
	}
	other := netip.MustParseAddr("192.168.1.99")
	if err := VerifyLocalName(ctx, fakeLookup([]netip.Addr{other}, nil), "jellyfin.lan", answer); err == nil ||
		!strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("err = %v", err)
	}
	if err := VerifyLocalName(ctx, fakeLookup(nil, errors.New("SERVFAIL")), "jellyfin.lan", answer); err == nil {
		t.Fatal("verified through failure")
	}
	if err := VerifyLocalName(ctx, nil, "jellyfin.lan", answer); err == nil {
		t.Fatal("verified without lookup")
	}
}

func TestPublicRecord(t *testing.T) {
	record, err := PublicRecord("example.com", "jellyfin.example.com", netip.MustParseAddr("203.0.113.7"))
	if err != nil {
		t.Fatalf("PublicRecord: %v", err)
	}
	if record.Name != "jellyfin" || record.Type != dns.RecordA || record.Value != "203.0.113.7" {
		t.Fatalf("record = %+v", record)
	}
	v6, err := PublicRecord("example.com", "jellyfin.example.com", netip.MustParseAddr("2001:db8::1"))
	if err != nil {
		t.Fatalf("PublicRecord: %v", err)
	}
	if v6.Type != dns.RecordAAAA {
		t.Fatalf("record = %+v", v6)
	}
	if _, err := PublicRecord("example.com", "jellyfin.other.com", netip.MustParseAddr("203.0.113.7")); err == nil {
		t.Fatal("planned outside zone")
	}
	if _, err := PublicRecord("example.com", "jellyfin.example.com", netip.Addr{}); err == nil {
		t.Fatal("planned bad address")
	}
}

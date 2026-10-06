package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func firewallServer(t *testing.T) *Server {
	t.Helper()
	srv := New("127.0.0.1:0")
	srv.MountFirewall(8080)
	return srv
}

func TestFirewallStatusShape(t *testing.T) {
	srv := firewallServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"nft_available"`, `"managed"`, `"daemon_port":8080`, `"local_subnets"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestFirewallPlanUnprivileged(t *testing.T) {
	srv := firewallServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/plan", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	// Unprivileged here: 503. Privileged elsewhere: 200 with a plan shape.
	// Either way the route exists and never mutates.
	if rec.Code == http.StatusOK {
		for _, want := range []string{`"in_sync"`, `"changes"`, `"script"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("body missing %s", want)
			}
		}
	} else if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d, want 200 or 503", rec.Code)
	}
}

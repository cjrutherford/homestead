package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func firewallStub(t *testing.T, applyStatus int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path + " " + r.Method {
		case "/api/v1/firewall/status GET":
			_, _ = w.Write([]byte(`{"nft_available":true,"ruleset_accessible":false,"managed":false,"rule_count":0,"daemon_port":8080,"local_subnets":["192.168.1.0/24"]}`))
		case "/api/v1/firewall/plan GET":
			_, _ = w.Write([]byte(`{"in_sync":false,"changes":[{"action":"create-table","detail":"create"}],"script":"table"}`))
		case "/api/v1/firewall/apply POST":
			if applyStatus != 200 {
				http.Error(w, "would lock out", applyStatus)
				return
			}
			_, _ = w.Write([]byte(`{"applied":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestFirewallStatus(t *testing.T) {
	url := firewallStub(t, 200)
	code, stdout, _ := invoke(t, []string{"--server", url, "firewall", "status"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "192.168.1.0/24") || !strings.Contains(stdout, "daemon port 8080") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestFirewallPlan(t *testing.T) {
	url := firewallStub(t, 200)
	code, stdout, _ := invoke(t, []string{"--server", url, "firewall", "plan"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "create-table") || !strings.Contains(stdout, "--- script ---") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestFirewallApply(t *testing.T) {
	url := firewallStub(t, 200)
	code, stdout, _ := invoke(t, []string{"--server", url, "firewall", "apply"})
	if code != exitOK || !strings.Contains(stdout, "firewall applied") {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
}

func TestFirewallApplyGuarded(t *testing.T) {
	url := firewallStub(t, 409)
	code, _, stderr := invoke(t, []string{"--server", url, "firewall", "apply"})
	if code != exitError || !strings.Contains(stderr, "apply refused") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestFirewallUsage(t *testing.T) {
	if code, _, _ := invoke(t, []string{"firewall"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code, _, _ := invoke(t, []string{"firewall", "bogus"}); code != exitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
}

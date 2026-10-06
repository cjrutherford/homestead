package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func firewallStub(t *testing.T) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path + " " + r.Method {
		case "/api/v1/firewall/status GET":
			_, _ = w.Write([]byte(`{"nft_available":true,"ruleset_accessible":true,"managed":false,"rule_count":0,"daemon_port":8080,"local_subnets":["192.168.1.0/24"]}`))
		case "/api/v1/firewall/plan GET":
			_, _ = w.Write([]byte(`{"in_sync":false,"changes":[{"action":"create-table","detail":"create"}],"script":"table"}`))
		case "/api/v1/firewall/apply POST":
			_, _ = w.Write([]byte(`{"applied":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return New(ts.URL)
}

func TestFirewallStatus(t *testing.T) {
	status, err := firewallStub(t).FirewallStatus(context.Background())
	if err != nil {
		t.Fatalf("FirewallStatus: %v", err)
	}
	if !status.NFTAvailable || status.DaemonPort != 8080 || len(status.LocalSubnets) != 1 {
		t.Fatalf("status = %+v", status)
	}
}

func TestFirewallPlan(t *testing.T) {
	plan, err := firewallStub(t).FirewallPlan(context.Background())
	if err != nil {
		t.Fatalf("FirewallPlan: %v", err)
	}
	if plan.InSync || len(plan.Changes) != 1 || plan.Changes[0].Action != "create-table" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestFirewallApply(t *testing.T) {
	if err := firewallStub(t).FirewallApply(context.Background()); err != nil {
		t.Fatalf("FirewallApply: %v", err)
	}
}

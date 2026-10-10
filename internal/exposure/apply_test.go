package exposure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/firewall"
	"homestead/internal/operation"
	"homestead/internal/store"
)

func openApplyStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func applyTestPlan(t *testing.T) Plan {
	t.Helper()
	plan, err := PlanExposure(planRequest())
	if err != nil {
		t.Fatalf("PlanExposure: %v", err)
	}
	return plan
}

// stubApplySeams replaces network discovery, firewall application,
// and proxy reload: tests assert composition without nft or daemons.
func stubApplySeams(t *testing.T, network discover.Network) (*firewall.RuleSet, *int) {
	t.Helper()
	oldDiscover, oldApply, oldReload := discoverNetwork, applyFirewallSet, reloadProxy
	var applied firewall.RuleSet
	reloads := 0
	discoverNetwork = func() (discover.Network, error) { return network, nil }
	applyFirewallSet = func(set firewall.RuleSet) error { applied = set; return nil }
	reloadProxy = func(ctx context.Context) error { reloads++; return nil }
	t.Cleanup(func() { discoverNetwork, applyFirewallSet, reloadProxy = oldDiscover, oldApply, oldReload })
	return &applied, &reloads
}

func applyTestNetwork() discover.Network {
	return discover.Network{Interfaces: []discover.Interface{
		{Name: "eth0", Up: true, Addresses: []discover.Address{
			{IP: "192.168.1.10", Network: "192.168.1.0/24"},
		}},
	}}
}

func TestApprove(t *testing.T) {
	plan := applyTestPlan(t)
	approval, err := Approve(plan, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approval.Host != "app.example.com" || approval.Actor != "owner" ||
		len(approval.Networks) != 1 || approval.Plan.To != docker.ExposurePublic {
		t.Fatalf("approval = %+v", approval)
	}
	if _, err := Approve(plan, "", []string{"203.0.113.0/24"}); err == nil {
		t.Fatal("actorless approval accepted")
	}
	stale := plan
	stale.Edge.Exposure = docker.ExposureLocal
	if _, err := Approve(stale, "owner", nil); err == nil {
		t.Fatal("misaligned approval accepted")
	}
	noop := plan
	noop.From = docker.ExposurePublic
	if _, err := Approve(noop, "owner", nil); err == nil {
		t.Fatal("no-op approval accepted")
	}
	if _, err := Approve(plan, "owner", []string{"not-a-cidr"}); err == nil {
		t.Fatal("bad CIDR accepted")
	}
}

func TestApprovalRoundTrip(t *testing.T) {
	st := openApplyStore(t)
	plan := applyTestPlan(t)
	approval, err := Approve(plan, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, approval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := ApprovalFor(st, "app.example.com")
	if err != nil {
		t.Fatalf("ApprovalFor: %v", err)
	}
	if got.Host != approval.Host || got.Actor != "owner" || len(got.Networks) != 1 ||
		got.Plan.To != docker.ExposurePublic || len(got.Plan.Firewall) != 2 {
		t.Fatalf("got = %+v", got)
	}
	// Re-approving replaces the record.
	approval.Actor = "owner2"
	if err := RecordApproval(st, approval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err = ApprovalFor(st, "app.example.com")
	if err != nil {
		t.Fatalf("ApprovalFor: %v", err)
	}
	if got.Actor != "owner2" {
		t.Fatalf("got = %+v", got)
	}
	listed, err := Approvals(st)
	if err != nil {
		t.Fatalf("Approvals: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %+v", listed)
	}
	if _, err := ApprovalFor(st, "ghost.example.com"); err != ErrNoApproval {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyDefinitionValidation(t *testing.T) {
	st := openApplyStore(t)
	if _, err := ApplyDefinition(nil, "app.example.com", 8080, t.TempDir()); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := ApplyDefinition(st, "", 8080, t.TempDir()); err == nil {
		t.Fatal("empty host accepted")
	}
	if _, err := ApplyDefinition(st, "app.example.com", 0, t.TempDir()); err == nil {
		t.Fatal("bad port accepted")
	}
	if _, err := ApplyDefinition(st, "app.example.com", 8080, ""); err == nil {
		t.Fatal("empty dir accepted")
	}
	if _, err := ApplyDefinition(st, "app.example.com", 8080, t.TempDir()); err != ErrNoApproval {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyFlow(t *testing.T) {
	st := openApplyStore(t)
	plan := applyTestPlan(t)
	approval, err := Approve(plan, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, approval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// A second route approves alongside: composition keeps both.
	shop := plan
	shop.Host = "shop.example.com"
	shop.Edge.Host = "shop.example.com"
	shopApproval, err := Approve(shop, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, shopApproval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	applied, reloads := stubApplySeams(t, applyTestNetwork())
	caddyDir := t.TempDir()
	def, err := ApplyDefinition(st, "app.example.com", 8080, caddyDir)
	if err != nil {
		t.Fatalf("ApplyDefinition: %v", err)
	}
	if def.Type != "exposure.apply" || len(def.Steps) != 2 ||
		def.Steps[0].Name != "firewall" || def.Steps[1].Name != "edge" {
		t.Fatalf("def = %+v", def)
	}
	runner := operation.NewRunner(st)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	// Firewall composes baseline plus both routes with approved CIDRs.
	names := map[string]bool{}
	var scoped bool
	for _, rule := range applied.Rules {
		names[rule.Name] = true
		if rule.Name == "web-http-public" && len(rule.Service.Name) > 0 {
			for _, cidr := range []string{rule.Source.CIDR} {
				if cidr == "203.0.113.0/24" {
					scoped = true
				}
			}
		}
	}
	for _, want := range []string{"management-loopback", "web-http-public", "web-https-public"} {
		if !names[want] {
			t.Fatalf("rules = %+v, want %q", applied.Rules, want)
		}
	}
	if !scoped {
		t.Fatalf("rules = %+v, want approved CIDR", applied.Rules)
	}
	// Edge writes this host's snippet and reloads once.
	content, err := os.ReadFile(filepath.Join(caddyDir, "routes", string(docker.RouteFilename("app.example.com"))))
	if err != nil || !strings.Contains(string(content), "app.example.com") {
		t.Fatalf("route = %q, err = %v", content, err)
	}
	if *reloads != 1 {
		t.Fatalf("reloads = %d", *reloads)
	}
}

func TestApplyConflictNamesOwners(t *testing.T) {
	st := openApplyStore(t)
	plan := applyTestPlan(t)
	approval, err := Approve(plan, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, approval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Same service name, different port: same rule name, different
	// content — ambiguous, never silently picked.
	clash := plan
	clash.Host = "other.example.com"
	clash.Edge.Host = "other.example.com"
	clash.Firewall = []firewall.ServicePolicy{{
		Service: firewall.Service{Name: "web-http", Protocol: firewall.ProtocolTCP, Port: 81},
		Sources: []firewall.Source{{Zone: firewall.ZonePublic}},
	}}
	clashApproval, err := Approve(clash, "owner", []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := RecordApproval(st, clashApproval); err != nil {
		t.Fatalf("Record: %v", err)
	}
	applied, _ := stubApplySeams(t, applyTestNetwork())
	_ = applied
	def, err := ApplyDefinition(st, "app.example.com", 8080, t.TempDir())
	if err != nil {
		t.Fatalf("ApplyDefinition: %v", err)
	}
	runner := operation.NewRunner(st)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	if !strings.Contains(done.Error, "app.example.com") || !strings.Contains(done.Error, "other.example.com") {
		t.Fatalf("error = %q, want both owners", done.Error)
	}
}

func TestRefinePolicies(t *testing.T) {
	policies := []firewall.ServicePolicy{{
		Service: firewall.Service{Name: "web", Protocol: firewall.ProtocolTCP, Port: 80},
		Sources: []firewall.Source{{Zone: firewall.ZonePublic}, {Zone: firewall.ZoneLocal}},
	}}
	refined, err := refinePolicies(policies, []string{"203.0.113.0/24", "198.51.100.0/24"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if len(refined) != 1 || len(refined[0].Sources) != 3 {
		t.Fatalf("refined = %+v", refined)
	}
	if refined[0].Sources[0].CIDR != "203.0.113.0/24" || refined[0].Sources[2].Zone != firewall.ZoneLocal {
		t.Fatalf("refined = %+v", refined)
	}
	wide := []firewall.ServicePolicy{{
		Service: firewall.Service{Name: "web", Protocol: firewall.ProtocolTCP, Port: 80},
		Sources: []firewall.Source{{Zone: firewall.ZonePublic}},
	}}
	if _, err := refinePolicies(wide, nil); err == nil {
		t.Fatal("wide sources without CIDRs accepted")
	}
	narrow := []firewall.ServicePolicy{{
		Service: firewall.Service{Name: "web", Protocol: firewall.ProtocolTCP, Port: 80},
		Sources: []firewall.Source{{Zone: firewall.ZonePublic, CIDR: "192.0.2.0/24"}},
	}}
	refined, err = refinePolicies(narrow, nil)
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if len(refined[0].Sources) != 1 || refined[0].Sources[0].CIDR != "192.0.2.0/24" {
		t.Fatalf("refined = %+v", refined)
	}
}

package baseline

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/firewall"
	"homestead/internal/store"
)

var baselineNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// openClaimedService returns claim and auth services over a scratch
// store, claimed when asked, with an administrator when asked.
func openClaimedService(t *testing.T, claimed, withAdmin bool) (*claim.Service, *auth.Service) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	claimSvc := claim.New(s)
	authSvc := auth.New(s, claimSvc)
	if !claimed {
		return claimSvc, authSvc
	}
	token, err := claimSvc.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if err := claimSvc.Claim(token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if withAdmin {
		if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err != nil {
			t.Fatalf("SetupAdmin: %v", err)
		}
	}
	return claimSvc, authSvc
}

func appliedFirewall() func() firewall.State {
	return func() firewall.State {
		return firewall.State{NFTAvailable: true, RulesetAccessible: true, HasHomesteadTable: true}
	}
}

func checkByName(report Report) map[string]Check {
	out := map[string]Check{}
	for _, check := range report.Checks {
		out[check.Name] = check
	}
	return out
}

func TestAssessUnclaimedAppliance(t *testing.T) {
	claimSvc, authSvc := openClaimedService(t, false, false)
	report := Assess(Inputs{Claim: claimSvc, Auth: authSvc, Firewall: appliedFirewall(), Now: baselineNow})
	if report.Ready {
		t.Fatalf("report = %+v, want unready", report)
	}
	if len(report.Checks) != 4 {
		t.Fatalf("checks = %+v", report.Checks)
	}
	byName := checkByName(report)
	if byName["claim"].Status != StatusFail {
		t.Fatalf("claim = %+v", byName["claim"])
	}
	if byName["admin"].Status != StatusFail {
		t.Fatalf("admin = %+v", byName["admin"])
	}
	if byName["firewall"].Status != StatusPass {
		t.Fatalf("firewall = %+v", byName["firewall"])
	}
	if byName["management-tls"].Status != StatusPass {
		t.Fatalf("management-tls = %+v", byName["management-tls"])
	}
}

func TestAssessReady(t *testing.T) {
	claimSvc, authSvc := openClaimedService(t, true, true)
	report := Assess(Inputs{Claim: claimSvc, Auth: authSvc, Firewall: appliedFirewall(), Now: baselineNow})
	if !report.Ready {
		t.Fatalf("report = %+v, want ready", report)
	}
	for _, check := range report.Checks {
		if check.Status != StatusPass || check.Detail == "" {
			t.Fatalf("check = %+v", check)
		}
	}
	names := []string{"claim", "admin", "firewall", "management-tls"}
	for i, name := range names {
		if report.Checks[i].Name != name {
			t.Fatalf("checks = %+v", report.Checks)
		}
	}
}

func TestAssessFirewallLegs(t *testing.T) {
	claimSvc, authSvc := openClaimedService(t, true, true)
	cases := []struct {
		name   string
		state  firewall.State
		detail string
	}{
		{"no nftables", firewall.State{}, "nftables unavailable"},
		{"unreadable ruleset", firewall.State{NFTAvailable: true}, "unreadable"},
		{"unapplied rules", firewall.State{NFTAvailable: true, RulesetAccessible: true}, "not applied"},
	}
	for _, tc := range cases {
		state := tc.state
		report := Assess(Inputs{Claim: claimSvc, Auth: authSvc, Firewall: func() firewall.State { return state }, Now: baselineNow})
		if report.Ready {
			t.Fatalf("%s: ready", tc.name)
		}
		got := checkByName(report)["firewall"]
		if got.Status != StatusFail || !strings.Contains(got.Detail, tc.detail) {
			t.Fatalf("%s = %+v", tc.name, got)
		}
	}
}

func TestAssessNilInputs(t *testing.T) {
	report := Assess(Inputs{Now: baselineNow})
	if report.Ready {
		t.Fatalf("report = %+v, want unready", report)
	}
	byName := checkByName(report)
	// Unwired dependencies read as incomplete; the pure-crypto TLS
	// leg passes regardless — it needs no services.
	for _, name := range []string{"claim", "admin", "firewall"} {
		if byName[name].Status != StatusFail {
			t.Fatalf("check = %+v", byName[name])
		}
	}
	if byName["management-tls"].Status != StatusPass {
		t.Fatalf("check = %+v", byName["management-tls"])
	}
}

func TestHasAdmin(t *testing.T) {
	_, authSvc := openClaimedService(t, true, false)
	has, err := authSvc.HasAdmin()
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if has {
		t.Fatal("admin exists before setup")
	}
	if _, err := authSvc.SetupAdmin("owner", "long-enough-password"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	has, err = authSvc.HasAdmin()
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if !has {
		t.Fatal("admin missing after setup")
	}
}

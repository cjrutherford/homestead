package cert

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/wizard"
)

func testMoves(kept bool) *MovePlan {
	return &MovePlan{TokenKept: kept, Apps: []IssuerMove{{App: "immich", Title: "Immich",
		From: "https://authentik.lan/application/o/immich/", To: "https://homestead.example.com/apps/auth/application/o/immich/",
		HostEntry: "homestead.example.com:host-gateway", Digest: "d1"}}}
}

// ADR-048 §6: the apps moving to Authentik's public address are bound
// into the issuance digest, and an approved plan re-applies them after
// the certificate is placed and the edge reloaded.
func TestIssuanceMovesBoundAndReapplied(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	before, err := rig.issuing.Plan("www")
	if err != nil || before.IssuerMoves != nil {
		t.Fatalf("no hook plan = %+v err %v", before.IssuerMoves, err)
	}
	moves := testMoves(true)
	var gotNames []string
	rig.issuing.PlanMoves = func(names []string) (*MovePlan, error) { gotNames = names; return moves, nil }
	var moved []string
	rig.issuing.MoveIssuers = func(_ context.Context, actor string, m MovePlan) error {
		// The certificate is placed and the edge reloaded first.
		if _, err := os.Stat(docker.CertDir(rig.stackDir, "www")); err != nil || rig.reloads == 0 {
			t.Errorf("moved before placement: %v reloads %d", err, rig.reloads)
		}
		for _, a := range m.Apps {
			moved = append(moved, actor+" "+a.App+" "+a.Digest)
		}
		return nil
	}
	plan := rig.approve()
	if plan.IssuerMoves == nil || plan.Digest == before.Digest || strings.Join(gotNames, ",") != "www.example.com" {
		t.Fatalf("plan = %+v, names %v", plan.IssuerMoves, gotNames)
	}
	// A preview with the same moves has the same digest; another list does not.
	c, _ := rig.issuing.certificateNamed("www")
	if p := rig.issuing.Preview(c, rig.routes, moves); p.Digest != plan.Digest {
		t.Fatalf("preview digest %s != plan %s", p.Digest, plan.Digest)
	}
	if p := rig.issuing.Preview(c, rig.routes, testMoves(false)); p.Digest == plan.Digest {
		t.Fatal("a different move list kept the digest")
	}
	issue, err := rig.issuing.IssueDefinition("www")
	if err != nil || len(issue.Steps) != 3 {
		t.Fatalf("IssueDefinition = %d steps, %v", len(issue.Steps), err)
	}
	def, err := rig.issuing.Definition("www")
	if err != nil || len(def.Steps) != 4 || def.Steps[3].Name != MoveStep {
		t.Fatalf("Definition = %+v, %v", def.Steps, err)
	}
	if err := rig.run(def); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Join(moved, ";") != "owner immich d1" {
		t.Fatalf("moved = %v", moved)
	}
}

// Without the kept token nothing changes in Authentik: the plan lists the
// apps, the operation has no move step. A failing move fails the step.
func TestIssuanceMovesWithoutTokenAndFailure(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	moves := testMoves(false)
	rig.issuing.PlanMoves = func([]string) (*MovePlan, error) { return moves, nil }
	rig.issuing.MoveIssuers = func(context.Context, string, MovePlan) error { return errors.New("Immich failed") }
	rig.approve()
	def, err := rig.issuing.Definition("www")
	if err != nil || len(def.Steps) != 3 {
		t.Fatalf("no token Definition = %d steps, %v", len(def.Steps), err)
	}
	moves.TokenKept = true
	rig.approve()
	def, _ = rig.issuing.Definition("www")
	if err := rig.run(def); err == nil || !strings.Contains(err.Error(), "Immich failed") {
		t.Fatalf("run = %v", err)
	}
	// The listing failing blocks the plan instead of hiding the moves.
	rig.issuing.PlanMoves = func([]string) (*MovePlan, error) { return nil, errors.New("catalog unavailable") }
	if plan, _ := rig.issuing.Plan("www"); plan.Ready || !strings.Contains(strings.Join(plan.Blockers, ";"), "catalog unavailable") {
		t.Fatalf("blocked plan = %+v", plan.Blockers)
	}
}

// ApproveVia records where the approval came from (the tunnel plan).
func TestApproveViaReachesOnApprove(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	var via string
	rig.issuing.OnApprove = func(a Approval) error { via = a.Via; return nil }
	plan, _ := rig.issuing.Plan("www")
	if _, err := rig.issuing.ApproveVia("www", plan.Digest, "owner", true, false, RenewalAuto, "tunnel plan abc"); err != nil {
		t.Fatal(err)
	}
	if via != "tunnel plan abc" {
		t.Fatalf("via = %q", via)
	}
}

// AddToStrategy adds a certificate once, keeps the others, and refuses a
// different certificate under a taken name.
func TestAddToStrategy(t *testing.T) {
	rig := newIssuanceRig(t, issuanceDirectory)
	pub := Certificate{Name: "public-dashboard", DNSNames: []string{"homestead.example.com"}, State: StatePending,
		Renewal: Renewal{Auto: true}, Issuer: Issuer{Name: "letsencrypt", Type: IssuerACME, Directory: LetsEncryptDirectory,
			Challenge: ChallengeDNS01, DNSProvider: "script"}}
	for range 2 {
		if err := AddToStrategy(rig.wizards, pub); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	set, err := LoadStrategy(rig.wizards)
	if err != nil || len(set.Certificates) != 2 || set.Certificates[1].Name != "public-dashboard" {
		t.Fatalf("strategy = %+v, %v", set.Certificates, err)
	}
	other := pub
	other.DNSNames = []string{"other.example.com"}
	if err := AddToStrategy(rig.wizards, other); err == nil {
		t.Fatal("a different certificate under a taken name was added")
	}
	// With no strategy saved, it starts one.
	fresh := wizard.New(rig.store)
	_ = fresh.Delete(TLSWizardID)
	if err := AddToStrategy(fresh, pub); err != nil {
		t.Fatalf("fresh add: %v", err)
	}
	if set, _ := LoadStrategy(fresh); len(set.Certificates) != 1 {
		t.Fatalf("fresh strategy = %+v", set.Certificates)
	}
}

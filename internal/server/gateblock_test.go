package server

import (
	"context"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/firewall"
)

type gateFake struct {
	live     firewall.GateState
	applied  int
	removed  int
	nft      firewall.State
	external bool
}

func newGateFake(t *testing.T) *gateFake {
	f := &gateFake{nft: firewall.State{NFTAvailable: true, RulesetAccessible: true}}
	oa, orm, ol, on := gateApply, gateRemove, gateLive, nftState
	oext := firewallExternal.Load()
	gateApply = func() error {
		f.applied++
		f.live = firewall.GateState{Present: true, Complete: true}
		return nil
	}
	gateRemove = func() error { f.removed++; f.live = firewall.GateState{}; return nil }
	gateLive = func() (firewall.GateState, error) { return f.live, nil }
	nftState = func() firewall.State { return f.nft }
	ext := func() bool { return f.external }
	firewallExternal.Store(&ext)
	t.Cleanup(func() {
		gateApply, gateRemove, gateLive, nftState = oa, orm, ol, on
		firewallExternal.Store(oext)
	})
	return f
}

func gateOn(t *testing.T, a *ssoApp, on bool) {
	set, _ := loadEdgeRoutes(a.st)
	set.Routes = []docker.Route{{Host: "kuma.lan", Target: docker.Target{Host: "kuma", Port: 3001},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}}
	if on {
		set.Routes[0].Gate = &docker.Gate{Canonical: "kuma.lan"}
	}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileGateBlockFollowsTheGates(t *testing.T) {
	a := appSignInRig(t)
	f := newGateFake(t)
	ctx := context.Background()

	gateOn(t, a, false)
	if err := reconcileGateBlock(ctx, a.st); err != nil || f.applied != 0 || f.removed != 0 {
		t.Fatalf("no gate, no table: %v %+v", err, f)
	}
	gateOn(t, a, true)
	if err := reconcileGateBlock(ctx, a.st); err != nil || f.applied != 1 {
		t.Fatalf("gate on applies the block: %v %+v", err, f.applied)
	}
	if err := reconcileGateBlock(ctx, a.st); err != nil || f.applied != 1 {
		t.Fatalf("in sync must not reapply: %v %+v", err, f.applied)
	}
	f.live = firewall.GateState{Present: true, Complete: false} // someone edited the table
	if err := reconcileGateBlock(ctx, a.st); err != nil || f.applied != 2 {
		t.Fatalf("an incomplete table is reapplied: %v %+v", err, f.applied)
	}
	gateOn(t, a, false)
	if err := reconcileGateBlock(ctx, a.st); err != nil || f.removed != 1 {
		t.Fatalf("last gate off removes the table: %v %+v", err, f)
	}
}

func TestReconcileGateBlockRefusesWhereItCannotApply(t *testing.T) {
	a := appSignInRig(t)
	f := newGateFake(t)
	gateOn(t, a, true)
	f.external = true
	if err := reconcileGateBlock(context.Background(), a.st); err == nil || !strings.Contains(err.Error(), "external") {
		t.Fatalf("external mode: %v", err)
	}
	f.external = false
	f.nft.NFTAvailable = false
	if err := reconcileGateBlock(context.Background(), a.st); err == nil {
		t.Fatal("no nft must error while a gate is on")
	}
}

func TestHostBlockProblem(t *testing.T) {
	f := newGateFake(t)
	if p := hostBlockProblem(); p != "" {
		t.Fatalf("available: %q", p)
	}
	f.external = true
	if p := hostBlockProblem(); !strings.Contains(p, "external") {
		t.Fatalf("external: %q", p)
	}
	f.external = false
	f.nft = firewall.State{NFTAvailable: true}
	if p := hostBlockProblem(); !strings.Contains(p, "privilege") {
		t.Fatalf("unprivileged: %q", p)
	}
	f.nft = firewall.State{}
	if p := hostBlockProblem(); !strings.Contains(p, "not available") {
		t.Fatalf("no nft: %q", p)
	}
}

func TestDoctorChecksTheHostBlockOnlyWhileAGateIsOn(t *testing.T) {
	a := appSignInRig(t)
	f := newGateFake(t)
	c := DoctorLiveConfig{Store: a.st}
	ctx := context.Background()
	gateOn(t, a, false)
	if got := gateBlockChecks(ctx, c); len(got) != 0 {
		t.Fatalf("no gate: %+v", got)
	}
	gateOn(t, a, true)
	got := gateBlockChecks(ctx, c)
	if len(got) != 1 || got[0].Status != doctor.StatusWarn || got[0].Fix == nil {
		t.Fatalf("absent must warn with guidance: %+v", got)
	}
	f.live = firewall.GateState{Present: true, Complete: false}
	if got := gateBlockChecks(ctx, c); got[0].Status != doctor.StatusWarn || !strings.Contains(got[0].Detail, "incomplete") {
		t.Fatalf("partial: %+v", got)
	}
	f.live = firewall.GateState{Present: true, Complete: true}
	if got := gateBlockChecks(ctx, c); got[0].Status != doctor.StatusOK {
		t.Fatalf("holds: %+v", got)
	}
	f.external = true
	if got := gateBlockChecks(ctx, c); got[0].Status != doctor.StatusWarn {
		t.Fatalf("external: %+v", got)
	}
}

package server

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"homestead/internal/catalog"
	"homestead/internal/doctor"
	"homestead/internal/firewall"
	"homestead/internal/store"
)

// Host block for gated apps (S421, ADR-055 §6 amendment 2026-11-01): an
// app whose own login is off must be reachable only through the edge. The
// firewall package renders the rules; this file decides when they apply
// (while at least one forward-auth gate is on), reconciles them, and
// reports them to the Doctor.

// firewallExternal is set when firewall_mode=external: Homestead then
// leaves nftables alone, so it cannot provide the block. MountFirewall
// installs the reader; the zero value means managed.
var firewallExternal atomic.Pointer[func() bool]

func gateBlockExternal() bool {
	if f := firewallExternal.Load(); f != nil {
		return (*f)()
	}
	return false
}

// Seams over nftables for tests.
var (
	gateApply  = firewall.ApplyGate
	gateRemove = firewall.RemoveGate
	gateLive   = firewall.LiveGate
	nftState   = firewall.Detect
)

// anyGateOn reports whether any edge route carries an Authentik gate.
func anyGateOn(st *store.Store) (bool, error) {
	set, err := loadEdgeRoutes(st)
	if err != nil {
		return false, err
	}
	for _, r := range set.Routes {
		if r.Gate != nil {
			return true, nil
		}
	}
	return false, nil
}

// reconcileGateBlock brings the host block in line with the gates: present
// on every Docker bridge while any gate is on, absent otherwise.
// Idempotent; a var so tests skip Docker and nft.
var reconcileGateBlock = func(ctx context.Context, st *store.Store) error {
	on, err := anyGateOn(st)
	if err != nil {
		return err
	}
	if gateBlockExternal() {
		if on {
			return errors.New("firewall_mode=external: Homestead does not manage the firewall, so the gated-app host block is not applied; add the equivalent rules to your own firewall (ADR-055 section 6) or switch the mode to managed")
		}
		return nil
	}
	if !nftState().NFTAvailable {
		if on {
			return errors.New("nftables is not available, so the gated-app host block cannot be applied")
		}
		return nil
	}
	live, err := gateLive()
	if err != nil {
		return err
	}
	if !on {
		if live.Present {
			return gateRemove()
		}
		return nil
	}
	if live.Complete {
		return nil
	}
	return gateApply()
}

// RunGateBlock reconciles the block at start and then every interval, so
// it returns after a reboot (nft rules do not persist), after Docker
// arrives late, and if someone removes the table. The rules are
// interface-based, so a recreated network needs nothing.
// Only a changed error is reported.
func RunGateBlock(ctx context.Context, st *store.Store, interval time.Duration, warn func(error)) {
	last := ""
	for {
		err := reconcileGateBlock(ctx, st)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != last && err != nil && warn != nil {
			warn(err)
		}
		last = msg
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// hostBlockProblem says why the host block cannot be provided right now,
// or "" when it can. A sign-in plan for an app that requires the block
// (forwardauth.requireshostblock) refuses on a non-empty answer.
func hostBlockProblem() string {
	if gateBlockExternal() {
		return "Homestead does not manage the firewall (firewall_mode=external), so it cannot keep other users off this app's port. Switch the firewall mode to managed, then plan again."
	}
	if state := nftState(); !state.NFTAvailable {
		return "nftables is not available on this host, so Homestead cannot keep other users off this app's port."
	} else if !state.RulesetAccessible {
		return "Homestead cannot manage nftables here (it needs privilege), so it cannot keep other users off this app's port."
	}
	return ""
}

// requiresHostBlock reads the manifest flag.
func requiresHostBlock(m catalog.Manifest) bool {
	return m.SSO.ForwardAuth != nil && m.SSO.ForwardAuth.RequiresHostBlock
}

// gateBlockChecks is the Doctor check that the host block holds while a
// gate is on. It warns, with guidance, when it is missing or
// incomplete.
func gateBlockChecks(ctx context.Context, c DoctorLiveConfig) []doctor.Check {
	const name = "gated-app-host-block"
	on, err := anyGateOn(c.Store)
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the edge routes could not be read")}
	}
	if !on {
		return nil
	}
	fix := liveFix("Homestead restores the block by itself within minutes when it is allowed to manage the firewall. To do it now, re-apply a gated app's sign-in or restart homestead-server; with firewall_mode=external, add the gate rules to your own firewall.",
		"/admin/firewall", "sudo nft -n list table inet "+firewall.GateTable)
	if gateBlockExternal() {
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "A gated app is on, but the firewall is managed outside Homestead (firewall_mode=external): other users on this machine can reach the app's port directly unless your firewall blocks them.", Fix: fix}}
	}
	if !nftState().RulesetAccessible {
		return []doctor.Check{unknownCheck(name, "the firewall could not be read (it needs privilege)")}
	}
	live, err := gateLive()
	if err != nil {
		return []doctor.Check{unknownCheck(name, "the gated-app firewall rules could not be read")}
	}
	switch {
	case timedOut(ctx):
		return []doctor.Check{unknownCheck(name, "the check did not finish in time")}
	case !live.Present:
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "A gated app is on but the host block is not in place: other users on this machine can reach container ports directly.", Fix: fix}}
	case !live.Complete:
		return []doctor.Check{{Name: name, Status: doctor.StatusWarn,
			Detail: "The host block for gated apps is incomplete (a rule is missing or was changed).", Fix: fix}}
	}
	return []doctor.Check{{Name: name, Status: doctor.StatusOK,
		Detail: "Non-root users on this machine, and devices routing to the container networks, cannot reach container ports directly (published ports still work)."}}
}

# S322 — Tunnel setup updates the dashboard's sign-in

## Goal
From the owner's VM 2 acceptance run (2026-10-27): after the tunnel was set
up, the public dashboard's login page showed no Authentik button. Sign-in
had been planned before the tunnel, so it held only the local origins. The
tunnel plan said to re-plan Sign-in afterwards, and that note was missed.

## Owner decision (2026-10-27)
- Setting up the tunnel also sets up sign-in on the public dashboard name,
  in the same approval (ADR-046, amending ADR-037).

## Scope
- The tunnel plan carries `sign_in_update` (origin, redirect URI,
  Authentik's public address) when the dashboard is published, sign-in is
  configured, the Authentik API token is kept, and sign-in does not yet
  cover the public name. The field is in the plan's digest.
- The tunnel setup operation gains a last `sign-in` step, only when the
  plan carries the change:
  - it re-plans sign-in from the configured one plus the public origin;
  - it checks the result against the approved change;
  - it approves and applies it with the kept token;
  - it audits `tunnel.sign-in` with the tunnel plan's digest.

  A failure leaves the tunnel up and says to re-plan Sign-in.
- Without a kept token the plan keeps the note and its link to Sign-in.
  When sign-in already covers the name, the plan says so.
- The dashboard's tunnel review and `homesteadctl tunnel plan` list the
  change.

## Rules
AGENTS.md slice rules apply. No routes, CLI commands or dashboard surfaces
are added or renamed, so PARITY is unchanged. ADR-046 is written first.

## Acceptance Criteria
- Go tests cover:
  - the three plan cases (kept token, no token, already covered);
  - the digest changing with the change;
  - the step running after the edge carries the public name;
  - a failed step keeping the tunnel up;
  - the real sign-in manager adding the public origin, redirect URI and
    Authentik address in fake Authentik, with the audit.
- Dashboard and CLI tests show the change.
- All dashboard, Go and Python checks pass; the embedded dashboard is
  rebuilt.

## Live check
Deploy to VM 2. The tunnel there is already up, and approving a plan is
the owner's to do. The owner either re-plans Sign-in, or plans the tunnel
again and approves it; the agent cannot approve either.

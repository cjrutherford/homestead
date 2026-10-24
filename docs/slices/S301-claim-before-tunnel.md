# S301 — Claim first, then the tunnel (documentation)

## Goal
A tunnel cannot be set up before claim: tunnel setup needs a signed-in
owner. Owner decision 2026-10-24: keep it that way. Since S289 the appliance
is claimed over the local network, so the tunnel is only needed first when no
browser can reach it locally. Document the order and the routes for that
case instead of adding a claim-token authorised tunnel setup.

## Dependencies
S288 (claim token), S289 (LAN dashboard), S290 (tunnel adoption). No ADR and
no code: behaviour is unchanged.

## Scope
- `docs/architecture/LOCAL_DASHBOARD.md`, "Claim first, then the tunnel":
  claim locally; for a remote or headless box, an SSH forward of the daemon's
  loopback port (`ssh -L 8080:127.0.0.1:8080`); last resort, a hand-laid
  tunnel adopted after claim with `tunnel setup --adopt`.
- `docs/architecture/BOOTSTRAP_TO_PUBLIC_TLS.md` Stage 2: claim is local.
- `docs/DEFERRED.md`: close the "tunnel before claim" item.

## Acceptance Criteria
- The order and both fallback routes are documented.
- The loopback listener serves the Claim page (checked on VM 2:
  `http://127.0.0.1:8080/claim` answers 200 with the dashboard).
- Main build remains green; `docs/PARITY.md` unchanged.

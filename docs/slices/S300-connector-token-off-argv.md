# S300 — Tunnel connector token off the command line

## Goal
The tunnel connector was started as `cloudflared … run --token ${TUNNEL_TOKEN}`,
so the run token sat in `/proc/<pid>/cmdline`, which every local user can
read. cloudflared reads the same value from the `TUNNEL_TOKEN` environment
variable, which the unit already loads from the root-only `token.env`; the
process environment is readable by root only.

## Dependencies
S246, S290 (the daemon writes `token.env`). No ADR: the token file, its mode
and the unit's role are unchanged.

## Scope
- `systemd/homestead-tunnel.service`: `ExecStart` ends at `run`; the token
  comes from `EnvironmentFile=/etc/homestead/tunnel/token.env` only.
- `scripts/install-systemd.sh`: `systemctl try-restart homestead-tunnel.service`
  after `daemon-reload`, so a running connector takes the changed unit; a
  stopped or never-enabled connector is left alone.
- `docs/DEFERRED.md`: close the connector-argv item, and the stale
  `tunnel setup --api-token/--account-id` item (S294 already refuses those
  flags with usage help).

Out of scope: platform updates (ADR-027) replace binaries only, so a unit
change still needs `install-systemd.sh`.

## Acceptance Criteria
- The connector's command line carries no token; its environment does.
- The connector reconnects after install, and the public names still answer.
- `tests/test_s300_connector_token.py` guards the unit and the install step.
- Main build remains green.

## Required Documentation
- `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`, `docs/SLICE_INDEX.md`.
  `docs/PARITY.md` unchanged (no route, command or surface change).

## Live Check (VM 2, 2026-10-24)
- Before: `/proc/<pid>/cmdline` (mode 444) ended `run --token <token>`.
- After `install-systemd.sh`: the connector restarted, its command line ends
  at `run`, `TUNNEL_TOKEN` is set in its environment (mode 400), it
  registered three connections, and
  `https://auth.hopefulaspirationsindustries.com/` answered 302 to the
  Authentik login flow.

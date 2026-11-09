# S307 — Install guide: bare metal to public apps

## Goal
One document that takes an appliance from an empty disk to the dashboard,
Authentik sign-in and apps served on the owner's domain through the
Cloudflare tunnel, plus the factory reset for shipping a unit. Before this,
the steps were spread across architecture notes, and three prerequisites
(Docker, cloudflared, the dashboard build) were written down nowhere.

## Dependencies
S220 (bootstrap), S246/S294 (tunnel), S286/S296 (sign-in), S287/S291/S297
(setup wizard), S290 (public app names), S306 (factory reset). No ADR:
documentation only.

## Scope
- `docs/INSTALL.md`: prepare the machine (Debian, mounted disks, Docker,
  cloudflared), install (source, dashboard build, bootstrap), claim on the
  LAN, the setup wizard in the order that ends public and signed in (Sign-in
  before Domain, re-plan Sign-in after the tunnel), app public names,
  backups, final checks, factory reset, troubleshooting.
- `README.md` points to it.
- `SETUP_WALKTHROUGH.md`: six steps (not five); the factory reset section
  names `scripts/factory-reset.sh`.

Out of scope (`docs/DEFERRED.md`): installing Docker from `bootstrap.sh` (the
S071 operation is unwired), building the dashboard during install, and
cloudflared packaging (already deferred).

## Acceptance Criteria
- `tests/test_s307_install_guide.py`: every script, doc path and
  `homesteadctl` command the guide names exists; the guide covers each part.
- The guide's flow matches the VM 2 walkthrough of 2026-10-25 (S303–S305).

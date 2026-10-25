# S306 — Factory reset for a new appliance

## Goal
Preparing an appliance for a new owner (a fresh order, or a returned or test
unit) needs one in-repo command that puts it back to the unclaimed state.
Until now this was a VM-only script outside the repo (DEFERRED, S287 KVM
findings: "wizard_state has no in-repo factory-reset command").

## Dependencies
S011 (systemd install), S012 (layout), S073 (edge proxy), S074 (stacks).
No ADR: an operator script; no API, CLI or dashboard change.

## Scope
- `scripts/factory-reset.sh --yes [--wipe-mount PATH]... [--reinstall]`,
  root-gated, refuses without `--yes`.
- Removes only what Homestead owns: its three units, the binaries, the
  Compose projects under `/opt/homestead/stacks` with their volumes, the
  `homestead-caddy` edge container, the `homestead-edge` network, the
  layout and runtime directories, the catalog copy, and the two groups.
  Other Compose projects, volumes and all images are kept (Compose stays
  usable without Homestead).
- Mounted storage is emptied only for paths named with `--wipe-mount`, each
  checked first to be an absolute, non-system mount point (`lost+found` is
  kept). Nothing is formatted or partitioned.
- `--reinstall` runs `scripts/install-systemd.sh` afterwards so the daemon
  is active and unclaimed.

Out of scope: the Cloudflare tunnel and DNS records a previous owner made
(they live in that owner's Cloudflare account); the `cloudflared` package
and docker itself are left installed.

## Acceptance Criteria
- `tests/test_s306_factory_reset.py`: syntax, Makefile lint, argument
  handling, root gate, confirmation and wipe guards come before any removal,
  no prune or image removal, units and layout dirs match the repo.
- On VM 2: reset with the three mounts wiped and `--reinstall`; the daemon
  is active and https://homestead.local shows the Claim page.

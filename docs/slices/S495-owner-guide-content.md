# S495 - Owner guide content

## Goal and dependencies
Owner 2026-11-08: fill the S494 guide skeleton for an owner who runs one appliance at home. Depends on S494 and the accepted ADRs 043 to 062.

## Added
- Index and eight sections in `docs/guide/`: Getting started (install, claim, manual first-run Authentik setup, dashboard), Apps and the catalog (install, sign-in, apps page, app groups, craft apps and their save limits, open LAN), People and sign-in (grants, closed by default, recreate, recovery, step-up), Names, tunnel and public access (local names, tunnel, `/apps/<app>/` versus own names, shared gate, plan/approve/audit), Storage and backups (mounted storage, nightly backups, recovery key and rotation, restore), Updates and releases (signed updates, trusted keys, rotation), Troubleshooting (Doctor, warnings, FreshRSS owner limit), Command and page reference.
- The reference is hand-written, one row per top-level `homesteadctl` command with its dashboard page. The test checks it covers every top-level command.
- Rules kept: example.com only, no secrets, no real hostnames.

## Not built
The reference is not generated from `docs/PARITY.md`; that file is linked from the page and already has its own parity test.

## Verification
`tests/test_s495_guide_content.py`: every page builds, internal links resolve, and every `homesteadctl` command mentioned exists (top-level commands against `cmd/homesteadctl/run.go`, subcommand words against the Go sources). Live on VM 2 (PLAT-DASH-4).

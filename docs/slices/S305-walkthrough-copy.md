# S305 — Fresh-install walkthrough: install and backup review wording

## Goal
Three things the fresh-install walkthrough (VM 2, 2026-10-25) showed that
contradict what the screen or the plan does:
- The install wizard's sign-in step said installing does not change sign-in
  and to connect it after the install, while the review then offered "Sign
  in with Authentik" as part of the install (S298).
- The nightly policy review said "Back up every app below" with no list
  below; the plan's `covered` and `skipped` were not shown (the CLI prints
  them).
- The policy time field was labelled "24-hour" while the browser's time
  picker can show AM/PM.

## Dependencies
S291 (embedded install wizard), S297 (nightly policy), S298 (sign-in at
install). No ADR: wording and display only.

## Scope
- Install wizard sign-in step: the review offers Sign in with Authentik where
  Homestead can connect it; otherwise connect it after the install.
- Nightly policy review lists the plan's covered apps and the ones it leaves
  out, with reasons.
- The time label names only the time zone.

Out of scope: none discovered.

## Acceptance Criteria
- `install-wizard.spec.ts` and `nightly-policy.spec.ts` cover the new text
  and the plan's coverage list.
- Main build remains green; `docs/PARITY.md` unchanged (no route or surface
  change).

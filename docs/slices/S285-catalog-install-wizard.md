# S285 — Catalog install wizard

## Goal
Every catalog package installs through the same explicit, plain-language step sequence, so the owner always knows what an app is, what it needs, where it will live, who can reach it, and what happens next.

## Dependencies
S283 (the work-orders rail and the shared step-list component). S284 no longer owns the install flow (see S284).

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Scope addition (owner-approved 2026-10-18): the dashboard gains an Uninstall action. It sits on each installed app's wizard done page and next to every installed app in the catalog list, uses the existing `POST /api/v1/catalog/{name}/uninstall` route behind an S282 danger dialog, says in plain words what is removed and what is kept (including the remove-data option with every data root named in full), and shows progress in the work-orders rail. `docs/PARITY.md` flips the uninstall row from CLI-only to both surfaces. No route or CLI command is added or changed. The requirements step also reads the existing `GET /api/v1/docker/health` for Docker and edge readiness, so that row gains a dashboard consumer (`dockerHealth`).

Dashboard only. The existing install API (`POST /api/v1/catalog/{name}/plan`, `.../install`, `.../configure`, secrets routes) and its plan semantics are preserved unchanged: the wizard calls the same routes in the same order the current install flow does, and the plan the owner reviews is the plan that is applied. CLI parity (ADR-004) is unaffected: no route, command or parity row changes; the CLI installs exactly as before. Governed by ADR-004, ADR-006, ADR-013, ADR-014 and ADR-018, all preserved.

## Acceptance Criteria
- Catalog install wizard implemented and documented; the old inline install panel is removed, not kept beside it
- Steps, in order, for every package (a step that does not apply to a package is omitted, not shown empty):
  1. What it is: name, one-line purpose and what it does, from the manifest plus curated copy
  2. Requirements and preflight: dependencies, ports, memory/disk hints, Docker and edge readiness, and any failing preflight check with its fix; the wizard does not advance past a failing hard requirement, and a soft warning can be acknowledged
  3. Storage placement: shown when the manifest declares storage; choose among registered mounts (ADR-013), see free space and the owner-visible path; mounted storage only, no formatting or partitioning
  4. Address and exposure: local hostname and edge route; local-only is the default; any public exposure is shown as a plan to review and is never enabled by this step on its own (ADR-006)
  5. Secrets and sign-in options: shown where the manifest supports them (generated secrets with what they are for; the `sso` class: `optional` offers the choice, `unsupported` says so in plain words); no secret value is displayed or logged
  6. Review: the generated plan from the plan route rendered in plain language with the raw plan expandable, listing what will be created, changed and left alone
  7. Install: starts the install operation and shows live progress with the shared step list, and the operation appears in the S283 work-orders rail; leaving the wizard does not stop it
  8. Done: success state with next steps (open the app, first-login hints from curated copy, where its data lives, how to back it up); a failure state says what happened, links to the operation detail and offers retry
- Helper text: each step has a one-sentence plain explanation drawn from manifest fields where they exist (title, description, ports, storage, sso class, requirements) and from a curated copy table in the dashboard for what manifests do not say (first-login hints, per-app notes); a test fails when a catalog package lacks curated first-login copy, so a new package cannot ship without it
- Resumable within the session: Back and Next keep entered choices; a reload on the review step re-plans rather than reusing a stale plan, and the plan shown is always the one that will be applied
- Authentik installs through the same wizard with no special case beyond its curated copy; its `sso` class is explained as "this is the sign-in service"
- Keyboard and screen-reader support: step list with current step announced, focus moved to the step heading on change, errors tied to fields; works at 390 px and 1440 px, light and dark
- Component specs: step sequencing per package shape (no storage, with storage, `sso: optional`, `sso: unsupported`), blocked and acknowledged preflight, storage selection, plan rendering, install start and rail entry, failure and retry, missing-curated-copy test
- KVM review on VM 2: install one small app and one app with storage through the wizard, navigate away and back during install, confirm rail tracking, the done step and the first-login hint; uninstall afterward and leave VM 2 as found; findings and screenshots in `docs/DEVELOPMENT.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update the dashboard design doc with the wizard pattern (step sequence, helper text sources).
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/PARITY.md`: uninstall row to both surfaces, docker health row gains `dockerHealth`; state that the plan and install routes are unchanged.
- `docs/DEVELOPMENT.md`: S285 section with the KVM review summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

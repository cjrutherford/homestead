# S284 — Dashboard page passes

## Goal
Apply the S282 foundation and S283 state system page by page, so each page leads with what the owner needs and sets risky actions apart.

## Dependencies
S283, S285, S286, S287 (the page passes land last so they style the install wizard, Sign-in and Setup pages too)

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Presentation and copy only: no route, API, CLI or behaviour change. Copy follows the dashboard voice rules from S282 (plain verbs, sentence case, the user's vocabulary over the system's; errors say what happened and how to fix it; empty states invite the action).

## Acceptance Criteria
- Dashboard page passes implemented and documented
- Status: problems first ("Needs attention" with each warn/fail check, its explanation and the fix action or command), then a collapsed "All good" count; daemon, claim state and version as a compact header; the CLI commands in diagnostics shown as copyable code
- Catalog list and Craft apps (the catalog install flow moved to S285, the install wizard, and is not touched here): app entries with icon, one-line purpose, state badge, and install/open actions; craft browser advisories in the S252 component, restyled; avoid a uniform card grid where a list reads better
- Tables: Operations, Audit, Storage mounts, Backups jobs and schedules, Edge routes, Trust leaves, Craft files as real tables with sortable or grouped columns where useful and the S282 badges
- Risky actions set apart: Deploy Caddy, Reconcile routes, Apply firewall, rotation, re-key, volume removal and tunnel teardown use the danger or confirm patterns with the change preview next to the button
- Forms: Account, Backups destination, Tunnel setup, Craft files link, Exposure plan use the S282 field layout with inline validation messages
- Every page reviewed for copy against the voice rules; system-internal names replaced where a user term exists (keep command names and identifiers exact)
- Component specs updated for every touched page; no behaviour regression in existing specs
- KVM visual review on VM 2 at 1440 and 390 px, light and dark, every route; findings and screenshot set recorded in `docs/DEVELOPMENT.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Dashboard design doc: page patterns (triage list, app list, data table, risky action).
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/PARITY.md`: confirm no row changes are needed.
- `docs/DEVELOPMENT.md`: S284 section with the review summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

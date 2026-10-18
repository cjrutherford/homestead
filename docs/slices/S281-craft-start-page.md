# S281 — Craft apps start page

## Goal
A Homestead-owned "Craft apps" start page lists the installed craft apps with their local HTTPS URLs, replacing the dropped upstream Craft Launcher, without any external network call.

## Dependencies
S254 (or S253 if it lands first; the page needs only installed craft packages and edge route state)

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-031 (Accepted 2026-10-17, option (b): a daemon-derived dashboard page with CLI and API parity).
Background: Craft Launcher (S253) was removed from the catalog by owner decision on 2026-10-17 because it has a compiled-in upstream suite list with GitHub download buttons, cannot list installed apps, and calls `api.github.com` 14 times per load.

## Acceptance Criteria
- Craft apps start page implemented and documented
- API route, CLI command and dashboard page with configuration parity through the same API: lists installed craft packages (the catalog's craft packages that are installed) with title, version, status and local HTTPS URL taken from catalog and edge route state; an installed app without a route is listed without a URL and says why
- No external network calls from the daemon or the page, verified by a test and by the browser network log on KVM; no third-party assets
- Route classification (ADR-020): the new route is classified in `internal/server/boundary.go` `routeClasses`; the route-classification test fails otherwise; read-only, no mutation
- Lists only installed apps; installing or removing a craft app is reflected on the next load with no regeneration step
- `docs/PARITY.md` updated for the added route, CLI command and dashboard surface; the parity test is green
- Tests: empty list, several installed apps, app with and without a route, removed app, route classification, CLI output, dashboard component
- KVM: install two craft apps, open the page, confirm each link opens over S280 HTTPS, uninstall one and confirm it disappears, confirm no request leaves the appliance
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- ADR-031 is Accepted (option (b)); implementation follows it.
- `docs/PARITY.md`: add the rows for the route, CLI command and dashboard surface.
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

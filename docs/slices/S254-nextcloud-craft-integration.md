# S254 — Nextcloud Craft Integration

## Goal
Craft apps open and save documents in the owner's Nextcloud through a daemon WebDAV proxy that holds a sealed app password, so documents live in customer-owned storage and no Nextcloud credential reaches the browser (ADR-029).

## Dependencies
S249,S170,S266,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-029 (Accepted 2026-10-14), ADR-021 and ADR-020. Dependencies relaxed from S253: the proxy needs one craft package (S249), the Nextcloud package (S170), secret-backed variables and the secret store (S266, ADR-021) and enforced sessions (S257), not the media apps.
Homestead enters the file data path; the ADR-029 consequences (authorisation, size limits, streaming, audit without content, same-origin design) are acceptance criteria, not follow-ups.

## Acceptance Criteria
- Nextcloud Craft Integration implemented and documented
- Link flow (API, CLI and dashboard parity): owner supplies a Nextcloud account name and app password, or approves daemon creation of one through Nextcloud's app-password endpoint; the value is stored as an ADR-021 secret, never returned by any route, never in YAML, logs, audit, drift or configuration export; rotation and unlink go through the plan and approve flow
- Proxy implementation in a new `internal/craftfiles` package mounted from `internal/server`: fixed WebDAV method subset (`PROPFIND`, `GET`, `PUT`, `MKCOL`, `DELETE`, `MOVE`, `COPY`, `HEAD`), one owner-configured Nextcloud root folder, path normalisation and traversal refusal, requests only to the linked Nextcloud, streamed bodies in both directions with no whole-file buffering
- Authorisation: dashboard action authenticated by operator session and CSRF mints a one-time launch code; the craft host's forwarded `/_homestead/files/` path exchanges it for a craft-scoped `HttpOnly`, `SameSite=Strict` cookie bound to the operator session, expiring no later and revoked with it; no other credential is accepted; unknown or expired cookies get 401
- Route classification (ADR-020): every new route is classified in `internal/server/boundary.go` `routeClasses` with a new craft-session class; the route-classification test fails otherwise
- Same-origin design: no CORS headers, cross-origin requests and preflights rejected; verify that the edge route reconciler (`/api/v1/edge/routes`, edge code in `internal/server`) can forward a path prefix on a craft host to the daemon; if it cannot, extend it here with drift and validation support, tested
- Limits: configurable maximum upload size (default 512 MiB), request timeout, per-session concurrency cap; exceeding them returns 413 or 429 with tests using small configured limits
- Audit without content: each file operation audited (session, craft app, method, normalised path, status, byte count), reads aggregated per path and minute; tests assert no content, app password, cookie or launch code appears in audit, logs, or error bodies
- Craft app side: the vendored static apps are not modified; a small Homestead-provided adapter file added in the HAI image build (S279 Dockerfile) maps the app's file-provider hooks to `/_homestead/files/`; if an app exposes no hook, record it in `docs/DEFERRED.md` and ship that app browser-local only
- Documents default to Nextcloud once linked, with an explicit per-app local-only opt-out stored as desired config; unlinked apps keep working browser-local
- Exposure default revisit: with the proxy in place the recommendation for craft hosts stays local-only and the `allowwider` warning text is updated to explain what leaves the LAN; this slice changes `allowwider` only if the owner confirms in the slice review
- Stateless rule unchanged (ADR-028): craft packages remain stateless backup no-ops; documents are backed up through Nextcloud's own backup (S182)
- `docs/PARITY.md` updated for every added route with the session class noted; parity test green
- Tests: proxy against a fake WebDAV server (all methods, traversal attempts, oversize body, streaming of a body larger than test memory limit, upstream errors, credential never sent to the client), session exchange (valid, reused, expired, revoked by logout and password change with an injected clock), CORS refusal
- KVM: install Nextcloud (S170) and Wordcraft on the appliance, link an app password, open Wordcraft from the dashboard, save a document and confirm it appears in Nextcloud under the root folder, open it again from a second browser session after relaunch, confirm `docker inspect` and browser dev tools show no Nextcloud credential, confirm an unlinked browser profile gets 401 on the proxy path, revoke the app password in Nextcloud and confirm the proxy fails closed with a clear status
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/architecture/`: craft file proxy guide (flow, limits, audit, same-origin edge forwarding, revocation).
- `docs/DEFERRED.md`: Nextcloud OAuth instead of app passwords, per-user (not single-account) Nextcloud mapping, file locking and conflict resolution beyond last-writer-wins with ETag checks, apps without file-provider hooks.
- `docs/PARITY.md`: confirm no row changes are needed, or add/flip the rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.

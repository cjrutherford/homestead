# S334 — Authentik step-up on the public address

## Goal
ADR-049 §1–2. The dashboard on a public name takes no local password, so
the five People actions that normally ask for it (password reset,
second-factor reset, app-admin grant, removal, settling drift) could not be
done there. A fresh Authentik sign-in now stands in for the password.

## Scope
- `POST /api/v1/sso/step-up` (session class, allowed on public names) with
  body `{"return_to": "/path"}` starts an Authentik ceremony of the new
  kind `step-up`, with `max_age=30` (never `prompt=login`), bound to the
  caller's oidc session and to the browser (state cookie). It returns the
  authorize URL. Password sessions get 409; `return_to` must start with a
  single `/`, with no `//`, backslash, scheme or control characters.
- The callback checks `auth_time` is within `FreshWindow` (a missing one
  fails closed), the subject is the linked owner's, the state cookie
  matches and the bound session is still that owner's live oidc session.
  It then sets `sessions.stepped_up_until` to now plus five minutes
  (migration 55), creates no session, audits `sso.step-up`, and redirects
  to `return_to`. Failures redirect to `/?sso_error=` with a fixed code
  (`stale`, `session`, `unlinked`, `state`).
- Boundary: on a public name an oidc session passes the five People
  step-up routes only while stepped up and unexpired; otherwise 403 with
  `X-Homestead-Error: stepup-required`. Each pass is audited
  (`session.step-up.authorised`, "authorised by Authentik step-up"). Every
  other step-up route, and the local password header, behave as before on
  public names; local and overlay names are unchanged.
- Dashboard: on `stepup-required` the re-authentication dialog shows
  "Sign in with Authentik again" instead of the password field and sends
  the person to Authentik, returning to the current path.

## Rules
AGENTS.md slice rules apply. PARITY: the route is dashboard-only; the CLI
has no browser and keeps the local password.

## Acceptance criteria
- Go: stale, missing and future `auth_time`, another subject, missing or
  foreign state cookie, session/owner mismatch, single use, expiry and
  replacement of the state; `ValidReturnTo`; `MarkSteppedUp`; the boundary
  matrix (public stepped-up People routes pass, other step-up routes
  refused, not stepped up gives `stepup-required`, expired gives it again,
  local unchanged); the full ceremony marks the session and mints none.
- Dashboard specs: the dialog, the navigation, the failure message and the
  interceptor.
- All Go, Python and dashboard checks pass.

## Live check
On VM 2, on the public dashboard name, remove a test person: the dialog
offers Authentik, the return lands on the People page, and the action
succeeds; after five minutes it asks again.

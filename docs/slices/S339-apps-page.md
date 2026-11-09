# S339 — The apps page and /admin

## Goal
The dashboard's root is an apps page for everyone who signs in; administration
moves under `/admin` (ADR-050 §4).

## Scope
- `/` is the apps page (`dashboard/src/app/home/`): greeting from
  `GET /api/v1/me`, tiles from `GET /api/v1/me/apps` (icon and name, a plain
  link opening the app in the same tab), an empty state, sign-out, and an
  Administration link for the owner only.
- All administration screens moved under `/admin/…`; old paths redirect with
  params, query and fragment. Setup, claim, login, sign-in and account stay
  at the root (the server and the walkthrough link to them by path).
- Guards: `homeGuard` (signed-out to `/login`; owner still passes the setup
  gate) and `ownerGuard` (a person goes to `/`) on `/admin`, setup, sign-in
  and account.
- Shell: sidebar, drawer and work-orders rail only for the owner off the apps
  page. For a person the shell asks `/me` first and makes no owner calls
  (identity, operations, setup status). The SSO login finish asks `/me`, not
  the owner-only lockout status.
- Server: doctor and app-setup fix links, and setup error messages, name
  `/admin/…` paths. SSO redirects (`/login`, `/sign-in`, `/` with `sso_error`)
  are unchanged.

## Acceptance
Dashboard specs for routes and redirects, guards, owner versus person
rendering, the empty state and a person making no owner calls; Go tests for
the doctor link change.

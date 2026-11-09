# S338 — Person sessions

## Goal
A person signs in with Authentik and gets a session that can do one thing:
see their apps (ADR-050 §1–§3). Everything else stays the owner's.

## Scope
- Sign-in: the SSO callback matches the subject (`sub`, Authentik's hashed
  user id, which equals the user `uid`) to a person through the
  `people.authentik_uid` Homestead stored when it created or adopted the
  user. Never a username or email claim. The issuer must be the linked
  issuer. The owner's linked `sub` still signs in as the owner; inactive,
  disabled or unmatched subjects stay `unlinked`. A record with no stored
  uid is matched once through the Authentik API by its pk and remembered.
- Migration 56: `sessions.role` (`owner` default) and `sessions.person_id`,
  `people.authentik_uid`. A person session has no identity row, so it cannot
  be mistaken for the owner. `Verify` re-checks the person is active on every
  request; a dead person's session row is deleted.
- Audit: `session.login` (actor is the person's username; detail names the
  person id and display name) and `session.logout`.
- Revocation: disabling or removing a person (and a recreate of a disabled
  one) ends their sessions; unlinking Authentik ends all person sessions.
- Boundary: a person session reaches only `GET /api/v1/me`,
  `GET /api/v1/me/apps` (their own list) and `DELETE /api/v1/sessions`, plus
  the static dashboard. Every other route, including step-up routes, SSE and
  websockets, answers 403 `owner-only` (`X-Homestead-Error`).
  `requireOperator` repeats the check in the handlers. A test walks every
  registered route.
- Authentik: group `homestead-people`; the dashboard application is bound to
  `homestead-owner` and `homestead-people`. Both are in the spec and digest;
  an earlier sign-in gets a re-plan reason naming the people group. Apply has
  a `sync-people` step. People add, adopt, enable, disable, remove, put back
  and recreate keep membership in step.
- Dashboard: `SessionService.role`/`loadRole()` from `GET /api/v1/me`;
  `owner-only` answers mark the person role without expiring the session.

## Acceptance
Go tests: sign-in matching and wrong issuer, owner unaffected, person
session end to end with audit, deny-by-default over every route, session
death on disable, group sync, plan/apply/digest; dashboard spec tests.

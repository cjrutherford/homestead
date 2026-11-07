# S446 - Memos: no first-account screen

## Goal and dependencies
ADR-059: the first Memos account becomes its administrator, so a stranger could claim it. Depends on S442-S445.

## What changed
- `internal/firstrun/memos.go` (routine `memos`): waits for `GET /api/instance/profile`; when `needsSetup` is true, creates `homestead-admin` through the Connect `UserService/CreateUser` call (no sign-in needed during setup; Memos makes the first user ADMIN and the routine checks the role in the reply). The generated password is sealed first (revealable `admin_password`, reused on retry) and travels on stdin via busybox `wget --post-file /dev/stdin`, never argv. An app that no longer needs setup is left alone.
- `catalog/memos.yaml`: first-run block, revealable secret, and the declarative file now manages the `GENERAL` instance setting. Sign-in on: `disallowPasswordAuth` true (anonymous password sign-up is refused, registration stays open for Authentik's first sign-in, which is blocked by `disallowUserRegistration`). Sign-in off (also the starting value): registration closed. The administrator signs in with its password at `/auth/admin` (the admin sign-in survives `disallowPasswordAuth`). The owner's Authentik account stays an ordinary user: Memos never gives OIDC users a role.
- Verified against the pinned image 26.10.0 on VM 2: needsSetup, CreateUser role, stdin POST, setup allowed with registration closed, the on/off file transitions apply on restart, anonymous sign-up refused in both states, Authentik first sign-in works with password auth off and is refused with registration closed.
- Dashboard curated copy, checklist SRV-MEMOS-1a, DEFERRED notes.

## Tests
`internal/firstrun/memos_test.go`, `internal/appsso/memos_test.go`, `tests/test_s446_memos_first_run.py`.

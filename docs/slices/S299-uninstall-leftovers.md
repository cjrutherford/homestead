# S299 — Uninstall keeps no unreachable leftovers

## Goal
Uninstalling an app must not strand its public names or its sign-in. Both are
removed through plans that need the app installed (S290, S298), so once the
app is gone they cannot be removed through Homestead. Uninstall refuses while
they exist and names the steps to remove them first.

## Dependencies
S298, S290. ADR-039 §3, ADR-040, ADR-041. No new ADR: every exposure change
still goes through its own approved plan; uninstall only refuses.

## Scope
- `POST /api/v1/catalog/{name}/uninstall` answers 409 while:
  - the app's edge routes carry public tunnel names. The message lists them
    and the `apps public plan <app> --off --hostname NAME` step. There is no
    override: removal needs only the stored Cloudflare token.
  - sign-in with Authentik is recorded for the app. The message names
    `apps sign-in plan <app> --off`. `leave_sign_in: true` acknowledges
    leaving the provider and application in Authentik (for when Authentik is
    unreachable). The uninstall operation then forgets the sealed client
    secret and the sign-in record in a final step. Audited
    `app.uninstall.leave-sign-in`, never with a secret.
  - the app is Authentik and other apps have sign-in recorded. The message
    lists them. There is no override.
- CLI: `homesteadctl apps uninstall … [--leave-sign-in]`.
- Dashboard: the uninstall dialog shows the refusal. When sign-in is the
  only reason, it offers "Uninstall anyway and leave its sign-in in
  Authentik".

Out of scope: Authentik's own public name (owned by the tunnel plan), the
dashboard's sign-in when Authentik is uninstalled, and removing leftovers
in a single uninstall operation.

## Acceptance Criteria
- Each refusal leaves the app installed and changes nothing.
- `leave_sign_in` uninstalls, then the sealed secret and the record are gone.
- Automated tests cover the three refusals and the acknowledgement.
- Main build remains green.

## Required Documentation
- `docs/DEFERRED.md` (close the S290 and S298 uninstall items),
  `docs/DEVELOPMENT.md`, `docs/PARITY.md` (no route change).

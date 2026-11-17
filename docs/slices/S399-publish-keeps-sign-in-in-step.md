# S399 — Publishing an app keeps its sign-in in step

## Status
Complete (automated tests). Live check on VM 2 pending.

## Bug
Jellyfin had app sign-in on and was given a public name through
`app.public.add`. Its sign-in stayed on the local issuer
(`https://homestead.lan/apps/auth/...`) with no public redirect URI, so public
visitors were sent to the local Authentik. `apps sign-in status` only said
"re-plan: the app's names changed". The issuer is chosen when the sign-in plan
is built (internal/appsso/appsso.go): public when the app is published and
Authentik's public name is served. It moved automatically only when a
certificate was issued (ADR-048 §6).

## Decision
No architecture change (ADR-047/048 unchanged). The public plan previews the
sign-in plan for the names after the change and the operation applies it.

- Public plan: when the app has an app_sign_in record, the Authentik API token
  is kept, and sign-in would change, the plan builds the sign-in plan over a
  copy of the edge table with the name added or removed (the same technique as
  the install-time setup inputs). `sign_in` now reads "App sign-in ... moves
  to the public issuer <url> (now <old>); adds redirect URI ..." and
  `sign_in_digest` carries that plan's digest. It is part of the public plan
  digest, so approving the plan approves the sign-in change.
- Operation: a final `sign-in` step runs after the edge/connector steps, through
  the existing `reapplyAppSignIn` (the app sign-in apply path, audited as
  app.sign-in) with the approved digest. A stale digest fails it.
- Failure: the operation fails with "<app> is public, but its sign-in still
  uses the old issuer: <reason>; re-plan it from its card ..." (or "is no
  longer public" on removal). The name change is not rolled back.
- Unpublishing reverts to the local issuer the same way.
- Without a record, without a kept token, or when nothing in sign-in changes,
  nothing is added: the plan keeps the earlier "re-plan afterwards" text.
- `apps sign-in status` and the sign-in plan carry `public_sign_in_url`; the CLI
  prints "public sign in at: ..." beside the local address.

## Tests
internal/server/apppublic_signin_test.go: publish shows the change, digest
covers it, the stored record has the public issuer and redirect URI; unpublish
reverts to the local issuer; no sign-in record leaves the operation and plan
unchanged; a step failure is reported after the name is live. CLI status line.
The real Authentik/compose apply is replaced by the real plan, digest check and
record save, as other server tests do.

## Not in this slice
App path moves (`appmove`) still leave sign-in to a re-plan; the token-not-kept
case; see DEFERRED. No routes, commands or surfaces were added or renamed
(new JSON fields only), so PARITY.md is unchanged.

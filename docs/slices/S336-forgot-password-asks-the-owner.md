# S336 — Authentik's login page tells people to ask the owner

## Goal
ADR-049 amendment (2026-10-28): forgotten passwords go to the owner, who
resets them from People. No self-service link, no outbound email.

## Finding
Authentik 2026.8.3 (`authentik/stages/identification/stage.py`) sets the
challenge's `recovery_url` only `if current_stage.recovery_flow`. The
brand's `flow_recovery` is not read there, so S333's brand-only setting
shows no "Forgot username or password?" link. Homestead leaves the stage
alone, and the brand's flow keeps serving one-time links.

## Scope
- The dashboard sign-in spec carries `login_hint`. Plan and apply set the
  title of `default-authentication-flow` to "Forgot your password? Ask the
  owner of this Homestead to reset it." The plan lists a "Login page hint"
  object; a title already equal is unchanged and posts nothing; a missing
  flow is skipped. The digest covers the field. App specs never set it.
- Sign-ins applied before S336 show a re-plan reason on the Sign-in page.

## Rules
AGENTS.md slice rules apply. No routes, commands or surfaces; PARITY is
unchanged.

## Acceptance criteria
Go tests with the fake Authentik: title set once, idempotent, skipped
without the flow, identification stage untouched, app specs untouched,
digest changes, re-plan reason. All checks pass.

## Live check
On VM 2, re-plan and apply Sign-in; the Authentik login page shows the
hint and no recovery link. Not done in this slice.

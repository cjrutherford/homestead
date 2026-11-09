# S466 - The "Forgot password?" link no longer ends in a dead end

## Goal and dependencies
PLAT-PEOPLE-6 (acceptance run, medium). Depends on S333 (recovery flow) and S336 (sign-in hint). Authentik 2026.8 shows "Forgot password?" on the public sign-in whenever the default brand has a recovery flow; it opened a reset form that failed with "No user found".

## What changed
- `internal/sso/recovery.go`: the recovery flow gets the expression policy `homestead-recovery-owner-links-only`, bound to the flow (target is the flow's policy-binding-model id). It allows `is_restored` (a one-time link) and a plan with a pending user (the administrator's `core/users/{pk}/recovery/` call); otherwise it denies with "Ask the owner of this Homestead to reset your password." Found by name, expression corrected on drift, binding added if missing; a recovery flow the owner set on the brand is untouched. The plan shows a recovery-flow update for appliances set up earlier, so re-planning and applying sign-in fixes them.
- ADR-049 amendment: the link stays (Authentik offers no way to hide it with a brand flow set) but ends in the ask-the-owner message.
- No route, command or surface change; `docs/PARITY.md` unchanged. Checklist PLAT-PEOPLE-6 text updated.

## Live (VM 2)
Fresh profile: public sign-in shows the hint; "Forgot password?" now shows "Request has been denied. Ask the owner of this Homestead to reset your password." An owner-issued link for a temporary person opened the reset form, set a password and signed in; LAN sign-in unaffected.

## Tests
`internal/sso/recovery_test.go` (policy created, bound, idempotent, drift corrected, owner flow untouched), `tests/test_s466_recovery_owner_links_only.py`.

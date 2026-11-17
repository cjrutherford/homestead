# S486 - Tighten the parity test

## Goal and dependencies
Enforce the owner rule (2026-11-08): every `homesteadctl` action that goes through the API has a real dashboard control; only `claim token`, `login --print-token` and root-socket `admins/recover` stay CLI-only. Depends on S483-S489.

## What changed
- `tests/test_s037_parity.py` no longer counts spec files as proof a dashboard symbol exists.
- `tests/test_s486_parity_flags.py` (new): every non-`n/a` dashboard symbol in `docs/PARITY.md` needs a non-spec call site, and an API wrapper needs a caller outside `api.service.ts`; a row with a CLI token and a dashboard `n/a` fails unless it is `admins/recover`.
- `docs/PARITY_FLAGS.md` (new): every `homesteadctl` flag (source file, flag, command, request field, dashboard field or `n/a` with a reason). The test parses flags from `cmd/homesteadctl/*.go` (`flag.NewFlagSet` calls and hand-rolled `"--x"` cases) and fails on any flag missing from, or unknown to, the table; each named dashboard field must exist in non-spec code. Allowed `n/a` reasons: `output`, `plan-digest`, `actor` (`--actor` only), `route` (`--route` only), `session`, `retired`, `cli-only` (three flags).
- `docs/PARITY.md` header states the rule.

## Findings
No UI gap surfaced: the suspected people-remove gap is not one (`POST /api/v1/people/{id}/remove` is a wrapper called from the People page). No dashboard or daemon change.

## Verification
`python3 -m pytest tests` and the new test.

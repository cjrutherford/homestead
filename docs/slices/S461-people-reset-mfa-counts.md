# S461 - people reset-mfa reads the daemon's device counts

## Goal and dependencies
Owner acceptance PLAT-PEOPLE-4. Depends on S333 (people reset). `homesteadctl people reset-mfa A` removed the authenticators in Authentik and then failed with `decode response: json: cannot unmarshal number into Go struct field SecondFactorReset.removed of type []string`, because the daemon answers `{"removed": N, "skipped": M}` (counts) while the client and dashboard types expected string arrays.

## What changed
- `internal/apiclient/people.go`: `SecondFactorReset.Removed` and `Skipped` are counts.
- `cmd/homesteadctl/people.go`: prints "N removed" and ", M left".
- Dashboard People service and page read counts (the message was always "0 removed" because a number has no `length`).
- No route changed, so `docs/PARITY.md` is unchanged.

## Tests
`cmd/homesteadctl/people_test.go` (stub now answers counts; new `TestResetMFAReadsDeviceCounts`), `tests/test_s461_reset_mfa_counts.py`.

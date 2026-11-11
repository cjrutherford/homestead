# S508 - People commands show warnings from the response

## Goal and dependencies
`homesteadctl people` and the dashboard People page show the `warnings` list the API returns, so a failed Nextcloud admin drop is not silent. Depends on S507.

## Problem
S507 returns a `warnings` list from people disable, enable, remove, grant and revoke. The apiclient and the CLI ignored it, and the dashboard People page did not read it, so a failed Nextcloud admin drop was visible only in the audit log.

## Fix
- `internal/apiclient`: `PersonResult` decodes `warnings`. `GrantPersonApp` and `RevokePersonApp` return the `PersonResult`; `SetPersonActive` and `RemovePerson` return the warnings.
- `homesteadctl people disable|enable|remove|grant|revoke` print each as `warning: <text>` on stderr after the normal output. The exit code is unchanged.
- Dashboard People: the same five calls read `warnings` and show each as a `Warning: <text>` notice toast, the pattern the add dialog already uses for its warning.

## Not built
Nothing else changes; no new routes, so PARITY.md is unchanged.

## Verification
Go: `TestPeopleChangesPrintResponseWarningsOnStderr` (cmd/homesteadctl), `internal/apiclient/people_test.go`. Dashboard: `people.spec.ts` (warnings as notices).

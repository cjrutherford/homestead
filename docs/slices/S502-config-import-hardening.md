# S502 - Config import validates settings, needs step-up, rolls back on failure

## Goal and dependencies
Configuration import must meet the same rules as `config apply` and the other destructive routes. Depends on S483 and ADR-033.

## Problem
Found in code review:
1. `ImportExport` saved `export.Settings` after version checks only, so unknown keys or wrong types were written.
2. `POST /api/v1/config/import` was session-class and not a step-up route, so an Authentik session could overwrite settings and secrets without the local password.
3. Secrets were set one by one and then the file saved; a failure midway left new secrets beside old settings, despite the comment.

## Fix
- Settings are validated through `config.Service.ValidateSettings` (the `Plan` rules `config apply` uses, firewall and craft-files fields included) in the preview route and the import route. Preview reports the refusal in `error` with `valid: false`, so the Configuration page shows it; import answers 422 and writes nothing.
- `POST /api/v1/config/import` joins `stepUpRoutes` as a boundary step-up: password sessions pass, an Authentik session must present the local password (the dashboard's existing re-authentication prompt handles the retry) and the public name refuses it as local-only, like the other step-up routes. The preview stays session-class. The CLI uses a password session, so it needs no change.
- Import is all-or-nothing: before writing, the desired file bytes and each affected secret (and whether it existed) are snapshotted; any failed Set or Save restores them (deleting secrets that did not exist) and returns the original error, noting any incomplete restore.

## Not built
Rollback is best effort at process level (a crash between steps is not recovered). The import still replaces settings wholesale, as before.

## Verification
Go: `internal/desired/import_test.go` (rollback on a failing Set and a failing Save, refusal writes nothing), `internal/server/configio_validate_test.go` (unknown key and wrong type refused in preview and import), `internal/server/sso_test.go` and `stepup_test.go` (route is on the step-up list and local-only on the public name).

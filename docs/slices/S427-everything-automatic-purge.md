# S427: Everything automatic purge

ADR-058 section 1 (Automatic purge). Depends on S425, S426.

## Scope
When the owner removes a person in People, or revokes an Everything app from them, the daemon purges that person's sharing in the Everything app(s) by running the bridge's own offline tool inside the app's container. No bridge listener, port or credential is added.

## Design as built
- `internal/purge`: `Service.Request` finds the installed Everything apps (a catalog package with a `homestead/everything-` service image and a recorded stack; container `<package>-<service>`, bridge app name = package), stores a row in `everything_purges` (migration 57) and runs `docker exec <container> /everything-bridge people purge -db /data/bridge.db -app <app> -subject=<uid>`. A subject that matches nobody (`purged 0 people`) falls back to `-username=<name>`; the `-flag=value` form means a value can never be read as a flag. Arguments hold a name and a subject only; no secret travels in argv.
- Success deletes the row and audits `people.purge` ok; any failure (a stopped container, a missing database) keeps the row, counts the attempt, audits `people.purge` pending, and never fails the removal.
- Retry: scheduler job `people:everything-purge` every 2 minutes, enabled only while a row exists; a row for an app that is no longer installed is dropped (audited skipped). Granting the app to the same username again cancels its pending rows so a stored purge cannot remove restored access.
- People hooks: `Remove` (all Everything apps) and `Revoke` (that app, ignored when it is not an Everything app); the `people.remove` / `people.revoke` audit detail says "sharing purged in ..." or "purge pending in ...".
- Surface: `GET /api/v1/people` gains `purges`; the People page shows a warning per pending purge; `people list` prints `purge pending:` lines. No new route or command; PARITY row updated.
- Plain Compose keeps the manual `docker compose exec ... everything-bridge people purge`.

## Tests
`internal/purge/purge_test.go` (every app, one app, non-Everything app ignored, subject miss falls back, stopped then retried, uninstalled dropped, cancel, nil service), `internal/people/people_test.go` (revoke purges only Everything apps, remove with a stopped app stays pending and shows in the list, grant again cancels), `internal/server/everythingpurge_test.go` (targets from the real catalog, job enabled only while pending), `cmd/homesteadctl/people_test.go`, `dashboard/.../people.spec.ts`. Store tests updated for migration 57.

## Unproven
Not run against a live Docker daemon: `docker exec` of the distroless bridge binary is assumed from the image entrypoint (`/everything-bridge`, data `/data/bridge.db`, user 65532). Whether the app's OIDC `sub` equals the person's Authentik `uid` is unverified; the username fallback covers a mismatch.

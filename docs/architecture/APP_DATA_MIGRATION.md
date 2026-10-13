# App Data Migration and Backup Source Guard (S277)

Owner runbook and design notes. Applies to apps installed before S276,
whose data sits in anonymous or named Docker volumes (or the container
layer) instead of `<mount>/<package>/<subpath>` on mounted storage.
Architecture context: ADR-005 (Compose runtime), ADR-006 (plan, preview,
approve), ADR-013 (mounted storage only), ADR-004 (CLI/dashboard
parity), ADR-020 (session boundary).

## Detection

Every installed catalog app is compared with its S276 data declarations
by reading the containers' mounts from the engine (stopped containers
count; their mounts persist):

- `placed`: each declared path is a bind mount at the resolved host path
  (or tmpfs for ephemeral paths).
- `needs-migration`: a declared path is held by a Docker volume, lives
  inside a volume mounted above it (Nextcloud `data/` inside the `html`
  volume), or has no mount at all (container layer).
- `unknown`: nothing to inspect (no containers), a bind mount somewhere
  else, a volume at a path the catalog does not declare, or a class
  without an assigned mount. These are never migrated automatically.

Where to look: `homesteadctl data status`, the dashboard Data page
(`/data`), and `homesteadctl doctor` (`app-data:<package>` checks, warn
unless placed). API: `GET /api/v1/data-migrations`.

## Migrating one app

1. Review: `homesteadctl data plan <package>` (dashboard: Review
   migration plan). The plan lists each copy (volume directory to target
   path, size), free space on every target mount, the old volumes that
   will be left behind, expected downtime (an estimate), and whether the
   pre-migration backup can run. It is refused when free space (plus 5%)
   is insufficient, a target already holds files, or two volumes would
   share one target.
2. Approve: `homesteadctl data approve <package> --digest <digest>`
   (dashboard: Approve migration). The digest binds the approval to the
   plan you read; if the stack changed since, approval is refused (409)
   and again at execution. If the backup cannot run (no backup class
   assigned, not enough room), approval needs the explicit
   `--acknowledge-no-backup` (dashboard checkbox); the audit entry says
   so. Approval is audited (`app.data.migrate.approve`, with the plan)
   and the outcome is audited (`app.data.migrate`).
3. Steps (operation `catalog.migrate`): `preflight`, `provision:*`,
   `stop`, `verify-stopped`, `backup` (or the acknowledged no-op), one
   `copy:*` per path, `verify-copy`, `write-stack`, `verify-database`
   (database apps), `up`, `health-check`, `record-old-volumes`. The
   whole stack is down from `stop` until `up`.
   Only one migration runs per package at a time: a second approval is
   refused (409), and configure, remove, drift restore, and update
   requests for that package are refused while it runs. The claim is
   held in the daemon's memory for the whole operation.
4. The backup is a cold file copy of the stopped volumes taken after
   `stop`, so databases are consistent and no database password is
   needed.

Bookkeeping: once `health-check` passes the migration is committed.
Recording the old volumes is retried and, if it still fails, the
operation succeeds with a `warning` audit entry (`app.data.migrate`)
naming the volumes; they are then not eligible for removal until
recorded. A bookkeeping failure never rolls the migration back.

Database apps (Postgres): the database directory is copied only after
`verify-stopped` proves no container runs. `verify-database` then starts
only the database services on the new bind mounts and waits for
`pg_isready` (images whose name contains `postgres`) before the rest of the stack starts.

### Copy method

GNU `tar` on the host with `--numeric-owner --xattrs --acls --sparse`,
creating from the volume data directory and extracting onto the target
(`tar -c | tar -x`). The daemon runs as root, the engine reports each
volume's host directory, and no helper image needs to be present or
pulled. The usual helper-container tools (busybox `cp -a` or `tar`) drop
extended attributes, which is why they are not used. Nested declarations
are split: the parent copy excludes the nested directory and the nested
path is copied from its sub-directory of the same volume. `verify-copy`
compares every entry: type, mode, owner, symlink target, extended
attributes where readable, and a streamed SHA-256 of each regular file.
Both trees are read once more during the downtime window (the plan's
estimate includes the cold backup, not this read). A missing source is a
failure; only a nested path absent from its volume counts as empty.
Symlinks are copied as links and never followed; hard links stay linked;
FIFOs are preserved; sockets are skipped on both sides.
Before copying and before verifying, the stack is checked to still be
stopped.

## Rollback

Any failing step stops the operation, rolls back, and names the step in
the operation error (`migration stopped at step "<name>": ...`). The old
volumes are never modified or removed during migration.

- Before containers were recreated (any step up to `write-stack`): the
  original `compose.yaml` bytes are restored if they had been replaced
  and the original stopped containers are started again.
- After containers were recreated (`verify-database`, `up`,
  `health-check`): the new stack is stopped first, then `compose.yaml`
  is rewritten as the original plus explicit mounts of the original
  volumes (declared external), and the stack is brought up on them. The explicit pins matter: a bare original
  file would let Compose create fresh empty anonymous volumes. The
  pinned file is the one difference from the original, and the error
  says so. To return to pure original text, edit it by hand.
- If rollback itself fails the error says `ROLLBACK FAILED`. The old
  volumes are intact; start the stack by hand with
  `docker compose -p <package> start` (containers still exist) or restore
  `compose.yaml` and pin the volumes as above.

Rollback never deletes the data at the new location. Every copied
directory that holds files is renamed (same mount, so instant) to
`<target>.rollback-<UTC stamp>`, and the operation error and audit entry
list each moved-aside path. That tree includes anything the app wrote
between `up` and the failure, so you can inspect it or merge those
writes by hand; the old volumes remain the source of truth for the
restarted app. A retry migrates onto a fresh empty target. Moved-aside
directories are never cleaned up automatically (they use space on the
target mount); remove them yourself once you no longer need them.
Container-layer contents of paths with no mount are not carried over.

If the new stack cannot be stopped, rollback stops there rather than
move data from under a running app, and says so.

## After a successful migration

The old volumes are recorded as `pending-removal`. Spot-check the app,
and confirm the next scheduled backup captures real data. Removal is a
separate, explicit action: `homesteadctl data remove-volumes <package>
<volume>...` (dashboard: Remove old volumes), audited as
`app.data.old-volumes.remove`. It refuses (409) volumes not recorded by a
migration, already removed, or still mounted by any container, and checks
again at execution. Nothing removes volumes automatically.

## Backup source guard

`backupJob` (used by scheduled backups and pre-update backups) now fails
with a clear error when:

- a declared persistent path is missing under the chosen sources, or is
  not covered by them (a stray directory no longer passes);
- a container keeps a declared path in a Docker volume, the container
  layer, tmpfs, or a bind mount elsewhere (the error points at this
  migration);
- the engine cannot be inspected.

It records warnings in the run's `backup.json` (`warnings`) instead of
failing when every declared path exists but is empty (an app that has
not written yet), or when a container has an image volume the catalog
does not declare (that data is not backed up). Read-only binds and
owner-supplied extra binds are the owner's libraries and are not judged. The pre-migration backup is its own job (volume
directories as sources) and is not subject to the guard.

## Store

Only Postgres readiness is verified. No other database engine is
declared by a catalog entry today (Nextcloud's SQLite is a file inside
the copied tree, covered by `verify-copy`); a new engine needs its own
readiness check added to `databaseReady` before its entry can rely on
`verify-database`.

Schema version 29 adds `app_data_old_volumes` (volume, package, service,
target, size, state `pending-removal` or `removed`, actor, timestamps).

## Order for apps that predate both S266 and S276
Apps installed before secret-backed variables (S266) and before declared
data placement (S276) need three steps, in this order:

1. **Register** the existing database password
   (`homesteadctl apps secrets register <pkg> db_password --value-file -`).
   The migration's pre-copy backup exports the database and needs it, and
   registration only verifies, never changes, the password.
2. **Adopt** the secret wiring (`homesteadctl apps configure <pkg>
   --adopt-secrets`). It restarts the containers with new compose wiring;
   Compose keeps anonymous volumes across that recreate, so data stays.
3. **Migrate** the data (`homesteadctl data plan <pkg>`, then approve).
   The migration rewrites only the `volumes:` of the compose file, so the
   adopted secret wiring survives it.

The other order (migrate, register, adopt) also works, but the
migration backup then runs without a stored password and Postgres file
copies are not crash-consistent; register first. Never uninstall and
reinstall such an app to "fix" credentials: the uninstall is refused while
data still lives in Docker volumes.

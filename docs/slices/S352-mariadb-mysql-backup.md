# S352 — MariaDB/MySQL Backup Export

## Goal
Let the catalog back up MariaDB and MySQL applications consistently, so
BookStack and the PHP apps that follow never copy a live database
directory. This is foundation F2: it adds engines to the backup contract,
not a package.

## Dependencies
S180 (database exports and the run manifest) and S266 (database password
secrets) are complete. S351 closed Wave 1; this slice opens the second
foundation on the critical path to S366 BookStack.

## Scope

### Catalog contract (`internal/catalog/backup.go`)
- `DatabaseMariaDB` (`mariadb`) and `DatabaseMySQL` (`mysql`) join
  `DatabasePostgres` as server engines. `DatabaseKind.Server()` names the
  family, and `Valid()` accepts sqlite plus every server engine.
- Server engines share one shape: database and username must be valid
  identifiers, `path` is refused, and `PasswordSecret` may name the
  manifest secret holding the password. Error text names the offending
  engine rather than saying "postgres" for all of them; the kind error
  lists all four values.
- `PasswordSecret` validation now accepts any server engine (`sqlite`
  still refuses it): the secret exists so scheduled backups can resolve a
  password, not to imply Homestead can rotate it.

### Export (`internal/backup/database.go`)
- One dispatch case covers all server engines and writes
  `<database>.sql` (or `.sql.age` when encrypted, mode 0600). SQLite is
  unchanged.
- `exportMySQL` runs the engine's own client inside the database
  container: `mariadb-dump` for MariaDB, `mysqldump` for MySQL. Both get
  `--single-transaction` (one InnoDB-consistent snapshot without blocking
  writers), `--quick` (stream rows), `--skip-lock-tables` (no read locks
  on non-transactional tables) and `--no-tablespaces` (MySQL 8 otherwise
  asks for the `PROCESS` privilege). MySQL additionally gets
  `--set-gtid-purged=OFF`; MariaDB's client has no such flag and would
  reject it.
- The client connects over `127.0.0.1`, so the server checks the password
  instead of trusting a socket login. The password arrives on stdin and
  is exported as `MYSQL_PWD` inside the container shell for the single
  client call, never argv, the host environment, disk, or errors.
  `exportPostgres` keeps its `PGPASSWORD` path; both now share
  `writeDump` for plaintext/encrypted output.
- `Job.Validate` requires a password for every server engine, not just
  Postgres, and names the engine in the error.

### Password secret resolution (`internal/server/updates.go`)
The backup job builder resolves `PasswordSecret` for any server engine.
The three Postgres-specific messages ("postgres backup needs …") become
engine-neutral; a missing secret still names the store entry and the
registration command.

### Password setting and verification (`internal/catalog/rotate.go`)
Setting or verifying a database password is Postgres-only. MariaDB and
MySQL are declared server engines, so the functions no longer claim the
manifest "declares no postgres database"; they refuse with "not supported
yet" and point at the engine's own client plus `--skip-verify`.
`rotateSecret` refuses a MariaDB/MySQL database password before it
journals a pending value, so no rotation can leave the secret and the
database disagreeing. Adoption skips password verification for those
engines (it has no `--skip-verify` flag) and proves the stack by starting
it instead. Rotation support is recorded in `docs/DEFERRED.md`.

### Restore path
Database restores stay file-level: the run holds `<database>.sql.age`, and
the owner loads it with the runbook. `docs/architecture/BACKUP_RESTORE_RUNBOOK.md`
section 6 gains a MariaDB/MySQL subsection: start only the database
service, decrypt the artifact, and load it with `"$DBCLIENT"` (`mariadb`
or `mysql`) as `$DBUSER` into `$DBNAME`, with `$DBPASS` from the restored
secret file in the container environment for that one command. Both dump
clients write `DROP TABLE IF EXISTS` before each table, so a load
replaces the tables the run holds; prose covers the drop/recreate case
when the database must hold exactly the run's contents.

### Documentation and tests
- `TestExportMariaDBAndMySQL` drives the export for both engines: client,
  consistency flags, loopback host, database argument, password on stdin
  only, `<database>.sql` naming, and 0600 mode.
- `TestDatabaseBackupValidation` accepts both kinds and still refuses a
  path, a bad identifier, a missing username, and unknown kinds.
- `TestJobValidateServerDatabasePassword` covers Postgres, MariaDB and
  MySQL with and without a password.
- `TestRotateRefusesMariaDBPassword` and
  `TestDatabasePasswordEngineGuards` pin the refusal, and that no pending
  journal row is written.
- `internal/backup/runbook_test.go` runs the two new named blocks against
  a real age-encrypted run and the docker stand-in, including the
  truncated-artifact case.
- `tests/test_s180_backup.py` now pins all four kinds;
  `tests/test_s352_mariadb_backup.py` pins the engine clients, flags,
  password path, refusal, runbook blocks and docs.

## Non-goals
- A new catalog package. S352 changes the contract only; BookStack is
  S366.
- MariaDB/MySQL password setting, verification, or rotation (recorded in
  `docs/DEFERRED.md`).
- Dumping `mysql`/`performance_schema` system schemas, routines, events,
  or non-InnoDB consistency guarantees. A package that needs those adds
  documented consistency hooks.
- An automatic in-place database restore operation. The runbook keeps the
  proven file-level restore and an explicit engine load.
- Restoring a dump over a database that must lose tables the run does not
  contain; the runbook says how.
- Routes, CLI commands, or dashboard surfaces, so `docs/PARITY.md` does
  not change.

## Acceptance Criteria
- A manifest declares `kind: mariadb` or `kind: mysql` with database,
  username and an optional password secret; validation refuses a path or
  a cross-engine field.
- Export writes `<database>.sql` through the matching client with
  `--single-transaction --quick --skip-lock-tables --no-tablespaces`,
  connects over 127.0.0.1, puts the password only on stdin, and writes
  mode 0600 (or age-encrypts when recipients are configured).
- A scheduled backup resolves `PasswordSecret` for both engines and
  refuses to build without a password.
- Rotation/verification of a MariaDB/MySQL password refuses clearly and
  changes nothing.
- The runbook's new blocks run literally in the runbook test, including
  failure on a truncated artifact.
- Main build remains green and all existing tests pass.

## Handoff
F2 is landed; the remaining foundation is F4, the community-image policy
(S360). Wave 2 starts with S353 `audiobookshelf`
(`docs/SLICE_PLAN.md`), and BookStack (S366) can now declare its MariaDB
export.

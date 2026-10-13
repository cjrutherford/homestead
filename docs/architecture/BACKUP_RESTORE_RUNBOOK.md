# Backup Restore Runbook (S275, ADR-022)

How to restore a Homestead backup with only the `age` CLI and standard
tools. Nothing here needs `homestead-server` or `homesteadctl`: backups
are customer-owned (ADR-022) and the application stacks are plain Docker
Compose (ADR-005).

Every shell block in this document is marked `sh runbook <name>` and is
executed literally by `internal/backup/runbook_test.go` against a run
written by the real backup code, using the real `age` and `tar` binaries
(only `docker` is a stand-in that records its arguments). The test fails
if a shell block is unnamed or never exercised. If you change a command
here, that test shows whether it still works.

The blocks are written for `sh -eu`: save a block as a file and run it
with `sh -eu FILE`, or paste it into a shell started that way, so that
the first failing command stops it. This matters because the blocks never
pipe `age` into another command: a pipeline reports only its last
command's status (and `/bin/sh` on Debian and Ubuntu has no `pipefail`),
so a failed or truncated decryption would be hidden. Each block decrypts
into a private temporary directory (mode 0700), checks that `age`
succeeded, and only then extracts or loads. The plaintext copy is
removed at the end of a successful block (`rm -rf`: on a spinning disk or
when the plaintext is a secret dump, run `shred -u` on the file inside it
first). If a block fails, the
temporary directory is left behind: shred and remove it yourself (it is
the newest `tmp.*` directory under `${TMPDIR:-/tmp}`).

## What you need

- The backup run directory (on the backup-class mount, one directory per
  run: `<backup root>/<package>/<UTC timestamp>/`).
- An identity file that one of the run's recipients holds: your own
  owner key, or the recovery identity exported with
  `homesteadctl backup identity export --out FILE` (store that file off
  the appliance; a backup encrypted only to an appliance whose disk is
  gone cannot be opened).
- `age`, `tar`, `cp`, `find`, and `docker` with the
  Compose plugin on the machine you restore to.
- Root. `tar` only restores file owners as root, and database
  directories need their original uid (Postgres refuses to start on a
  directory owned by anyone else). Use `sudo -i` or run each command
  with `sudo`; the identity file must be readable by that user.

Treat the identity file as a secret: keep it mode 0600 and shred it from
the restore host afterwards.

### Trust and path traversal

`age` encrypts but does not sign. Anyone who knows a recipient's public
key can produce an archive that your identity decrypts, so what you
trust is the run's provenance (the backup root, and the recipient keys
being private to you), not the decryption succeeding. GNU `tar` strips a
leading `/` from member names and refuses `..` components, and `-C`
confines extraction to the target directory, so an archive does not
extract outside it by name. Still, list an archive first (section 2) and
look for absolute names, `..` and symlinks that point out of the tree
before extracting something you did not write yourself.

## 1. Read the run

`backup.json` is plaintext and holds no secret values:

```sh runbook read-manifest
cat "$RUN/backup.json"
```

Fields that matter:

| Field | Meaning |
| --- | --- |
| `format_version` | `2` is age-encrypted. Absent (or `1`) is a plaintext run: see "Plaintext (format 1) runs". |
| `entries` | Every restorable item: file trees and database exports. |
| `archives` | Entry to the encrypted file holding it, relative to the run. File trees are `<entry>.tar.age`; database exports are `<name>.age`. |
| `sources` | Entry to the mountpoint it was copied from. |
| `paths` | Classed entry to its path on that mount. |
| `warnings` | What the run did not cover or could not copy consistently. Read these first. |

Where an entry restores to: the target is `sources[entry]` joined with
`paths[entry]`. For example, with `application/authentik` from mount
`/srv/app` and path `authentik`, the target is `/srv/app/authentik`.
Older runs have no `paths`; the entry itself is the path.

Run layout (format 2):

```
<run>/backup.json
<run>/<class>/<package>.tar.age                data tree on that storage class
<run>/stack/<package>.tar.age                  stack definition: compose.yaml and
                                               compose.override.yaml, without secrets/
<run>/stack-secrets/<package>/secrets.tar.age  stack secret files
<run>/<database>.sql.age                       Postgres dump (plain SQL)
<run>/<file>.db.age                            SQLite snapshot
```

`<class>` is `application` or `bulk`. Database directories of a package
that has a database export are deliberately empty in the data tree
(`warnings` says so): the export is authoritative.

Set these variables for the commands below (the values here are
examples; the blocks name the other variables they need where they use
them):

```text
RUN=/srv/backup/authentik/20261008T120000Z
IDENTITY=/root/recovery-identity.txt
PACKAGE=authentik
STACK=/opt/homestead/stacks/authentik
```

## 2. Check the identity opens the run

List an archive without extracting anything. A wrong identity fails here,
before any live data is touched.

```sh runbook list-archive
WORK=$(mktemp -d)
age -d -i "$IDENTITY" -o "$WORK/archive.tar" "$ARCHIVE"
tar -tvf "$WORK/archive.tar"
rm -rf "$WORK"
```

`ARCHIVE` is a path from the manifest's `archives` map, for example
`"$RUN/application/authentik.tar.age"`.

## 3. Stop the application

Do not restore into a running application. Stop the stack and keep it
stopped until every tree is back:

```sh runbook stop-stack
docker compose --project-directory "$STACK" --project-name "$PACKAGE" stop
```

## 4. Restore a data tree

Move any existing target aside (never delete it until the restore is
verified), then decrypt to a temporary file and extract from it.
`--numeric-owner` restores the recorded uid and gid as numbers instead of
looking names up on the restore host; `-p` keeps modes.

```sh runbook move-aside
if [ -e "$TARGET" ]; then
	mv "$TARGET" "$TARGET.before-restore"
fi
```

```sh runbook extract-tree
WORK=$(mktemp -d)
age -d -i "$IDENTITY" -o "$WORK/tree.tar" "$ARCHIVE"
mkdir -p "$TARGET"
tar --numeric-owner -xpf "$WORK/tree.tar" -C "$TARGET"
rm -rf "$WORK"
```

`age` authenticates the whole encrypted stream, and a truncated or
tampered archive makes the decryption step fail before `$TARGET` is
created or `tar` runs. If any step fails, remove `$TARGET` if it was
created, move `$TARGET.before-restore` back, and try another run.

Repeat for every `archives` entry whose entry name is a data tree:
`application/<package>`, `bulk/<package>`. The mount for each target
must be mounted first (mounted storage only, ADR-013; Homestead does not
format or partition).

## 5. Restore the stack definition and secret files

The `stack/<package>` entry holds the stack's `compose.yaml` and
`compose.override.yaml` (the stack directory without `secrets/`). Restore
it into the stack directory, `STACK` (typically
`/opt/homestead/stacks/<package>`), moving any existing directory aside
first with the `move-aside` block above (`TARGET="$STACK"`). It restores
the stack directory's own files and an empty `secrets/` directory.
Variables: `STACK_ARCHIVE` is the `stack/<package>.tar.age` file.

```sh runbook restore-stack
WORK=$(mktemp -d)
age -d -i "$IDENTITY" -o "$WORK/stack.tar" "$STACK_ARCHIVE"
mkdir -p "$STACK"
tar --numeric-owner -xpf "$WORK/stack.tar" -C "$STACK"
rm -rf "$WORK"
```

Do this before the secrets: restoring the stack definition replaces the
stack directory.

The `stack-secrets/<package>/secrets` entry holds the files Compose
mounts as secrets (database passwords, keys). Restore it the same way
with `extract-tree`, into `STACK/secrets` (the empty directory the stack
restore left, so it needs no move-aside), then enforce the permissions
Homestead itself applies: a 0700 directory and 0600 files, with the
recorded owners (kept by `--numeric-owner`).

```sh runbook secrets-modes
chmod 700 "$TARGET"
find "$TARGET" -type f -exec chmod 600 {} +
```

Do not restore these into a directory that other users can read.

## 6. Restore databases

Database exports are in the run next to the trees: `<database>.sql.age`
(Postgres, plain SQL from `pg_dump`) or `<file>.db.age` (SQLite
snapshot). Because the live database directory is not in the file copy,
the restored directory is empty and the database engine starts a fresh
cluster.

### Postgres

1. Start only the database service, so it initializes an empty cluster
   from the restored secret file. `DBSERVICE` is the Compose service name
   (for example `postgresql`):

```sh runbook postgres-up
docker compose --project-directory "$STACK" --project-name "$PACKAGE" up -d "$DBSERVICE"
```

2. Wait until it accepts connections, then load the dump. The dump is
   plain SQL, so it is loaded with `psql` (not `pg_restore`, which reads
   custom-format dumps). It has no `DROP` statements: load it into an
   empty database. If the database already holds tables, drop and
   recreate it first:

```sh runbook postgres-recreate
docker exec -i "$DBCONTAINER" psql -U "$DBUSER" -d postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE \"$DBNAME\"" -c "CREATE DATABASE \"$DBNAME\" OWNER \"$DBUSER\""
```

   Variables: `ARTIFACT` is the `.sql.age` file, `DBCONTAINER` is
   `<package>-<service>` (for example `authentik-postgresql`), `DBUSER`
   and `DBNAME` come from the package's database declaration. The dump is
   decrypted to a file first, so a damaged export never reaches `psql`
   half-read:

```sh runbook postgres-restore
WORK=$(mktemp -d)
age -d -i "$IDENTITY" -o "$WORK/dump.sql" "$ARTIFACT"
docker exec -i "$DBCONTAINER" psql -U "$DBUSER" -d "$DBNAME" -v ON_ERROR_STOP=1 < "$WORK/dump.sql"
rm -rf "$WORK"
```

### SQLite

A SQLite export is the whole database file. With the application
stopped, put it where the application expects it (the `database.path`
under the data tree) and give it back the application's owner:

```sh runbook sqlite-restore
age -d -i "$IDENTITY" -o "$DBFILE.part" "$ARTIFACT"
chmod 600 "$DBFILE.part"
mv "$DBFILE.part" "$DBFILE"
```

The file is written beside its destination under a `.part` name and
moved into place only after `age` succeeded, so a truncated export
leaves no database file (delete a leftover `$DBFILE.part`).

Then `chown` it to the uid and gid the application runs as (the owner of
the neighbouring files), for example `chown 1000:1000 "$DBFILE"`.

## Plaintext (format 1) runs

Runs written before encryption (no `format_version`, or `1`) are plain
trees at `<run>/<entry>/` and plain database files. No identity is
needed. Copy with `cp -a`, which keeps modes, owners and links when run
as root:

```sh runbook plaintext-tree
mkdir -p "$TARGET"
cp -a "$RUN/$ENTRY/." "$TARGET/"
```

`ENTRY` is the manifest entry name (for old runs, the path itself).
Database files in a plaintext run (`<name>.sql`, `<name>.db`) load the
same way as above, without `age -d`.

## 7. Bring the stack back up by hand

Compose stacks are ordinary: Homestead only wrote `compose.yaml` and a
`secrets/` directory into `/opt/homestead/stacks/<package>/`. With the
data trees and secrets restored, start it with Docker alone:

```sh runbook start-stack
docker compose --project-directory "$STACK" --project-name "$PACKAGE" up -d
```

```sh runbook stack-status
docker compose --project-directory "$STACK" --project-name "$PACKAGE" ps
```

The run holds the stack definition (section 5), so a rebuilt machine
needs nothing from the old one. Runs written before the `stack` entry
existed do not: for those, get `compose.yaml` from a copy of
`/opt/homestead/stacks`, or write a Compose file for the package
yourself: it only needs the images and the bind mounts from the restored
`application` and `bulk` directories.

Check the application, then delete the `*.before-restore` directories.

## Troubleshooting

- `no identity matched any of the recipients`: that identity is not one
  of the run's recipients. Try the other key you hold (owner key versus
  recovery identity). Runs written before a recipient was added cannot
  be opened with it.
- `tar: ... Cannot change ownership`: you are not root.
- Postgres refuses to start on the restored directory: the directory must
  be owned by the image's postgres uid with mode 0700 (Authentik's image
  uses uid 70, Immich's uid 999). Restoring as root with
  `--numeric-owner` keeps this; if you restored by other means,
  `chown -R 70:70` and `chmod 700` the directory.
- The run's `format_version` is higher than 2: the layout is newer than
  this runbook; the `age` step still works, and `archives` still names
  each file.

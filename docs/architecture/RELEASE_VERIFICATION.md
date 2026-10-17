# Release Artifact Verification (S264, ADR-025)

Operations and design notes for verifying platform artifacts. Nothing is
fetched: the owner places the artifact, checksums file, and signature on
the appliance.

## Files

- Checksums file (signed), one `key=value` header pair then `sha256sum`
  lines:

  ```
  version=0.2.0
  counter=7
  <sha256-hex>  homestead-server
  <sha256-hex>  homesteadctl
  ```

  `version` and `counter` (positive integer, incremented every release)
  are mandatory. The artifact is matched by its file name.
- Signature: minisign `.minisig`, non-prehashed `Ed` only. Prehashed `ED`
  signatures are rejected. Both the file signature and minisign's global
  signature over signature plus trusted comment must verify. Create with
  `minisign -S -m SHA256SUMS`.
- Public key: minisign `.pub` file or its bare base64 line.

## Rules

- The daemon reads the files, and its unit sets `ProtectHome=true` and
  `PrivateTmp=true`: files under `/home`, `/tmp`, or `/var/tmp` look
  unreadable to it and give the generic "readable regular file" 400. Place
  them elsewhere, for example `/srv` or `/opt`.

- Authenticity failures (unknown key, bad signature, checksum mismatch,
  malformed file) block the release with no override.
- A counter below the installed one is refused unless the owner
  overrides (`--allow-older`, or the confirmation checkbox shown after a
  rollback refusal). The override is audited with both versions and
  counters. An equal counter with a different version is always refused.
- The installed baseline is seeded by verification: when a release whose
  version equals the running version (`version.Version`) verifies and no
  installed record exists, it is recorded as installed (version and
  counter), never overwriting. From then on an older counter is refused.
  Before that first match, no rollback check applies. A successful
  apply (S265) moves the baseline with `RecordInstalled`.
- Failed, rollback-refused, and unverified releases are never offered to
  the planner: the plan route lists them under `refused` with the
  reason, and only `verified` releases can appear in `plan`.
- Input files must each be a regular file: symlinks (final component),
  FIFOs, devices, and directories are refused, checked on the opened
  descriptor. Limits: artifact 2 GiB, checksums and signature 1 MiB,
  checksums lines 16 KiB. The route answers "must each be a readable
  regular file" without saying which path failed or whether it exists.
  Input refusals are 400s, not recorded as verdicts.
- Checksums file names must be bare names (no `/`, `\`, `..`); a second
  `version=` or `counter=` line is rejected even when empty.
- Time of check to time of use: verification records the artifact's
  SHA-256 in the release record. Apply (S265) calls
  `release.VerifyDigestUnchanged` on the owner's file when the apply
  starts, and again on the staged copy immediately before replacing the
  binary, refusing on any error, because the file the owner supplied can
  be swapped after verification.

## Trusted keys

Stored in SQLite (`platform_trust_keys`; removal of the embedded key is
tombstoned in `platform_trust_removed`) and rendered to
`platform-trust.yaml` beside `desired.yaml`. The embedded HAI key is a
placeholder until the production key ceremony (docs/DEFERRED.md); the
API, CLI, dashboard, and YAML mark it `placeholder`, and doctor warns
(`release-trust`) while it is the only trusted key (or none is trusted).
Key changes render the YAML before the audit entry: a render failure is
audited as an error. To
rotate or after a leaked HAI key:
`homesteadctl updates trust add|replace|remove`, or the dashboard.

## Surfaces

`GET /api/v1/platform/updates`, `POST /api/v1/platform/updates/verify`,
`POST /api/v1/platform/updates/apply` and `/clear-apply` (S265),
`GET|POST /api/v1/platform/trust-keys`,
`PUT|DELETE /api/v1/platform/trust-keys/{id}`; `homesteadctl updates
platform [clear-apply]|verify|apply-platform|trust`; the dashboard Updates page. See docs/PARITY.md.

## Proven live in KVM (S264, da3362f)

On both appliances, with a throwaway Ed25519 minisign-format key:
without the key, verification failed as untrusted and doctor warned
(`release-trust`); after `trust add` the doctor check was ok and the
release verified (the matching running version recorded the installed
baseline). A tampered artifact, a tampered checksums file, and a
signature from an unknown key each failed; counter 5 after installed 10
was refused with the rollback hint and verified with `--allow-older`
(audited with both versions and counters); FIFO, symlink, directory, and
missing artifact paths were refused as 400 inputs, and the API required
CSRF. `platform-trust.yaml` listed the key and dropped it on removal;
the placeholder embedded key stayed flagged. The test key was removed
afterwards. As documented, a later verify of an older counter does not
move the baseline (a successful apply does).

## Applying a verified release (S265, ADR-027)

`homesteadctl updates apply-platform --component homestead-server
--artifact /srv/rel/homestead-server --checksums /srv/rel/SHA256SUMS
--signature /srv/rel/SHA256SUMS.minisig [--allow-older] [--follow]`, or the
Apply action on the dashboard Updates page. Only a release that is
`verified` can be applied (failed, rollback-refused, and unverified records
never are), and the three files must be the ones it was verified from.

Who does what (ADR-027): `homestead-server` never writes executables; its
unit has no write grant on `/usr/local/bin`. The daemon verifies the files
again, stages copies into `/var/lib/homestead/staging` (root-only), writes
the SQLite apply marker, and starts the privileged oneshot
`homestead-apply.service` (`systemctl --no-block start`). That unit runs
`homestead-server apply-platform`: the installed binary in apply mode, so
the component model and checksums are unchanged. It independently
re-verifies the staged files (signature, checksums, digest, counter against
the installed baseline; the database `verified` row is not enough), keeps
the previous binary, swaps it atomically, restarts the daemon, waits for it
to be healthy, and finalizes the same operation record from outside the
daemon. `make install` installs the unit; an install from before ADR-027
must re-run it, or the apply fails at `handoff` before anything changes.

Components: the daemon (`homestead-server`) and the CLI (`homesteadctl`)
apply separately, each from its own verified release. The dashboard is
embedded in the daemon. The daemon restarts; the CLI does not. Apply the
CLI after the daemon so the two match. The CLI's "from" version is its own
record, or `unrecorded (sha256 ...)` before one exists, never the daemon's.

Policy: only an older counter than the installed release demands
`--allow-older` (dashboard: a separate confirmation); the apply and its
outcome are audited as overrides with both versions and counters. With no
installed counter nothing is "older". The same counter with a different
version is refused; applying the installed version or an identical binary
is refused (409). One apply at a time (409).

Operation steps: daemon side `stage`, `handoff`, `apply` (waits; it has no
timeout of its own, so it can never mark the operation failed while the
apply unit still owns it); apply unit `verify-staged`, `preserve-previous`
(`/usr/local/bin/<name>.previous` plus version, counter and SHA-256 in
`platform_previous`), `replace` (digest check, copy to `<name>.new` beside
the target, check, rename; a failed directory fsync after the rename is
audited and the swap kept), `restart` (daemon only; a restart that cannot
run undoes the replacement), `health-check`. Health means, within the
health timeout (90 s): the daemon's main process runs the verified digest,
the database opens, `/healthz` reports the target version (a release binary
must be built with its version, `-ldflags "-X
homestead/internal/version.Version=<version>"`), and the recovery socket
accepts connections. The CLI is checked on disk by digest.

Outcomes. Success: `RecordInstalled` moves the rollback baseline (retried;
if it cannot be recorded the apply fails rather than claiming success), the
operation succeeds, before and after are audited, the marker is cleared.
Failure at any point: the operation fails, the marker is always cleared (it
never blocks later applies), the failure is audited, `doctor` reports
`platform-apply` as failed until a later apply succeeds, and the previous
binary is kept. Startup recovery excludes the marker's operation
(`Runner.RecoverExcept`). A new binary that crashes at startup is retried by
systemd at most five times in five minutes (`StartLimitBurst`), and the
apply unit's health timeout then fails the operation.

If the apply unit itself dies, the marker stands: `doctor` warns once it is
older than 15 minutes, and `homesteadctl updates platform clear-apply` (or
the dashboard Clear stale apply button) marks the operation failed and
clears it, audited; it refuses while the unit is running.

### Automatic rollback

If the restarted daemon is not healthy within the health timeout (the
process runs a different binary, the listeners do not answer with the
target version, or the database does not open), the apply unit does not
leave the appliance on the bad binary. In the same run it keeps a copy of
the failed binary as `homestead-server.rolledback-<stamp>`, restores
`homestead-server.previous` (atomic rename; the copy's digest must equal
the digest recorded when it was preserved), runs `reset-failed` and
restarts the daemon, and waits for it to be healthy. The operation ends
`failed (rolled back)`; the audit trail records both versions and the
installed release record stays exactly as it was before the apply (the
failed release is never recorded, and the baseline never drops). `doctor`
reports `platform-apply` as failed until a later platform operation
(apply or rollback) succeeds. The homesteadctl component has no restart:
its health check is the on-disk digest, and rollback restores the file.

### Manual rollback

```
homesteadctl updates rollback-platform [--component homestead-server|homesteadctl] [--allow-older] [--follow]
```

The Updates page offers the same action behind a confirmation. It runs
through the same apply unit, which re-verifies the recorded previous
binary's digest (and that it is the sibling `<binary>.previous`), restores
it, restarts the daemon (not for the CLI), health-checks it, and records
the previous release as the installed baseline. It is refused when no
previous binary is recorded, when the component already runs the previous
binary, and while another apply or rollback is pending. Rolling back to an
older counter than the installed release is an explicit owner override
(ADR-025): without `--allow-older` (or the second dashboard confirmation) it
is refused; with it, the audit trail records the override with both
versions. A rollback is refused even with the override when the previous
release has no recorded counter (it would erase the anti-rollback
baseline) or reuses the installed counter under a different version; in
that case reinstall the release through a verified apply with its signed
checksums file. There is no roll-forward: the replaced binary is only kept
as a timestamped copy, not made the new previous. The last two
`homestead-server.rolledback-<stamp>` copies are retained (older ones are
removed); the copy is skipped, and the message says so, if there is not
enough free space.

### If rollback fails

The operation ends `rollback failed` with the cause, `doctor` fails until a
later platform operation succeeds, the
audit trail and the `homestead-apply` journal
(`journalctl -u homestead-apply`) carry the error, and both binaries stay
on disk where they existed: `homestead-server.previous` (the last good
binary) and the newest `homestead-server.rolledback-<stamp>` (the binary
that was replaced). The error states whether the daemon is still on the
failed binary (for example when `.previous` was missing or corrupt, so
nothing could be restored: put a known-good binary in place with the same
cp/mv steps) and prints the manual restore command; the daemon may be
down:

```
sha256sum /usr/local/bin/homestead-server.previous   # compare with GET /api/v1/platform/updates "previous"
sudo cp -p /usr/local/bin/homestead-server.previous /usr/local/bin/homestead-server.restore
sudo mv /usr/local/bin/homestead-server.restore /usr/local/bin/homestead-server
sudo systemctl reset-failed homestead-server
sudo systemctl restart homestead-server
```

The `mv` is atomic and safe while the binary runs. If a marker still shows
in `homesteadctl updates platform` afterwards, run `clear-apply` once the
daemon is back.

### Building a test release

```
go build -ldflags "-X homestead/internal/version.Version=0.9.0" -o /srv/rel/homestead-server ./cmd/homestead-server
printf 'version=0.9.0\ncounter=90\n%s  homestead-server\n' "$(sha256sum /srv/rel/homestead-server | cut -d' ' -f1)" > /srv/rel/SHA256SUMS
minisign -G -p test.pub -s test.key   # once; then: homesteadctl updates trust add --key-file test.pub
minisign -S -s test.key -m /srv/rel/SHA256SUMS   # writes SHA256SUMS.minisig
homesteadctl updates verify --component homestead-server --artifact /srv/rel/homestead-server --checksums /srv/rel/SHA256SUMS --signature /srv/rel/SHA256SUMS.minisig
homesteadctl updates apply-platform --component homestead-server --artifact /srv/rel/homestead-server --checksums /srv/rel/SHA256SUMS --signature /srv/rel/SHA256SUMS.minisig --follow
```

Keep the files outside `/home`, `/tmp`, and `/var/tmp`.

## Craft release manifests (S279, ADR-028)

Craft container images are never pulled or published. The appliance
verifies a signed craft manifest and the upstream artifact it names, then
builds the image locally. The trust mechanics are ADR-025's, under a
separate key class.

### Manifest format

A strict YAML document (unknown fields, duplicate keys, a second document
and any unlisted shape are refused). The signed message is the file's exact
bytes.

```yaml
format: homestead-craft-manifest/1
counter: 7                        # positive, incremented every manifest
context_digest: sha256:<64 hex>   # digest of images/craft (Dockerfile + context)
apps:
  - name: wordcraft               # ^[a-z][a-z0-9-]*$
    version: 1.2.3                # 1.2 or 1.2.3
    artifact:
      name: wordcraft-1.2.3.tar.gz   # bare .tar.gz, .tgz or .zip name
      urls:                       # optional https mirrors of the same bytes
        - https://example.org/wordcraft-1.2.3.tar.gz
      sha256: <64 hex>
      size: 12345678              # exact bytes; bounds the download
```

- `counter` is manifest-wide with ADR-025 semantics and one sequence per
  signing train: the baseline is recorded per app (`craft_baseline`, moved
  when a build is recorded), but a manifest whose counter is below the
  highest counter recorded for **any** craft app is refused unless the
  audited override is given (both counters recorded), so an old manifest
  cannot be replayed for an app never built. An equal counter with a
  different version, or with the same version built from different
  artifact or context digests, is refused with no override. The baseline
  moves only forward (guarded upsert) except under the audited override, and one build holds the pipeline
  lock from verify through record, so concurrent builds cannot lower it. An app absent from the manifest cannot be built from
  it. An update names the version the catalog wants and the manifest must
  carry exactly it.
- Signature: minisign non-prehashed `Ed`, as for platform releases;
  prehashed `ED` is rejected. A craft manifest is verified only against
  **craft** trust keys (`craft_trust_keys`, rendered to `craft-trust.yaml`
  beside `desired.yaml`), never platform keys, and the reverse; the embedded
  HAI craft key is a placeholder until the key ceremony (docs/DEFERRED.md).
  Manage keys with `homesteadctl craft trust list|add|replace|remove`, the
  Updates page Craft images panel, or `/api/v1/craft/trust-keys`; the audit
  actions are `craft.trust.add|replace|remove`.
- Build-context digest: SHA-256 over the text `<file sha256>  <path>\n`
  (sha256sum layout, `/` separators, no `./`) for every regular file under
  the context directory, sorted bytewise by path. Modes, timestamps and
  empty directories are not part of it. Symlinks, special files, newlines
  in names and a top-level `artifact` entry are refused. The daemon and
  `scripts/craft-manifest.sh` compute it identically (tested).

### Signing runbook (HAI and owners alike)

```
# 1. The artifacts (the upstream release files) sit in one directory.
scripts/craft-manifest.sh --counter 7 --artifact-dir /path/to/artifacts \
    --app wordcraft 1.2.3 wordcraft-1.2.3.tar.gz --url https://example.org/wordcraft-1.2.3.tar.gz \
    > craft-manifest.yaml
# 2. Sign with minisign (non-prehashed). The key stays off the repository.
scripts/sign-craft-manifest.sh craft-manifest.yaml --key /secure/craft.key
# 3. Ship craft-manifest.yaml and craft-manifest.yaml.minisig in the
#    catalog's craft/ directory (HAI), or keep them anywhere readable.
```

An owner who builds from their own artifact does the same with their own
key, then `homesteadctl craft trust add --key-file craft.pub`. Omit `--url`
for an artifact the owner supplies as a local file (offline). The daemon's
unit sets `ProtectHome=true` and `PrivateTmp=true`: keep supplied files
under `/srv` or `/opt`, not `/home`, `/tmp` or `/var/tmp`.

### Verify-then-build (`craft.build` operation)

`craft plan` runs the first four checks without fetching or building; the
build operation repeats everything. Order and failing-check names:

1. `signature`: the manifest verifies against a trusted craft key
   (unreadable inputs are `input`, a 400, never a verdict); then `manifest`
   (strict parse) and `counter` / `counter-conflict` against the app's
   baseline (only `counter` is overridable).
2. `disk-space`: free space on the staging directory (which must be a real
   directory owned by the daemon; it is forced to 0700, symlinks refused)
   and the Docker data root must cover twice the signed artifact size plus
   twice the expansion cap (default 1 GiB) plus 512 MiB, checked before any
   fetch; the refusal states the shortfall.
3. `artifact-digest`: the artifact is fetched from the signed URLs (https
   only, at most 5 https redirects, at most the signed size) or copied from
   the owner's local file under the S264 path rules (absolute, regular
   file, no symlink or FIFO, size-bounded); the bytes hashed are the bytes
   staged, and both SHA-256 and exact size must equal the signed values.
   `fetch` is a network failure (retryable); a mirror serving other bytes
   is skipped and the next URL tried, and every failure is reported only
   if all mirrors fail (`artifact-digest` when any served wrong bytes).
   Requests send `Accept-Encoding: identity`; a `Content-Encoding` reply
   is refused. Redirects to http are refused. Then `scan-archive`: the whole
   archive is read before any extraction and refused (check
   `artifact-digest`) unless it holds only regular files and directories
   with relative paths free of `..`, and expands to at most the cap.
4. `build-context-digest`: the catalog's `craft/context` is copied to a
   private staging tree and the digest is taken of the copy.
   The build context's Dockerfile must pin every external `FROM` by
   `@sha256:` digest (global `ARG` defaults are resolved), else
   `build-context-digest` refuses.
5. `build`: `docker build --network none` of the private copy with `ARTIFACT_FILE` and
   `ARTIFACT_SHA256` build arguments (the Dockerfile fails on a mismatch
   itself) to a unique staging tag, output captured (last 32 KiB, with
   credential shapes redacted). Cancellation is `cancelled`.
6. `record`: the image ID is read, the final tag
   `homestead/craft-<app>:<version>` is added, and the build is recorded
   (`craft_images`: image ID, counter, key id, artifact digest, context
   digest, manifest digest) with the baseline in one transaction. A failure
   restores or removes the final tag.

Signature, manifest, artifact-digest and context-digest failures have no
override. Network, Docker build, cancelled, disk-space and record failures
are retryable and leave no final tag, no record and no baseline change; the
staging tag and directory are removed on every path. Every outcome is the
app's latest verdict (`GET /api/v1/craft`) and the operation is audited as
`craft.build` (the override as `craft.build.override`, with both counters).

### Using the recorded digest

The install gate (`verify-craft-images` step) and every Compose `up`
(install, configure, restore, adoption, the migration database-only up and
start, and the update up) refuse a stack running a
`homestead/craft-<app>:<version>` image unless a build is recorded and the
local image behind the tag has the recorded ID. Live drift (S262) compares
a running container's image ID with the recorded one, not a tag or a
published digest. `craft status` shows each image as matching, differing or
missing. An update that moves a craft image verifies the manifest (shipped
at `<catalog>/craft/craft-manifest.yaml`, or `--craft-manifest`), rebuilds,
runs the normal update, and only then removes the previous version's tag
and record; a failed build or update keeps the previous image.

### Manual check

```
homesteadctl craft status
docker image inspect --format '{{.Id}}' homestead/craft-wordcraft:1.2.3
sqlite3 /var/lib/homestead/homestead.db \
  "select tag, image_id from craft_images"      # the two IDs must be equal
```

### Rules for catalog manifests (S249 onward)

A catalog service names a locally built image as
`homestead/craft-<app>:<version>` (`craft.ParseRef`). Catalog validation
(S249) refuses a registry-qualified reference, a tag for another app and a
tag whose version differs from the manifest version; `ValidateCraftListing`
and the shared `craftPackages` test require the tag to be listed in the
shipped craft manifest. The start gate covers every Compose start path:
`up`, the database-only `up SERVICE` of migrations, `start`, and the update
`up` (`docs/architecture/STATIC_CRAFT_PACKAGES.md`).

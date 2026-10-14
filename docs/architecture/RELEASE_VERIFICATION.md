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
`POST /api/v1/platform/updates/apply` (S265),
`GET|POST /api/v1/platform/trust-keys`,
`PUT|DELETE /api/v1/platform/trust-keys/{id}`; `homesteadctl updates
platform|verify|apply-platform|trust`; the dashboard Updates page. See docs/PARITY.md.

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

## Applying a verified release (S265)

`homesteadctl updates apply-platform --component homestead-server
--artifact /srv/rel/homestead-server [--allow-older] [--follow]`, or the
Apply action on the dashboard Updates page. Only a release that is
`verified` can be applied (failed, rollback-refused, and unverified
records never are). `--artifact` is the file the release was verified
from; the daemon refuses if its SHA-256 no longer matches the verified
digest. Apply is a `platform.update` operation: the first apply
requires `make install` to have been re-run so the unit grants write
access to `/usr/local/bin` (an older unit fails the replace step with a
read-only filesystem error and leaves everything unchanged).

Components: the daemon (`homestead-server`) and the CLI (`homesteadctl`)
apply separately, each from its own verified release (the checksums file
lists both). The dashboard is embedded in the daemon, so it updates with
it. The daemon restarts; the CLI does not. Apply the CLI after the
daemon so the two match.

Policy: an older counter than the installed release (or, with no
installed record yet, an older version) applies only with
`--allow-older` (dashboard: a separate confirmation); the apply and its
finalization are audited as overrides with both versions and counters.
The same counter with a different version is refused. Applying the
already installed version is refused (409). One apply at a time (409).

Steps of the operation: `stage` (copy the artifact into
`/var/lib/homestead/staging`, root-only, verify size and digest),
`preserve-previous` (copy the current binary to
`/usr/local/bin/<name>.previous` and record its version, counter, and
SHA-256 in `platform_previous`), `replace` (verify the staged digest,
copy to `<name>.new` beside the target, verify again, write the
SQLite marker, `rename(2)` over the target, fsync the directory), and
for the daemon `restart`.

Restart: `systemctl --no-block restart homestead-server.service`. The
job is queued in systemd and the command returns, so it survives the
daemon (and its cgroup) being stopped; a blocking restart would be
killed mid-job. The restart step then waits to be stopped (5 minutes)
and never reports success: the process ends. If the restart cannot be
queued, the new binary never ran, so the replacement is undone and the
marker cleared. If it is queued but nothing happens within the wait, the
operation fails and the marker stays: restarting the unit finalizes the
apply.

Finalization: the `platform_apply_marker` row (operation, component,
from and to version and counter, digest, previous binary, override,
actor) is written before the rename. At startup the daemon excludes that
operation from the interrupted-operation recovery pass
(`Runner.RecoverExcept`), and once the listeners answer it runs the
health check: the process reports the target version (a release binary
must be built with its version: `-ldflags "-X
homestead/internal/version.Version=<version>"`), `/proc/self/exe`
hashes to the verified digest, the database answers, `/healthz` on the
listen address reports that version, and the recovery socket accepts
connections. On success the operation is marked succeeded,
`RecordInstalled` moves the rollback baseline, the audit trail records
before and after, and the marker is cleared. On failure the operation
is marked failed, audited, the marker cleared, the installed record left
alone, and the previous binary kept. The CLI is health-checked in-process
by digest.

### Manual restore

After a failed apply (the operation error prints this command), put the
previous binary back; `homestead-server.previous` is exactly the binary
that ran before:

```
sha256sum /usr/local/bin/homestead-server.previous   # compare with platform previous (GET /api/v1/platform/updates, "previous")
sudo cp -p /usr/local/bin/homestead-server.previous /usr/local/bin/homestead-server.restore
sudo mv /usr/local/bin/homestead-server.restore /usr/local/bin/homestead-server
sudo systemctl restart homestead-server
```

The `mv` is atomic and safe while the binary runs. The installed
release record still names the old version, so rollback protection is
unchanged. A new binary that crashes at startup is restarted by systemd
with the marker pending; restore the same way (the marker clears on the
next healthy start). Automatic restore and a rollback route are S273:
`Finalizer.OnFailure` and `platformapply.RestorePrevious` are its seams.

### Building a test release

```
go build -ldflags "-X homestead/internal/version.Version=0.9.0" -o /srv/rel/homestead-server ./cmd/homestead-server
printf 'version=0.9.0\ncounter=90\n%s  homestead-server\n' "$(sha256sum /srv/rel/homestead-server | cut -d' ' -f1)" > /srv/rel/SHA256SUMS
minisign -G -p test.pub -s test.key   # once; then: homesteadctl updates trust add --key-file test.pub
minisign -S -s test.key -m /srv/rel/SHA256SUMS   # writes SHA256SUMS.minisig
homesteadctl updates verify --component homestead-server --artifact /srv/rel/homestead-server --checksums /srv/rel/SHA256SUMS --signature /srv/rel/SHA256SUMS.minisig
homesteadctl updates apply-platform --component homestead-server --artifact /srv/rel/homestead-server --follow
```

Keep artifacts outside `/home`, `/tmp`, and `/var/tmp`.

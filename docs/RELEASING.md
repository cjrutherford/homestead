# Releasing Homestead

The gate for a release, in order. Signing is offline and manual
(ADR-025, `docs/architecture/RELEASE_VERIFICATION.md`); CI never holds a key.

1. Open a PR to `main`. CI (`ci.yml`) must be green. Merge it.
2. Prepare the release on a branch (and merge it the same way):
   - `VERSION`, the default in `internal/version/version.go`, and
     `dashboard/package.json` all carry the new version
     (`tests/test_s332_release.py` checks they agree).
   - Raise `RELEASE_COUNTER` in the `Makefile`. It must increase with every
     release; appliances refuse a lower counter.
   - Add a `## X.Y.Z` section to `CHANGELOG.md`.
3. Tag the merged `main`: `git tag vX.Y.Z && git push origin vX.Y.Z`. The tag
   must equal `v` plus `VERSION` or the workflow fails.
4. `release.yml` runs the CI checks and `make release`, then creates a draft
   prerelease with the four binaries (`homestead-server` and `homesteadctl`,
   linux amd64 and arm64) and `SHA256SUMS`, with the CHANGELOG section as notes.
5. The owner downloads the assets, checks `sha256sum -c` on the lines in
   `SHA256SUMS`, then signs offline: `minisign -S -l -m SHA256SUMS`. The `-l` is required: minisign
   0.10+ defaults to prehashed `ED` signatures, which the appliance rejects.
6. Upload `SHA256SUMS.minisig` to the draft and publish it.

To try the build locally: `make dashboard-install release`; output is in
`dist/release/`. Names in `SHA256SUMS` carry the platform suffix; verification
matches the file name of the artifact the owner supplies.

## Debian packages

Build local-install packages for both supported CPU architectures with:

```bash
DEB_MAINTAINER='Name <email@example.org>' make deb
```

Set the maintainer field to the actual release owner/contact. The target uses
the same release build, so both packages include architecture-specific Go
binaries with the dashboard and owner guide embedded. It also packages the
curated catalog, craft/everything build contexts and pinned source archives.
Output is `dist/deb/homestead_<VERSION>-<revision>_amd64.deb` and `_arm64.deb`,
plus a `SHA256SUMS` file. `DEB_REVISION` defaults to `1` and can be increased
for packaging-only revisions. Package scripts start the daemon but do not
bootstrap Docker/cloudflared or change Docker address pools.

The `.deb` files are local-install artifacts; this target does not publish or
cryptographically sign them. Release signing remains the offline owner step
above. Build and inspect on Debian 13 for the target architecture before
distributing. The amd64 binaries have run on the Debian 13 review appliance,
but the `.deb` lifecycle has not been installed there. Arm64 is cross-built
and structurally checked only. Ubuntu package compatibility has not been
validated.

## Release signing key

The production key is generated and used offline by the owner (ADR-025,
amended by S493). The private key never enters the repository, CI, or the
appliance.

### Generate the key (once, offline)

1. On an offline machine with `minisign` installed, from a checkout:
   `scripts/release/keygen.sh --out ~/secure/homestead-release.key`.
   The path must be outside the repository and must not exist. minisign asks
   for the passphrase itself; the key file is mode 600.
2. The script prints the key ID and the public key line (also saved as
   `<path>.pub`). Hand over or commit only `.pub`. Back up the private key
   and passphrase off the machine.
3. Commit the `.pub` file as `release/release.pub`. `make release` finds it
   and builds it into both binaries as the embedded key (no placeholder). To
   use another file: `make release RELEASE_PUBLIC_KEY_FILE=path/to/key.pub`.
   Under the hood this sets
   `-X homestead/internal/release.ReleasePublicKey=<base64 key line>`.

### Rotate the key

Rotation is done on the appliance with trust configuration, in this order so
that updates never stop verifying:

1. Generate a new key with `keygen.sh` (above).
2. Trust the new key beside the old one: `homesteadctl updates trust add
   --key-file new.pub --label "release 2027"`, or Updates page, Trusted keys,
   paste the key, Add trusted key. `homesteadctl updates trust list` shows
   both.
3. Sign releases with the new key (`minisign -S -l -m SHA256SUMS -s new.key`).
   Releases signed with either key verify while both are trusted.
4. Once appliances have verified a release signed by the new key, retire the
   old key: `homesteadctl updates trust remove OLD_KEY_ID`, or the Remove
   button beside it on the Updates page. A release signed by the retired key
   is then refused. `replace OLD_KEY_ID --key-file new.pub` does steps 2 and 4
   in one move when no overlap is needed.
5. Ship the next binary with the new key embedded (`release/release.pub`).

Add, replace, and remove are audited (`platform.trust.add`, `.replace`,
`.remove`). A leaked key: remove it at once and rotate; see ADR-025.

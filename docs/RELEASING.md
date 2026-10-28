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
   `SHA256SUMS`, then signs offline: `minisign -S -m SHA256SUMS`.
6. Upload `SHA256SUMS.minisig` to the draft and publish it.

To try the build locally: `make dashboard-install release`; output is in
`dist/release/`. Names in `SHA256SUMS` carry the platform suffix; verification
matches the file name of the artifact the owner supplies.

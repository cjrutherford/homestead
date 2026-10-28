# S332 — Release 0.0.1

## Goal
Gate current main as Homestead 0.0.1, the first release, and make
releasing repeatable. Signing stays offline and manual (ADR-025).

## Scope
- `VERSION` is the single version source; `internal/version` default and
  `dashboard/package.json` follow it.
- `make release`: builds `homestead-server` and `homesteadctl` for linux
  amd64 and arm64 (pure Go, `CGO_ENABLED=0`, `-tags embeddash`, `-trimpath`,
  version linked in) into `dist/release/` with an ADR-025 `SHA256SUMS`
  (`version=`, `counter=` from `RELEASE_COUNTER`, then sha256 lines). It
  never signs or touches a key.
- `.github/workflows/release.yml`: on `v*` tags, checks the tag equals
  `v$(VERSION)`, runs the CI checks and `make release`, and creates a draft
  prerelease with the assets and the CHANGELOG section as notes.
- `CHANGELOG.md`, `docs/RELEASING.md`, a README link.

## Rules
AGENTS.md slice rules apply. No routes, commands or surfaces, so PARITY is
unchanged. No push, tag or release is made by this slice.

## Acceptance criteria
- `tests/test_s332_release.py`: VERSION, version.go and package.json agree;
  CHANGELOG has the section; the workflow and Makefile reference them.
- `make release` produces four ELF binaries and a `SHA256SUMS` with the
  header lines; `homestead-server -version` prints 0.0.1.
- All Go, Python and dashboard checks pass.

## Live check
After the tag, the draft prerelease lists four binaries and `SHA256SUMS`.

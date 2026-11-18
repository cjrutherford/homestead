# S511 — Owner-approved Debian package updates

## Goal

Extend the existing signed platform update flow to update a dpkg-managed
Homestead installation with its architecture-matched `.deb`, while retaining
the source-install binary updater and customer ownership.

## Dependencies

- ADR-025 signed release artifacts.
- ADR-027 privileged apply unit.
- ADR-063 Debian package update lifecycle.
- S509 Debian package contents and maintainer scripts.

## Scope

- Detect source versus dpkg-managed installs. Refuse direct binary updates on
  package-managed installs and refuse package updates on source installs.
- Verify signed `.deb` digest, package name, Debian package version, and
  appliance architecture before an owner can apply it.
- Show a bounded, no-download APT preview that contains no dependency changes
  or removals. Require owner approval bound to the exact preview digest.
- Recheck the release and preview in `homestead-apply.service`, then run
  `dpkg --install` on only the fixed staged Homestead package. Preserve the
  durable operation across the systemd restart.
- Verify dpkg state, daemon version, health, and database open before success.
  Record failure durably and retain staged inputs; never blindly downgrade or
  restore SQLite.
- Keep the API route set unchanged; extend the existing CLI and dashboard
  verify/apply inputs and update `docs/PARITY.md` and release guidance.

## Verification

- Focused Go tests for package metadata, architecture, APT preview guards,
  source/package mode boundaries, approval digest, apply recovery, and
  post-transaction verification.
- Dashboard and CLI tests for preview display and approval propagation.
- Build signed test `.deb` artifacts with an ephemeral test key; prove an
  untrusted artifact is rejected and a same-architecture package transaction
  completes in a disposable Debian/systemd environment if available.
- Do not change the user's review VM as part of this slice.

## Limits

- No release acquisition, public package repository, or production signing
  ceremony is added.
- Package downgrade/automatic rollback is intentionally unsupported.
- Source-to-package migration remains a separate owner-managed operation.

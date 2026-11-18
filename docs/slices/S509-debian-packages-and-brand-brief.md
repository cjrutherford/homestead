# S509 — Debian packages and branding handoff

## Goal

Build local-install Debian packages for amd64 and arm64 from the existing
release artifacts, and provide a standalone factual branding handoff.

## Dependencies

- S011 native systemd service and S012 filesystem layout.
- S031 embedded dashboard and S160 curated catalog.
- S332 release build, S493 embedded release key, and ADR-025 artifact trust.

## Scope

- `make deb` builds architecture-specific `.deb` files with the prebuilt
  daemon/CLI, embedded dashboard and guide, catalog, container build contexts,
  pinned source archives, and three systemd units.
- Package maintainer scripts create groups and state directories, start the
  daemon, and stop/disable Homestead services on removal. They preserve
  configuration, SQLite state, stacks, containers, volumes, and mounted data.
- The Debian package lifecycle does not bootstrap host dependencies, mutate
  Docker network configuration, or claim/configure the appliance.
- `docs/BRANDING_HANDOFF.md` records verified product facts, proposed
  positioning, boundaries, current visual assets, and requested design files.

## Verification

- `tests/test_s509_debian_packages.py` builds a fixture package, inspects
  metadata/assets, and exercises maintainer scripts with a temporary root and
  mocked service commands.
- Release artifacts are built for both architectures and inspected with
  `dpkg-deb` and `file`; the amd64 binaries have run on Debian 13, but this
  package lifecycle has not been installed on the review appliance.
- Arm64 binaries/package metadata are cross-build and archive-checked only;
  no arm64 runtime test is claimed.

## Limits

- This does not add `.deb` upload or signing to the release workflow.
- Ubuntu compatibility is not verified for the package artifacts.
- Package removal preserves app data; it is not a factory reset.

# Debian Package Platform Updates

## Status

Accepted

Decided by the owner on 2026-11-17.

Supersedes the binary-only apply procedure in ADR-027 for dpkg-managed
installations. ADR-027 remains in force for source-managed installations.

## Decision

Platform updates support two installation modes. A source-managed appliance
continues to update `homestead-server` and `homesteadctl` through the atomic
binary path in ADR-027. A dpkg-managed appliance updates the single `homestead`
Debian package through an explicit `homestead-deb` component. The package
contains both binaries, the embedded dashboard, curated catalog data, and
systemd units. Direct binary replacement is refused when dpkg owns the
installation. The updater does not convert a source install to a package
install; that transition remains an explicit owner operation.

The package follows ADR-025: the owner supplies a `.deb`, a checksums file
containing its exact filename, release version and monotonic counter, and a
minisign signature over that checksums file. Verification checks the signed
artifact digest, `Package: homestead`, the package version against the signed
release version, and `Architecture` against the appliance's dpkg architecture.
The package version must be strictly newer than the installed package. The
package mode does not permit downgrade overrides.

The existing Updates verify response includes a bounded APT simulation of the
exact verified package. It uses fixed `apt-get` arguments, `--no-download`,
`--no-remove`, and `--no-install-recommends`; the plan is refused unless the
only package action is an upgrade of `homestead`. The plan digest binds the
artifact digest, architecture, installed version, and normalized APT actions.
The owner explicitly approves this digest through the existing Updates apply
confirmation or the CLI. The daemon and apply unit both regenerate the plan;
the apply unit refuses if it differs from the approved plan.

Only `homestead-apply.service` runs the package transaction. It simulates the
APT plan with fixed arguments and a fixed staged path; caller input is never
interpreted as a command or package name. Apply invokes `dpkg --install` on
that exact package after the plan is regenerated and matched to the owner's
approval. Debian 13's APT local-archive transaction combined with
`--no-download` fails after converting the absolute archive path to a
basename. Using dpkg avoids APT's resolver and network entirely; if package
dependencies are absent, dpkg fails without installing or removing them. The
apply unit's write access is limited to dpkg metadata, the Homestead package
payload paths, and systemd enablement metadata required by the package's
existing maintainer scripts. It does not grant write access to appliance
configuration, SQLite state beyond the existing state grant, stacks, Docker
data, or mounted storage. The approved APT plan may not add, upgrade, or remove
dependencies.

The SQLite apply marker and operation survive the daemon restart initiated by
the package maintainer script. The unit verifies dpkg's final package version
and architecture, the running daemon version, the health endpoint, and the
database opening before it records success. A failure is durably recorded and
audited; staged package inputs are retained for diagnosis. It does not
automatically downgrade or restore SQLite: application migrations can be
irreversible and writes can resume before a post-restart failure is detected.
The owner must use the operation details and Debian's package recovery tools
to recover a failed or interrupted transaction. A stale marker is not itself
proof that dpkg is consistent.

## Consequences

- Existing source installs keep the current binary updater unchanged.
- Package-managed installs can update only the `homestead` package component;
  the package and source binary update modes cannot be mixed.
- The package signing key remains customer-controlled through ADR-025. Package
  publication and automated signing remain separate release-workflow work.
- Package updates are intentionally forward-only. A failed update requires an
  owner-supplied known-good package and a review of the database migration
  state before recovery.

## Alternatives considered

- Let the daemon run apt: rejected because the network-facing daemon must not
  write executables or package-managed files.
- Replace package files directly: rejected because it leaves dpkg's database
  inconsistent and bypasses package lifecycle scripts.
- Automatically downgrade after a failed health check: rejected because the
  database may already have migrated and accepted new writes.
- Support both source and package modes at once: rejected because package
  ownership and direct binary replacement would race over the same paths.

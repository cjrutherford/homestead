# Privileged Apply Unit for Platform Updates

## Status
Accepted

Owner decision 2026-10-13. Amends ADR-001 (Native Go Control Daemon) and
ADR-025 (Release Artifact Trust Root).

## Decision
`homestead-server` stays read-only on the locations the platform binaries
install to: its unit does not grant write access to `/usr/local/bin`. A
platform update therefore never lets the long-running, network-facing
daemon replace executables. The daemon only prepares an apply: it checks
the release, stages the artifact, checksums file and signature into a
root-owned staging directory under its state directory, writes a persisted
apply marker to SQLite, and starts a separate privileged oneshot unit,
`homestead-apply.service`.

The apply unit runs the installed `homestead-server` binary in an apply
mode (`homestead-server apply-platform`), not a separate binary. This keeps
one artifact per component, so the checksums file, the trust chain and
the component model of ADR-025 are unchanged, and it reuses the release
verification and operation code instead of duplicating it in a second
program. The unit is short-lived, listens on nothing, and takes no network
input: its only inputs are the marker, the staging directory and the
database. The logic that runs is the currently installed version's, so a
fix to apply logic reaches an appliance with the update after the one that
carries it.

The apply unit does not trust the daemon's verdict. It independently
re-verifies the staged files with the full ADR-025 chain: the minisign
signature and checksums against the trusted keys, the artifact digest, and
the counter against the installed baseline (an older counter only with the
recorded owner override). The database `verified` record is not
sufficient. It then keeps the previous binary beside the installed one,
replaces the binary atomically (same-filesystem rename, fsync), restarts
`homestead-server` through systemd for the daemon component, waits for the
daemon to be healthy within a bounded timeout, and finalizes the operation
record from outside the daemon by opening the store itself (SQLite
`busy_timeout` handles contention with the daemon). Health means the
restarted main process runs the verified digest, answers its listeners
reporting the target version, and the database opens.

On a failed apply the apply unit marks the operation failed, clears the
marker, audits the failure loudly, and records the manual restore command.
When the post-restart health check fails (S273) the same unit run first
restores the previous binary (atomic swap, digest checked against the
recorded previous digest), restarts the daemon, waits for it to be
healthy, and ends the operation `failed (rolled back)` with both versions
audited; the installed release record is never moved to the failed
release. If the rollback itself fails the operation says `rollback failed`,
both binaries stay on disk (the previous and `<binary>.rolledback`), and
the manual restore command is printed. A manual rollback is the same unit
run with a rollback marker.

Crash loops are bounded: `homestead-server.service` sets
`StartLimitIntervalSec` and `StartLimitBurst`, so a new binary that dies at
startup stops being restarted. A new binary that never becomes healthy is
caught by the apply unit's health timeout, so the operation cannot stay
running forever. An owner command (`homesteadctl updates platform
clear-apply`, and the dashboard action) marks a stale operation failed and
clears a stale marker, for the case where the apply unit itself died;
doctor warns while a marker outlives the health timeout and fails while the
latest apply failed.

## Consequences
- `homestead-server.service` loses the `/usr/local/bin` write grant;
  `homestead-apply.service` holds it, runs as root, and is installed
  (not enabled) by `make install`.
- An appliance installed before this decision needs `make install` re-run
  before its first apply; without the unit the apply fails before anything
  changes.
- The apply unit reads the trusted keys and installed baseline from the
  database, so a compromised daemon could still alter trust. The unit
  removes the daemon's ability to write binaries directly and makes
  swapped staged files fail verification, not the key store.
- The daemon operation stays open while the apply unit works; the unit
  advances and finalizes the same operation record. Startup recovery
  excludes the marker's operation.
- S273 adds automatic and manual rollback in the apply unit using the
  previous binary and its recorded digest. A manual rollback to an older
  counter than the installed release is the explicit, audited ADR-025
  owner override.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

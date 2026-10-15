# Release Artifact Trust Root

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
Platform release artifacts are always verified before use: the SHA-256 of
each artifact is checked against a checksums file, and the checksums file
is verified with a minisign Ed25519 signature. The HAI public key is
embedded in the binary.

### Signature scheme
Minisign **non-prehashed** (`Ed` algorithm) signatures. The signed message
is the checksums file itself, which is small, so there is no need for the
prehashed (`ED`, BLAKE2b-512) variant that exists for large files. The
non-prehashed form is the minisign default for small inputs, needs only the
standard library's `crypto/ed25519`, and avoids adding a BLAKE2b dependency.
Verification code parses the minisign public key and signature formats
directly; no minisign binary is required at runtime. Prehashed signatures
are rejected.

### Rollback protection
The signed checksums file carries a `version` (the release version) and a
monotonic `counter` (an integer incremented on every release). The
installed counter is recorded in SQLite. Artifacts whose counter is lower
than the installed one are refused. The owner can explicitly override
(`--allow-older` on the CLI, an equivalent confirmed action in the
dashboard); the override is audited with both versions and counters.
Equal counters with a different version, and checksums files missing
either field, are refused with no override.

### Owner trust
The owner adds or replaces trusted keys through **configuration** over the
API and CLI (stored in SQLite and rendered to inspectable YAML), so no
binary release is needed to rotate trust. The embedded HAI key is the
default; the owner may remove it. Trust remains with the customer.

### Failure
Signature or checksum verification failure blocks the update with no
override flag. (Only the rollback-counter check above is overridable,
because it concerns version policy, not authenticity.)

### Fetching
No fetching is added. Release acquisition remains deferred (DEFERRED.md,
"Platform release acquisition"); the owner supplies artifact, checksums,
and signature files locally. S194 and S243 model desired versions and the
CLI view only and do not fetch.

## Context
`internal/update/platform.go` `PlanPlatform` only compares owner-declared
running versus desired versions; nothing verifies artifact integrity or
origin. Actuating platform updates (replacing the daemon binary) without
verification would be a supply-chain hole.

## Alternatives considered
- Checksums only: protects against corruption, not a compromised mirror.
- Trust-on-first-use: weak against first-fetch attack.
- Hosted HAI update service: deferred (hosted control service) and
  conflicts with no runtime lock-in.
- Hard-coded key with no owner override: violates customer ownership.
- Prehashed minisign: needed only for large files; adds BLAKE2b.

## Amended by ADR-027
Platform binary replacement happens in a separate privileged apply unit, not in the daemon; see ADR-027.

## Amended by ADR-028
The same trust chain covers craft container images through a signed image
manifest verified on the appliance; ADR-028 holds the substance.

## Consequences
- Key rotation for HAI is an owner key import (or a binary release).
- Release process must publish checksums with version and counter, and
  signatures.
- A lost or leaked HAI signing key requires owners to replace the trusted
  key by configuration; documented in the operations runbook.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

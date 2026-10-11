# Release Artifact Trust Root

## Status
Proposed

## Decision
Platform release artifacts are always verified before use: the SHA-256 of
each artifact is checked against a checksums file, and the checksums file
is verified with an Ed25519 signature (minisign-compatible). The HAI
public key is embedded in the binary. The owner may add or replace
trusted keys, so trust remains with the customer. Verification failure
blocks the update with no override flag. No automatic fetching is added in
v1 beyond what S194 and S243 already allow.

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

## Consequences
- Key rotation for HAI requires a binary release or owner key import.
- Release process must publish checksums and signatures.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

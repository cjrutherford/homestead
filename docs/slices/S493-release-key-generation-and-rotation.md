# S493 - Release signing key generation and rotation

## Goal and dependencies
Owner 2026-11-08: a script to generate the release minisign keypair offline, a working rotation procedure, and a seam to swap the owner's public key for the embedded placeholder. Depends on S264 (trust keys) and S332 (release build). The production key is NOT generated here.

## Already existed
Add, replace, remove and list of trusted platform keys: API (`/api/v1/platform/trust-keys`), `homesteadctl updates trust`, Updates page Trusted keys section, audit entries. Nothing was added to them.

## Added
- `scripts/release/keygen.sh --out PATH`: wraps `minisign -G`; refuses a path inside the repository (symlinks resolved), an existing key, or a missing directory; mode 600; minisign prompts for the passphrase; prints only key ID and public key; writes `PATH.pub`.
- `internal/release.ReleasePublicKey` (ldflags `-X`) and `newPlatformClass`: when set, the embedded key is the production key (label "HAI release key", not a placeholder). `make release` sets it from `release/release.pub` or `RELEASE_PUBLIC_KEY_FILE`.
- Go tests: build-time seam, and rotation end to end (trust new beside old, both verify, retire old, only new verifies).
- Owner doc: `docs/RELEASING.md` "Release signing key"; ADR-025 amendment.

## Verification
`internal/release/store_test.go`, `tests/test_s493_release_keygen.py` (stub minisign, real minisign when installed), live on VM 2 with a throwaway key (PLAT-UPDATE-4).

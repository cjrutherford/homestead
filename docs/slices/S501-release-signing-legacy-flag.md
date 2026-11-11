# S501 - Release signing docs use non-prehashed minisign

## Goal and dependencies
The signing commands in the docs must produce signatures the appliance accepts. Depends on S264 and S493.

## Problem
The verifier (`internal/release/verify.go`, ParseSignature) rejects prehashed `ED` signatures. minisign 0.10+ prehashes by default, so the signing command without `-l` in `docs/RELEASING.md` and `docs/architecture/RELEASE_VERIFICATION.md` yields a signature the appliance refuses. Non-prehashed `Ed` needs `-l`; `scripts/sign-craft-manifest.sh` already uses it.

## Fix
- Every signing command in `docs/` now passes the legacy flag; RELEASING.md step 5 says why.
- A unittest scans `docs/**/*.md` and `scripts/*.sh` and fails on any minisign signing line that lacks the legacy flag.

## Verification
`tests/test_s501_release_signing_docs.py`.

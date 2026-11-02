# S367 — Everything bridge and build pipeline

## Status
Implementation in progress. S367 is not complete and no Everything app is installable yet.

## Goal and dependencies
Prove an independent, customer-owned bridge and reproducible build for selected Everything apps, beginning with Tasks. S279 and S341 are present; S382's craft serving and DNS validation is still unfinished. ADR-053 constrains the new architecture; implementation details must meet its security and recovery requirements. Do not ship the pilot without its own complete build, serve and reachability proof.

## Evidence
Upstream candidate `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a` was inspected on 2026-10-29. In `src/tasks`, `npm ci --ignore-scripts`, `npm test` and `npm run build` succeeded with Node 24: 12 test files and 235 tests passed. These tests install upstream's in-memory mock, so this is an upstream baseline only, not an exit check. Build requires the repository's `dev/puter-localhost.mjs` as well as `src/tasks`. The 84-file source snapshot was compared byte-for-byte by Git blob digest against the pinned checkout; its archive SHA-256 is `864f60484a1aa759427fec5d308a2215200fa5873d7579bb73594850b8f5db2e`. The app uses authentication, key-value documents, file archives, offline synchronization, share links and optional collaborative key-value sharing. The older estimate of one file operation and three authentication calls is not representative of this revision.


## Work in progress
`third_party/everything/` contains the pinned Tasks source and its SHA-256. The experimental `scripts/everything/prepare_tasks.py` verifies the archive, requires a caller-supplied local bridge, and replaces the remote SDK tag and upstream service worker. The replacement worker caches only its static shell and listed assets, bypassing bridge data; `scripts/everything/test_tasks_sw.cjs` simulates its fetch and cache handling. A prepared source build succeeded with an intentionally nonfunctional bridge stub and no SDK host in the resulting index or service worker. A standalone SQLite store prototype now isolates records by app and verified-subject scope, supports bounded values and atomic batches, and has restart, concurrent access and SQLite snapshot tests. A prototype request handler requires an injected identity verifier, exact allowed origin and CSRF check; its tests use a fake verifier. This is not a running app or a security acceptance test: no real authentication implementation, JS bridge, sharing, total storage quota, CSP, real-browser service-worker validation, Docker image, or integrated backup/restore exists yet. The upstream suite still uses its own mock.

## Acceptance gate
- Resolve ADR-053, select and verify a pinned source and licence, remove the remote SDK from built assets, and build a Docker-served app reproducibly.
- Define and exercise a customer-owned per-person, per-app data and authorization boundary that operates with its Compose stack without the Homestead daemon. Authentik integration and local recovery must be documented and tested.
- Run upstream Tasks behavior against the actual bridge, not the upstream mock; cover data persistence, offline replay, concurrent clients, private isolation, sharing authorization and failed or revoked access. A pass of the unmodified upstream mock suite alone is insufficient.
- Prove explicit exposure, same-origin/CSRF controls, mounted storage, backup/restore and daemon-independent operation. Document and test unsupported optional capabilities without implying that they work.
- S368–S381 remain blocked until this gate passes. If it cannot pass, record the concrete failure and stop Track B.

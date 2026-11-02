# S367 — Everything bridge and build pipeline

## Status
Implementation in progress. S367 is not complete and no Everything app is installable yet.

## Goal and dependencies
Prove an independent, customer-owned bridge and reproducible build for selected Everything apps, beginning with Tasks. S279 and S341 are present; S382's craft serving and DNS validation is still unfinished. ADR-053 constrains the new architecture; implementation details must meet its security and recovery requirements. Do not ship the pilot without its own complete build, serve and reachability proof.

## Evidence
Upstream candidate `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a` was inspected on 2026-10-29. In `src/tasks`, `npm ci --ignore-scripts`, `npm test` and `npm run build` succeeded with Node 24: 12 test files and 235 tests passed. These tests install upstream's in-memory mock, so this is an upstream baseline only, not an exit check. Build requires the repository's `dev/puter-localhost.mjs` as well as `src/tasks`. The 84-file source snapshot was compared byte-for-byte by Git blob digest against the pinned checkout; its archive SHA-256 is `864f60484a1aa759427fec5d308a2215200fa5873d7579bb73594850b8f5db2e`. The app uses authentication, key-value documents, file archives, offline synchronization, share links and optional collaborative key-value sharing. The older estimate of one file operation and three authentication calls is not representative of this revision.


## Work in progress
`third_party/everything/` contains the pinned Tasks source and its SHA-256. The experimental `scripts/everything/prepare_tasks.py` verifies the archive, requires a caller-supplied local bridge, and replaces the remote SDK tag and upstream service worker. The replacement worker caches only its static shell and listed assets, bypassing bridge data; `scripts/everything/test_tasks_sw.cjs` simulates its fetch and cache handling. A prepared source build succeeded with an intentionally nonfunctional bridge stub and no SDK host in the resulting index or service worker. A standalone SQLite store prototype now isolates records by app and verified-subject scope, supports bounded values and atomic batches, and has restart, concurrent access and SQLite snapshot tests. A prototype request handler requires an injected identity verifier, exact allowed origin and CSRF check; its tests use a fake verifier. This is not a running app or a security acceptance test: no real authentication implementation, JS bridge, sharing, total storage quota, CSP, real-browser service-worker validation, Docker image, or integrated backup/restore exists yet. The upstream suite still uses its own mock.

### Bridge, SDK shim and behavioural gate (added 2026-11-02)
New code: `scripts/everything/puter-bridge.js` (the `puter` global, same-origin POST to `./_homestead/data`), `internal/everything/{server,devauth}.go` and `cmd/everything-bridge` (standalone server: SQLite store plus handler; handler gained `whoami` and atomic `putbatch`), and `scripts/everything/gate/` (`run_gate.sh`, harness, replacement `mock-puter.js`, `bridge-gate.test.js`).

Identity: the production build (no tags) has no verifier and refuses to start. The dev verifier (`devauth.go`, `identity_dev.go`) is compiled only with `-tags everythingdev`, and also needs `-dev-identity`, `HOMESTEAD_EVERYTHING_INSECURE_DEV=i-understand-this-is-not-authentication` and a loopback listen address. It is not authentication. TODO(S367): real Authentik verification. ADR-053 does not choose between a forward-auth header from the Homestead edge and OIDC, nor define the CSRF token source (the shim reads `<meta name="homestead-csrf">`); this needs an owner/ADR decision before it is built.

Puter SDK calls Tasks makes (grep of `src/`, excluding tests and `dev/puter-mock.js`):

| Call | Used in | Bridge |
| --- | --- | --- |
| `kv.get(key)` | store.js init/refresh | implemented (kv get) |
| `kv.set(key, env)` | store.js save/flush | implemented (kv put) |
| `kv.set([{key,value}], opts)`, `kv.list`, `kv.del` | store.js KV-collab paths and share reaper | implemented (atomic `putbatch`, prefix list, delete); `{handle}` options refused |
| `fs.write/read/delete` | logbook months, share snapshots, ICS feed, collab files | implemented (file kind, path normalised under the app/person scope) |
| `fs.getReadURL` | share links, ICS feed URL, collab manifests | fail-closed: rejects `bridge_unsupported` (signed public URLs need an exposure plan) |
| `auth.isSignedIn`, `auth.getUser` | main.js, store.js | implemented (`whoami`; username and uuid are the verified subject) |
| `auth.signIn`, `auth.signOut` | auth view, settings | redirect to configured Homestead URLs, otherwise reject; `env` is `app`, which hides the logout button |
| `env` | settings.js | constant `app` |
| `kv.share/unshare/listShared/listSharedByMe`, `perms.*`, `events.onLocal`, `appID` | KV collaboration | not provided: Tasks falls back to link mode, which is refused by `getReadURL` |
| `typeof puter` offline shim, `puter.offlineShim` | main.js | untouched (used when the script fails to load) |

Gate results (`scripts/everything/gate/run_gate.sh <empty dir> suite|bridge`, Node 24, pinned source, `npm ci --ignore-scripts`; real Go bridge process with temp SQLite and the dev identity; no upstream mock):
- `bridge` mode, Tasks' real `Store` through the shim: 6/6 pass: persistence across bridge restart; two concurrent clients converge (last-write-wins per key, healed by Tasks' record merge after re-reads); person B cannot read A's KV or files and a client-supplied subject is ignored; sharing, collaboration and signed URLs refused (`bridge_unsupported`); missing, forged, revoked, expired session, wrong CSRF and foreign Origin refused; oversize values refused and a duplicate-key batch rolls back.
- `suite` mode, the 12 upstream test files with `tests/helpers/mock-puter.js` replaced by a bridge-backed helper (kvMap/fileMap are write-through mirrors; each reset uses a fresh bridge subject): 172 of 235 pass. The 63 failures are all sharing: 27 collab, 23 kvshare (the other 1 passes), 13 store (snapshot share links, ICS feed, wipe and import of share files) fail because `fs.getReadURL` is refused or the KV-share surface is absent; 2 of the kvshare failures are on the missing `perms`. None fail for storage, merge, offline or isolation reasons.  The pure-logic files (dates, ics, quick actions, parser, recurrence, reminders, share viewer) pass in full, as do offline 8/8 and sync 19/19; store passes 39/52.
- Limits: helper mirrors are not the real shared account semantics for cross-account tests; shares are not supported at all, so those upstream tests cannot pass. This is the chosen handling of sharing under ADR-053 (refuse), not a pass. `enableShare` writes its private snapshot file before the URL is refused; the file stays inside the person's own scope.

Remaining for S367: real Authentik verification and CSRF source; Docker image, Compose wiring, catalog package; total quota; CSP and real-browser service-worker test; export tool; backup/restore and daemon-independence proof; a decision on whether to hide share/ICS-feed UI for this deployment.

## Acceptance gate
- Resolve ADR-053, select and verify a pinned source and licence, remove the remote SDK from built assets, and build a Docker-served app reproducibly.
- Define and exercise a customer-owned per-person, per-app data and authorization boundary that operates with its Compose stack without the Homestead daemon. Authentik integration and local recovery must be documented and tested.
- Run upstream Tasks behavior against the actual bridge, not the upstream mock; cover data persistence, offline replay, concurrent clients, private isolation, sharing authorization and failed or revoked access. A pass of the unmodified upstream mock suite alone is insufficient.
- Prove explicit exposure, same-origin/CSRF controls, mounted storage, backup/restore and daemon-independent operation. Document and test unsupported optional capabilities without implying that they work.
- S368–S381 remain blocked until this gate passes. If it cannot pass, record the concrete failure and stop Track B.

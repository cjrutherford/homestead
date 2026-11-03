# Everything apps as catalog packages

## Status
Accepted

Owner decision 2026-11-02, after the S367 spike: "a short ADR plus a new slice
makes Tasks an ordinary catalog package: locally built image, path route,
sign-in through the normal plan". Builds on ADR-053 (Everything application
bridge), ADR-028 (locally built craft images), ADR-039 (path serving), ADR-043
(closed-by-default apps) and ADR-047 (public issuer sign-in). Implemented by
S368 (Tasks).

## Context
S367 proved that an Everything app (an upstream Puter-SDK web app) can run on a
customer-owned bridge with no Puter service. It ran under hand-written Compose
with a hand-made Authentik client. It is not yet something an owner can install
from the catalog.

The owner rejected extending the owner-signed craft manifest
(`catalog/craft/craft-manifest.yaml`, counter 8) for these images. That
manifest and `images/craft` stay exactly as they are.

## Decision
**One recipe, one image per app, built on the appliance.**
- `images/everything/Dockerfile` is one shared recipe. The build argument `APP`
  selects the app definition; the per-app data lives in
  `scripts/everything/apps/<app>.json` (pinned archive name, SHA-256 and size,
  subtree, patches, service worker template, SDK surface).
- The image is `homestead/everything-<app>:<version>`, built from the pinned
  in-repo source (`third_party/everything/<archive>`) and the bridge sources
  (`cmd/everything-bridge`, `internal/everything`, `scripts/everything`). The
  Dockerfile is digest-pinned for its base images and uses lockfiles
  (`npm ci --ignore-scripts`, `go.sum`). The runtime is distroless, non-root.

**Install path (as implemented).**
1. `scripts/install-systemd.sh` ships the build context to
   `<catalog>/everything/context` and the pinned archives to
   `<catalog>/everything/sources`, next to (not inside) the signed craft
   context.
2. The existing craft `plan` and `build` operations accept the app name. The
   bridge pipeline (`internal/craft/bridge.go`) reuses the craft machinery:
   staging, disk preflight, private context copy compared by tree digest,
   pinned-base check, archive scan, docker builder and the craft image record.
   It differs in two ways: the archive is checked against the definition's
   SHA-256 and size (not the craft manifest), and the build has network access
   because `npm ci` and `go mod download` need it. A bridge build never touches
   the craft baseline or the signed-manifest counter.
3. The catalog package (`catalog/tasks.yaml`) names the local image. The
   existing image gates (`CheckImage`, recorded ID) refuse to start it until it
   is built.

**Integrity.** The archive SHA-256 in `third_party/everything/pin.yaml` and in
the app definition, cross-checked by a test against the real archive, plus the
signed Homestead release that carries both. It is not the craft manifest. The
owner-signed craft manifest is untouched.

**Serving.** By path, `/apps/<app>/`, without prefix stripping (`serve: path`;
the catalog refuses `strip_prefix` for these apps). The bridge is given
`BASE_PATH`, `ORIGIN` and its redirect URL from route tokens.

**Sign-in.** The normal sign-in plan with the generic `env-oidc` adapter. The
bridge is its own Authentik OIDC client (ADR-053): redirect
`<app_url>/_homestead/callback`, sign-in path `/_homestead/login`, client
secret passed as a file (`HOMESTEAD_EVERYTHING_OIDC_CLIENT_SECRET_FILE`). The
package starts in await-sign-in mode and answers 503 "Sign-in is not set up"
until the plan is applied; a half-configured bridge refuses to start.

**Access.** Closed by default (ADR-043): nobody but the owner reaches the app
until granted. Data is scoped by (app, `sub`); quotas apply per person and per
app.

**Browser safety.** The bridge sets a fail-closed Content-Security-Policy:
`default-src 'none'`, scripts from self plus hashes of the inline scripts in the
built index, no framing, no objects, no base-uri.

**Backup.** The bridge's SQLite database lives under `data/`, a catalog backup
path. The `backup` snapshot takes a consistent copy.

**SDK surface per app.** Each definition lists what the app may call. Anything
not supported fails closed with `bridge_unsupported`.

| Capability | Tasks / Projects |
| --- | --- |
| `kv.get/set/list/del`, `fs.write/read/delete` | supported, scoped to the person |
| `kv.list(prefix*, true)` with values, in bounded pages of at most 500 entries or about 4 MiB, so a book longer than the 1000-key list limit loads (S372; read-only, KV only) | supported, scoped to the person |
| `fs.stat`, `fs.readdir`, recursive `fs.delete` (S369) | supported, scoped to the person; folders are key prefixes |
| `auth.isSignedIn`, `auth.getUser`, `env` | supported; `getUser` returns the person's own `preferred_username` and a per-app pseudonymous uuid, never the `sub` (S425, ADR-058) |
| `fs.rename(path, newName)` (S375; same folder, never replaces, quota-checked) | supported, scoped to the person |
| `kv.share`, `kv.unshare`, `kv.listShared`, `kv.listSharedByMe`, the kv `{handle}` option, `perms.request`, `appID` | supported by ADR-058 (S428); answers `kv_handles_disabled` unless the bridge runs with `-sharing` |
| `fs.share`, `fs.unshare`, `fs.getShares`, `fs.listShared`, `~shared/<id>/...` paths for `fs.read/write/stat/readdir/rename/delete` | supported by ADR-058 (S429); answers `fs_sharing_disabled` unless the bridge runs with `-sharing` |
| `fs.getReadURL`, `events` | refused |
| `ai` | disabled (no backend) |
| `net` | disabled (only if S381 adds a network bridge) |
| WebAssembly (S375) | allowed only for a build that ships a `.wasm` file: the page policy adds `'wasm-unsafe-eval'`; JavaScript eval stays refused |
| sharing / calendar feed UI | hidden by a pinned patch (needs an exposure plan) |

**Adding an app (S369 and later).** Add `scripts/everything/apps/<app>.json`,
the pinned archive and a `pin.yaml` entry, `catalog/<app>.yaml`, an
`everythingPackages` row and the SSO posture and image volume rows in the
catalog tests, then run `scripts/everything/sync-context.sh`. No Go change.

## Amendment 2026-11-02 (S423): source offer for copyleft apps
Not an architecture change. Owner decision: "we can link to the agpl or lgpl
source directly on the page we serve." An app whose definition has a
`source_offer` (Notes and 3D, AGPL-3.0-only; Parametric, which bundles the
LGPL-2.1-only `replicad-opencascadejs` kernel) gets, from the same bridge:

- `GET <base>/_homestead/source`, a script-free HTML page (its own policy, no
  `script-src`) with the licence, upstream repository, pinned commit, the
  tarball link and any bundled components with their licence and source link;
- `GET <base>/_homestead/source/<app>-source.tar.gz` for AGPL apps: the
  deterministic Corresponding Source (pinned upstream archive byte for byte,
  the patch list, added files, shim, preparation script, image recipe and a
  README with the rebuild command), built into the image at `/srv/source`;
- a small fixed "Source" link added to the served `index.html` right after
  `<body>`, a style attribute only (no script, so the page policy is unchanged).

Both routes need a sign-in (page: redirect to sign-in, tarball: 401). AGPL
section 13 owes the offer to the people who use the app over the network; all of
them are signed in, and the catalog is closed by default. Apps without a
`source_offer` answer 404 and carry no link. Nothing is fetched at run time.

## Note 2026-11-03: refused rows under review
ADR-058 (Accepted 2026-11-03) replaces the refused rows (`fs.getReadURL`, `kv`
sharing, `perms`, `events`, `appID`, `net`) with bridge-provided versions, one
slice at a time. Until the slice for a row lands, this table stands.

## Consequences
- Tasks becomes installable, signed in and backed up like any package.
- The build needs network access on the appliance at install time.
- `images/everything` carries a generated copy of the bridge sources; a test
  fails when it drifts from the originals.
- Cross-engine byte-identical images are not promised (see DEFERRED).
- The CSP and the full flow are covered by Go and gate tests, and by the owner
  review plan in `docs/reviews/EVERYTHING_REVIEW_PLAN.md`; they were not
  verified in a browser by the implementer.

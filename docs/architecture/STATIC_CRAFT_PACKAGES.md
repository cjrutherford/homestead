# Static Craft Packages (S249, ADR-018, ADR-028, ADR-030)

The shared shape for the storytold craft apps: a browser application
shipped as a static release artifact, served by a small non-root nginx
container on its own internal host. WordCraft (S249) is the reference;
S250 to S253 add YAML, manifest entries, test rows and docs, and no Go.

## What a craft package is

- One catalog manifest `catalog/<app>.yaml`, shaped like
  `catalog/wordcraft.yaml`: one service on the local image
  `homestead/craft-<app>:<version>`, `exposes: web`, tiny `requirements`
  (memory and CPU only: no `ports`, because the container publishes no
  host port and the daemon itself listens on 8080, and no storage class),
  no `backuppaths`, no database, `sso.class: unsupported`, a title, blurb and description that
  say what the app does (never "clean-room", which is upstream's own
  statement and not ours).
- The image is **built on the appliance** from two verified inputs: the
  upstream release artifact `<app>-web-<version>.zip` and the shared
  Dockerfile and nginx config in `images/craft/`. Nothing is pulled and no
  image is published. The tag's `<version>` equals the manifest `version`
  (catalog validation enforces it), so a version bump moves the manifest,
  the craft manifest and the image together.
- A signed **craft manifest** (`catalog/craft/craft-manifest.yaml` plus
  `.minisig`) records the artifact URL, SHA-256 and size, the build-context
  digest and a monotonic counter. The daemon verifies it again at plan and
  build time (S279). The owner signs offline:
  `docs/architecture/CRAFT_SIGNING_RUNBOOK.md`.
- Hosts are HTTPS from the internal CA (ADR-030): the route is local-only
  with TLS `internal`, so a secure context exists for WebGPU and the
  clipboard once the owner trusts the root.

## Image rules (enforced in `images/craft/`)

`TestCraftBuildContextEnforcesTheSharedShape` pins these:

- Non-root: `USER nginx`; nginx listens on **8080**; nothing binds a
  privileged port.
- Dotfiles and dot directories in the artifact are deleted at build and
  answered 404 by a first regex location, so `.git`, `.env` and editor
  leftovers are never served.
- `GET /healthz` returns 200 `ok`; the Dockerfile `HEALTHCHECK` uses it.
- Zero data and no volumes: no `VOLUME`, no state. The only writable path
  is `/tmp` (pid file and request temp directories), declared by every
  package as one `ephemeral` data path, which Compose renders as a tmpfs.
  The root filesystem can be read-only, but the catalog has no field for it
  (docs/DEFERRED.md).
- Precompressed assets: the build gzips text and wasm files beside the
  originals and `gzip_static on` serves them; `gzip on` covers the rest.
- `application/wasm` for `.wasm`; `no-cache` for the HTML entry and
  anything unhashed; `public, max-age=31536000, immutable` for
  content-hashed assets; `X-Content-Type-Options: nosniff`.
- The extract stage accepts `.tar.gz`, `.tgz` and `.zip`, and strips a
  single top-level directory (the storytold zips unpack into
  `<app>-web-<version>/`). It fails unless `index.html` results.

An app that keeps the same file names across versions (LightCraft, S251) is
served correctly by these rules without change: unhashed js, wasm, worker
and font files get `no-cache`, never `immutable`, and the wasm still gets its
own MIME type. Verify such an app with `craft-header-check.sh --unhashed`.

An app that loads its unhashed files with a `?v=<hash>` query (FilmCraft,
S252) is served the same way: the rules key on the file name, so those files
are `no-cache` and the check script runs with `--unhashed`.

Browser capabilities (S252): a manifest may declare a `browser:` list of
`capability` and `reason` entries (closed set: `webcodecs`, `webgpu`,
`webgl2`, `webaudio`, `audio-output`, `secure-context`, `clipboard`,
`browser-storage`; reason 1-300 characters). They are advisories only. The
install plan returns them as `BrowserAdvisories` with a note that the
appliance cannot see the browser; the dashboard shows the same list on the
package page and the reviewed plan, and the CLI prints `browser:` lines.
Nothing refuses install, update or start on them (hard refusals are
deferred, `docs/DEFERRED.md`). The field is optional metadata in the
manifest schema; the signed craft manifest is unchanged by it.

Naming (S253): the manifest app name is the catalog package name and the
repository-style name, not necessarily the release asset's. The craft
manifest records the asset name separately (`artifact.name`) and the shared
Dockerfile strips the single top-level directory whatever it is called, so
the two names never need to match; `ParseRef` accepts any
`^[a-z][a-z0-9-]*$` app name (including one that starts with `craft-`, which
repeats in the tag) and `TestParseRef` pins that round trip. The one package
that exercised this, Craft Launcher (`artcraft-launcher-web-<version>.zip`),
was removed from the catalog on 2026-10-17 (owner decision); a Homestead-owned
craft apps start page is planned instead (`docs/DEFERRED.md`).

Anything app-specific beyond these (COOP/COEP for `SharedArrayBuffer`,
`.part-*` caching) is recorded per app in
`docs/architecture/CRAFT_HEADER_TABLE.md` and added in that app's slice
with a test. Changing any file under `images/craft/` changes the
build-context digest and needs a regenerated and re-signed craft manifest.

## The stateless rule (ADR-028)

A package with no non-ephemeral data declaration, no `backuppaths` and no
database export is **stateless** (`Manifest.Stateless()`). The rule derives
from the manifest, so a package that later gains persistent data stops
being stateless by itself. Stateless packages:

- cannot be scheduled: `POST /api/v1/backups/schedules` returns 422 naming
  the package; `GET /api/v1/backups/schedules/{package}` answers 200 with
  `stateless: true` instead of 404;
- never become scheduler jobs (no `ErrUnavailable` retry loop, no
  skipped-run noise), so `GET /api/v1/scheduler/jobs` has no row for them;
- are skipped by a pre-update backup request with a recorded note (the
  apply reply's `detail` and an `update.backup.skipped` audit entry);
  the update proceeds;
- take no owner volumes: `PlanInstall` refuses `volumes` on a stateless
  package, because an owner mount would hold data that none of the paths
  above would ever back up;
- return `backup.ErrStateless` from `backupJob` and `guardBackupSources`,
  which is distinct from "no sources" (retryable for stateful packages).

Documents live in the browser until the Nextcloud wiring (S254, ADR-029).
That is why exposure defaults to `local` with `allowwider: false` and a
warning; the owner can still override through the ADR-006 plan.

## Install, start and update gates

- Install adds a first `verify-craft-images` step; every Compose start the
  catalog performs takes the same gate (`composeUp`, the database-only
  `composeUpServices` of migrations, `composeStart`, and the update `up`):
  the stack's compose files are read raw, `${VAR}` image references are
  expanded, and each craft image must match its recorded local build or the
  start is refused. With no checker wired, craft images fail closed. The
  gate reads every file Compose could load (the `compose` and
  `docker-compose` names, overrides, and any `COMPOSE_FILE` from the stack
  `.env` or the daemon environment), and secret-consumer restarts take it
  too.
- The planner always joins a routed stack to the `homestead-edge` network
  (and keeps the project default network when the caller named none).
  Only the routed services join the edge network the planner adds;
  databases and caches keep the package network. The network is created
  when the edge proxy is set up, so an install before that fails at
  Compose with the missing external network, and the plan notes say so.
  Before S249 the edge network came only from the caller's `networks` list
  (the dashboard sent it, the CLI and acceptance flows did not), so the
  proxy had no path to the app and its route answered nothing.
- Zero-mount packages are valid end to end: no storage assignment, no
  provisioned directory, no bind mount, one tmpfs.

## Adding a craft app (S250 to S253)

Per app, and nothing else:

1. `catalog/<app>.yaml` copied from `catalog/wordcraft.yaml` (name,
   version = the vendored release, texts, icon); add the name to
   `curatedPackages` in `internal/catalog/curated_test.go` and the SSO
   posture to `internal/catalog/sso_test.go`.
2. Fetch the release into a fresh directory, cross-check the zip against
   `SHA256SUMS.txt`, read `LICENSE` and `NOTICE`, record version, URL,
   SHA-256, licence judgement and NOTICE in `docs/THIRD_PARTY.md` (the
   shared test requires `storytold/<app>`, the version, the SHA-256 and the
   artifact name there).
3. Add the app to the craft manifest with `scripts/craft-manifest.sh`
   (keep every shipped app in one manifest, counter above the last) and
   have the owner sign it.
4. Add the app to `craftPackages` in `internal/catalog/craft_test.go` and a
   `knownImageVolumes` row in `internal/catalog/data_test.go` for the
   locally built tag (`docker image inspect` on the KVM appliance).
5. Build the image, run it, and fill a row in
   `docs/architecture/CRAFT_HEADER_TABLE.md` from
   `scripts/craft-header-check.sh`, including the COOP/COEP question for
   that app. Do not copy another app's row.
6. KVM acceptance as in the slice file.

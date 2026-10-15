# Craft Suite Packaging and Image Trust

## Status
Accepted

Revised 2026-10-14: image publishing replaced by local build only and
signing moved to a separate craft key, per owner decision, before any
implementation. Supersedes the earlier HAI-built, registry-published image
and image-manifest text. S240 is no longer a dependency.

Owner decisions 2026-10-14. Amends ADR-018 (Static Container Packages) and
ADR-023 (Daemon Scheduler); extends ADR-025 (Release Artifact Trust Root)
to locally built container images.

## Decision
### Vendoring, not re-implementation
The craft apps (wordcraft, gridcraft, pdfcraft, deckcraft, vectorcraft,
photocraft, filmcraft, soundcraft) are the storytold project's static web
release artifacts. Homestead **vendors those release artifacts**; it does
not re-implement, port, or fork any of them. The storytold apps describe
themselves as clean-room re-implementations of familiar tools. That is a
statement about their provenance and is theirs to make: Homestead does not
clean-room anything here, and ADR-019 (clean-room reuse of Tanuki UI
patterns) does not govern these packages. Slice text and package metadata
therefore name the apps by function ("word processor", "spreadsheet")
without calling Homestead's packages clean-room.

Each vendored release is pinned by version and by SHA-256 of the exact
upstream artifact. A **licence compatibility check** of every storytold
release is a hard acceptance criterion of the slice that adds it: the
licence text is read, recorded in `docs/THIRD_PARTY.md` with its version
and the artifact digest, and the package is not added if the licence does
not permit redistribution inside a Homestead-built image under the
customer-ownership principles (the owner can rebuild, inspect, and keep
running without Homestead). The upstream source location is an
owner-supplied input: **storytold release source: TBD by owner**. No craft
package slice may start until the location is recorded in
`docs/THIRD_PARTY.md`.

### Local build only, from signed craft manifests
There is **no image registry** and no HAI-published image. The appliance
builds each craft image locally from two inputs: the verified upstream
storytold release artifact and the Homestead Dockerfile and build context
(`images/craft/`, versioned in this repository and shipped in the catalog
directory of the platform release). The Dockerfile is `nginx`-class,
static, with no server component, and its base image is **pinned by
digest**. Builds are reproducible in intent: the same artifact, build
context and base digest give the same layers, and an owner can rebuild and
compare. The catalog references the resulting image by a local tag
(`homestead/craft-<app>:<version>`); no registry reference is used.

### Trust: a separate craft signing key, ADR-025 semantics
Craft trust reuses the ADR-025 mechanics (minisign non-prehashed Ed25519,
`version` and monotonic `counter`, counter baseline in SQLite, audited
`--allow-older` override) but under a **separate HAI image/craft signing
key**, a distinct key class. It is trusted **alongside, not instead of**,
the platform release key: neither key verifies the other's artifacts. The
HAI craft public key is embedded in the binary; the owner can add or
replace craft trust keys through configuration over the API and CLI, as
with S264 trust keys. An owner who builds from their own artifact signs
their own manifest with their own key and adds it.

The signed object is a **craft release manifest** listing, for each craft
app: the upstream release artifact URL(s) with SHA-256 digests, the
version, the monotonic counter, and the digest of the Dockerfile and build
context. The manifest ships beside the catalog in the platform release (or
is supplied locally by the owner).

**Verification happens on the appliance, in the daemon, before any build**,
at plan time and again at apply time (the daemon never trusts an earlier
verdict):

1. The craft manifest signature verifies against a trusted craft key, and
   its counter is not lower than the recorded baseline (ADR-025 rollback
   rule, audited override unchanged).
2. The artifact is obtained and its SHA-256 equals the signed digest. The
   artifact is fetched from the signed URL, or the owner supplies the file
   locally (offline and air-gapped use) and it is verified identically.
3. The local build context's digest equals the signed build-context digest.
4. Only then does the daemon run the local build. The built image is tagged
   and **recorded by its local image ID/digest**; the daemon refuses to
   start the stack unless the local image matches the recorded digest.

Signature, artifact digest and build-context digest failures block build,
install and update with no override; only the counter rollback check is
overridable. Drift detection (S212) and installed checks compare the
running container's image against the **recorded local digest**, not a
published one. An update rebuilds from a newly verified manifest.

### Stateless packages are not backup targets
A package whose manifest declares no persistent data (every `data`
declaration `ephemeral`, or none) is **stateless**. Stateless packages:

- cannot be given a backup schedule: `POST /api/v1/backups/schedules`
  refuses with 422 and a message that names the package as stateless;
- never appear as scheduler jobs, so there is no `ErrUnavailable` retry
  loop (ADR-026) and no skipped-run audit noise for them;
- report `stateless` in backup status and scheduler surfaces as an
  explicit no-op, in the API, CLI and dashboard alike;
- are skipped, with a recorded note, by a pre-update backup request; the
  update proceeds without a backup step rather than failing;
- are handled explicitly by the backup guard
  (`internal/server/backupguard.go`) and `backupJob`
  (`internal/server/updates.go`): zero persistent sources is a defined
  stateless result, not "no backup sources" (which stays unavailable for
  stateful packages whose directories have not appeared).

### Declared writable paths and exposure defaults
`nginx`-class images need a few writable paths at runtime (cache, pid,
temporary files). Each is declared as an **ephemeral tmpfs** data path with
a reason, using the existing S276 `ephemeral` declaration. No read-only
root filesystem field is added to the catalog schema for this; that is a
possible later hardening (recorded in `docs/DEFERRED.md`). Craft apps keep
no state on the appliance: documents live in the browser until ADR-029
wiring lands.

Exposure defaults to **local-only with `allowwider: false`** for every
craft package until S254 lands. Without a server-side document home, a
widened exposure would offer a public editor that stores nothing for the
owner and invites confusion; the owner may still override recommendations
through the normal ADR-006 plan, which is why the control is a package
default and not a hard refusal. S254 revisits the default.

### Test shape
Craft packages share one table-driven test (built in S249) over their
per-app YAML manifests: local craft tag, listed in the craft manifest, own host, port, ephemeral-only
data, stateless, local-only exposure, image rows in `knownImageVolumes`.
Per-app manifests stay separate YAML files (one app, one file, one host).

## Consequences
- Craft package slices add YAML, a Dockerfile, and rows in tests; they
  add no runtime code of their own beyond what S249 and S279 build.
- The release process gains craft manifest signing tooling and the
  appliance gains a verify-then-build operation (S279); no registry is
  operated. A lost HAI craft key has the same remedy as ADR-025 (owners
  replace the trusted key by configuration).
- The appliance needs Docker build capability, disk space for build
  layers, and (unless the owner supplies the artifact) access to the
  upstream artifact URL at build time; failures leave nothing running.
- Craft catalog entries use a local tag; the recorded local digest, not a
  catalog digest, is the integrity reference for drift checks.
- Every vendored release needs a recorded licence check, a
  `docs/THIRD_PARTY.md` entry, and a pinned artifact digest before merge.
- Backup, scheduler, and restore surfaces gain a stateless state; a
  stateful package's behaviour is unchanged.
- A package that later gains persistent data stops being stateless
  automatically, because the rule derives from the manifest, and becomes
  schedulable.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

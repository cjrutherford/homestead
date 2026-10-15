# Craft Suite Packaging and Image Trust

## Status
Accepted

Owner decisions 2026-10-14. Amends ADR-018 (Static Container Packages) and
ADR-023 (Daemon Scheduler); extends ADR-025 (Release Artifact Trust Root)
to container images.

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

### HAI-built, signed images
Each craft package ships one HAI-built image: a Homestead Dockerfile
(`nginx`-class, static, no server component) over the pinned upstream
release artifact. The base image is pinned by digest, the artifact is
verified against its recorded SHA-256 during the build, and the build is
reproducible from the Dockerfile plus the artifact so an owner can rebuild
and compare. The published image is referenced in `catalog/*.yaml` by tag
**and digest** (`name:tag@sha256:...`); a tag alone is not accepted for
craft packages.

### Trust: ADR-025 extended to images
Image trust reuses the ADR-025 chain with no new trust root and no new
dependency. The release process publishes a signed **image manifest**: a
checksums-style file listing, for each craft image, its reference, digest,
and the vendored artifact digest, carrying the same `version` and monotonic
`counter` fields as an ADR-025 checksums file, signed with minisign
non-prehashed Ed25519. The HAI public key embedded in the binary and the
owner's trusted keys configured over the API and CLI (ADR-025 "Owner
trust") verify it. An owner who rebuilds images signs their own manifest
with their own key and adds that key; nothing is hard-wired to HAI.

**Verification happens on the appliance, in the daemon, before install and
before update**, as part of the plan and again at apply time (the daemon
never trusts a plan's earlier verdict):

1. The signed image manifest verifies against a trusted key, and its
   counter is not lower than the recorded baseline (the ADR-025 rollback
   rule and audited `--allow-older` override apply unchanged).
2. The digest in the catalog entry equals the signed digest for that
   reference. A mismatch refuses the install or update.
3. Compose pulls by the pinned digest, and after the pull the daemon
   inspects the local image and refuses to start the stack unless its
   `RepoDigests` contain the pinned digest. Docker's content addressing
   makes the digest the integrity check; the signature is the authenticity
   check for the digest itself.

Signature or digest failure blocks the install or update with no override
flag; only the counter rollback check is overridable, as in ADR-025.
Release acquisition is still not fetched by Homestead: the signed image
manifest ships beside the catalog in the platform release (or is supplied
locally by the owner), and the image layers come from the registry the
digest names, exactly as Compose already does for every package. Drift
detection (S212) reports a running craft container whose image digest
differs from the pin.

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
per-app YAML manifests: pinned digest, own host, port, ephemeral-only
data, stateless, local-only exposure, image rows in `knownImageVolumes`.
Per-app manifests stay separate YAML files (one app, one file, one host).

## Consequences
- Craft package slices add YAML, a Dockerfile, and rows in tests; they
  add no runtime code of their own beyond what S249 and S279 build.
- The release process gains an image build, publish and signing pipeline
  (S279); a lost HAI signing key has the same remedy as ADR-025 (owners
  replace the trusted key by configuration).
- The catalog schema must accept `image:tag@sha256:digest` and craft
  validation must require the digest.
- Every vendored release needs a recorded licence check, a
  `docs/THIRD_PARTY.md` entry, and a pinned artifact digest before merge.
- Backup, scheduler, and restore surfaces gain a stateless state; a
  stateful package's behaviour is unchanged.
- A package that later gains persistent data stops being stateless
  automatically, because the rule derives from the manifest, and becomes
  schedulable.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

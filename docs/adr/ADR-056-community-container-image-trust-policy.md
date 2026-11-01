# Community container image trust policy

## Status
Accepted

Owner approval 2026-11-01. Written by S360 (F4 of `docs/CATALOG_EXPANSION.md`).
`docs/SLICE_PLAN.md` first called this "ADR-035"; that number is the dashboard
ADR, so this record is ADR-056. Extends ADR-028 (which settles trust for the
locally built craft images) to every image the appliance pulls from a registry.
Builds on ADR-016 (bootstrap trust ladder) and ADR-025 (release artifact trust
root), and on S343 (GHCR and Docker Hub tag discovery).

## Context
Every catalog package so far runs an image its own project publishes, except
Calibre-Web Automated, which runs a single maintainer's image. Wave 3 adds the
*arr family (Prowlarr, Sonarr, Radarr), SABnzbd and BookStack, which have no
upstream-published image and are normally run from LinuxServer.io. The
appliance pulls and runs these images as root-adjacent containers with owner
data mounted in, so "who publishes it" is a trust decision that must be
written down, reviewable and checked by a test.

Facts from the repository:
- `Service.Validate` (`internal/catalog/schema.go`) already refuses a missing
  or `latest` tag. It does not look at digests; `update.ParseImage` drops them
  because discovery compares tags (S343). Docker Hub and GHCR are the two
  discoverable registries; every other registry is refused by name.
- Current non-craft images (all classified below) come from Docker Hub or GHCR.
  The craft images are built on the appliance and covered by ADR-028.
- The manifest loader uses strict YAML (unknown fields are errors), so a new
  field needs a schema change; the change here is two optional strings.

## Decision

### 1. Publisher classes
Every pulled service declares `publisher:` in its manifest:

| Class | Meaning | Acceptance |
|-------|---------|------------|
| `upstream` | The project that makes the app publishes the image (or, for a dependency such as Postgres or Valkey, the project or Docker official image). | Default and preferred. Always tried first. |
| `linuxserver` | LinuxServer.io (`lscr.io/linuxserver/...`, `ghcr.io/linuxserver/...` or `docker.io/linuxserver/...`). | Only for packages named here: Prowlarr, Sonarr, Radarr, SABnzbd, BookStack. Adding another package needs an ADR amendment. |
| `community` | A person or small group that is not the upstream project and not LinuxServer.io. | Case by case, with `justification:` recorded in the manifest. |

An app with an upstream image never uses a community or LinuxServer image.
Craft images (`homestead/craft-<app>:<version>`) leave `publisher` empty: they
are built on the appliance and signed (ADR-028).

### 2. Requirements for every pulled image
A slice adds a pulled image only if all of these hold, checked when the slice
is written and recorded in the slice file ("Image trust" section):
1. **Multi-arch**: the registry manifest list contains linux/amd64 and
   linux/arm64.
2. **Pinned version tag**: a specific version, never `latest` (enforced by
   `Service.Validate`), no floating major-only tag for an app image. Floating
   tags remain acceptable only where an existing package already uses one for a
   dependency (for example `valkey:9`), and S360 does not widen that.
3. **Digest**: the slice records the manifest-list digest it verified. The
   manifest does not pin it, because the schema pins a tag and update discovery
   (S343) compares tags; adding `@sha256:` pins would break drift comparison
   and the update plan. Pinning digests in the manifest is deferred (see
   `docs/DEFERRED.md`).
4. **Public source**: a public source repository and the Dockerfile (or build
   definition) that produces the tag, found and linked in the slice file.
5. **Active maintenance**: a release or commit within the last 12 months and an
   issue tracker that is read. A project whose last release is older is not
   added.
6. **Update source declared**: the registry is Docker Hub or GHCR (the two
   S343 can query) and the tag scheme works with the update comparison
   (`update.NewerTags`): numeric version prefix, optional variant suffix.
7. **Licence** recorded, as for any package (ADR-028 §Vendoring).

### 3. Community images
A `community` image additionally needs a `justification:` in the manifest (at
least a sentence: who maintains it, why no upstream or LinuxServer image
serves, where the source and Dockerfile are). A community image is
re-evaluated at every version bump slice; a lapse in maintenance (requirement
5) is grounds to retire the package. Community images are never made a
dependency of another package.

### 4. LinuxServer.io specifics
- Image: `lscr.io/linuxserver/<app>:<version-tag>` preferred; the same image on
  GHCR or Docker Hub is equivalent. lscr.io redirects to GHCR; discovery
  queries the GHCR repository (`ghcr.io/linuxserver/<app>`).
- Tags: LinuxServer publishes `<app-version>-ls<N>` build tags (and
  `version-<app-version>`). The pin is the `version-<app-version>` tag where it
  exists (stable across rebuilds, numeric prefix, matches `update.NewerTags`);
  otherwise the `<version>-ls<N>` tag, accepting that the `-ls<N>` suffix
  differs between rebuilds so discovery treats a new `-ls<N>` as a new variant.
  The slice records which scheme it used.
- Process model: s6-overlay init as root, a service user `abc` created from
  `PUID`/`PGID`, and persistent state under `/config`. Manifests set `PUID`
  and `PGID` to the app storage owner the appliance uses, declare `/config`
  (and any media/download paths) as data, and do not override the entrypoint
  (the secret-entrypoint supervisor of ADR-052 is not used with these images;
  secrets use the file-based `FILE__VAR` convention LinuxServer documents, to be
  confirmed per package in its slice).
- LinuxServer images are rebuilt weekly on base-image updates. A pinned tag is
  still immutable in intent; the digest recorded in the slice makes a silent
  rebuild visible when the package is next reviewed.

### 5. Provenance
Verify what is feasible, record what was checked, do not claim more:
- At slice time, inspect the registry manifest list (platforms, digest) and
  the image labels (`org.opencontainers.image.source`, `.revision`, `.version`)
  and compare them with the source repository release.
- If the publisher publishes signatures or build attestations (cosign, GitHub
  build provenance), the slice verifies them and records the result. If not,
  the slice states "no signature or attestation published" as a fact.
- The appliance does not verify image signatures at runtime. Runtime
  verification is deferred (ADR-025's trust root covers Homestead artifacts,
  not third-party images). Trust rests on the owner's curated catalog being
  reviewed against this policy, not on a cryptographic claim about the
  publisher.

### 6. Enforcement
Light and test-enforced:
- `Service.Publisher` (`publisher:`) and `Service.Justification`
  (`justification:`) are optional manifest strings. `Service.Validate` refuses
  an unknown publisher, a `community` image without a justification of at
  least 40 characters, and a justification on a non-community image.
- `TestCuratedImageTrust` fails the build when a pulled service in `catalog/`
  has no publisher, when a `linuxserver/` image is declared as anything else,
  when a `linuxserver` image appears outside the five named packages, when a
  community image lacks a justification, or when a craft image declares one.
- The existing tag check refuses `latest`. Multi-arch, digest, source,
  maintenance and provenance are human-verified and recorded in the slice
  file; no code can attest them offline.

### 7. What a slice records
In the slice file's "Image trust" section: image reference and publisher
class, platforms seen, manifest-list digest and date checked, source repository
and Dockerfile link, last release date, licence, tag scheme and update source,
signature/attestation result, and for community images the justification text
that is also in the manifest.

### 8. Classification of the current catalog (S360)
- `upstream`: actual-budget, audiobookshelf, authentik (server, worker, and its
  Docker official Postgres), freshrss, gitea, immich (server, machine-learning,
  the immich-app Postgres, Docker official Valkey), jellyfin, komga, linkding,
  mealie, memos, navidrome, nextcloud, paperless-ngx (with its Postgres and
  Valkey dependencies), uptime-kuma, vaultwarden, vikunja.
- `community`: calibre-web-automated (`docker.io/crocodilestick/...`), a
  single-maintainer project with a public repository and Dockerfile, built on
  the LinuxServer base image, amd64 and arm64. No upstream-published image
  exists for the app. Justification recorded in the manifest.
- `linuxserver`: none yet. Prowlarr, Sonarr, Radarr, SABnzbd and BookStack are
  the allowed first users (S361-S366).
- Craft apps: locally built, ADR-028.

## Consequences
- Wave 3 packages (S361-S366) can start: each follows this policy and carries
  its own "Image trust" section.
- The manifest schema grows two optional fields; the catalog loader, API and
  dashboard payloads carry them unchanged and nothing at install time reads
  them. A future owner-facing display of the publisher is possible but not
  built.
- Some checks (multi-arch, maintenance, signatures) remain human review.
  Registry-side automation and runtime signature verification are deferred.
- Digest pinning is not done; a rebuilt tag is detected only at review time.
- Adding a LinuxServer package outside the named five, or a second community
  image without justification, fails the test; widening the list needs an ADR
  amendment.

## Alternatives considered
- **Upstream images only.** Rejected: would exclude the *arr family, SABnzbd
  and BookStack, which the owner accepted (CATALOG_EXPANSION §1.1).
- **Pin digests in the manifest.** Rejected for now: breaks tag-based update
  discovery and the update plan; no S343 path reads digests.
- **Allow any community image with a free-text note.** Rejected: LinuxServer
  gets a named class because of its scale, tag scheme and maintenance record;
  everything else needs per-image justification and re-review.
- **Verify signatures at install.** Rejected for v1: LinuxServer and most
  upstreams publish none, so the check would be vacuous for most of the catalog.

# S343 — GHCR Update Discovery

## Goal
Discover image updates for packages published on GitHub Container
Registry, so the GHCR-based apps the catalog ships (Immich, Paperless,
Mealie, Audiobookshelf, Seerr) show updates instead of reading as
current forever.

Foundation item F3 in `docs/CATALOG_EXPANSION.md`.

## Dependencies
S190 (Docker Hub tag discovery), S191 (update planning), S263. All
complete.

## Scope

### Registry classification (`internal/update`)
- **`ParseImage`** splits a reference into registry name, repository,
  and tag. A first path segment with a dot, a colon, or `localhost` is a
  registry host; bare names and `owner/name` live on Docker Hub; `v`
  prefixes stay part of the tag. Digests are dropped, because discovery
  compares tags.
- **Docker Hub** keeps the anonymous path S190 introduced. `docker.io`
  and `registry-1.docker.io` normalize to it.
- **GHCR** joins through the token flow GHCR requires for every request:
  one anonymous pull token for `repository:<repo>:pull`, then the tag
  listing with that bearer token, at most two pages, using the `Link`
  continuation header.
- **Every other registry** (LinuxServer's `lscr.io`, `quay.io`, private
  hosts) refuses by name. Discovery never queries the wrong endpoint and
  never treats silence as "current".

### Discovery result and provenance
- **`RegistryClient`** holds the HTTP client and both registry bases, so
  tests inject doubles and production uses the zero value.
- **`Discovery`** carries the service, registry, repository, raw tags,
  a local-build marker, and the failure when a registry gave nothing.
- **`RegistryClient.Discover`** never aborts on one failure: a sulking
  registry leaves that service without listings while the rest of the
  plan proceeds.
- **`UpdatePlan.Provenance`** records, per service, which registry
  answered, whether it did, and why not. Locally built craft images are
  marked as such. A gap explains a service with no registry updates
  rather than letting silence read as current (S343's "provenance
  recorded per package").
- **`PlanUpdateFromDiscoveries`** feeds full results into the planner;
  `PlanUpdate` stays as the tag-only entry point for existing callers.

### Version comparison
- `versionPrefix` now tolerates a leading `v`: GHCR projects tag releases
  `v1.119.0`, and the old pattern silently ignored every one of them.
  Suffix families still never compare across variants.

### Surfaces
- **CLI**: `homesteadctl updates list` prints one line per service whose
  registry did not answer, naming the registry and the reason.
- **Dashboard**: the updates page shows the same gap line from the plan's
  provenance. No new route or command, so `docs/PARITY.md` is unchanged.

## Non-goals
- Private registries and authenticated pulls. The catalog ships public
  images; a refusal says the image may be private rather than inventing
  credentials Homestead does not hold.
- Digest-pinned updates and signature verification of upstream images
  (F4 covers the trust policy for community images).
- Registry rate-limit backoff or caching; a limited listing errors and
  the pin covers that service.
- Update *application* changes: S192 already applies plans, and GHCR
  images flow through it unchanged.

## Acceptance Criteria
- Docker Hub images keep using the Hub listing path; `ghcr.io` images use
  the token flow with the bearer header and follow one continuation page.
- An unsupported registry refuses with the host named, and the plan
  records it as an undiscovered service rather than an update.
- A failing registry never becomes an update target; the manifest pin
  still governs.
- `v`-prefixed tags compare numerically and still trigger major-version
  warnings.
- Plans carry per-service provenance, sorted by service, and both the CLI
  and the dashboard show gaps.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — update discovery section.
- `docs/DEFERRED.md` — the GHCR gap closes; private registries stay
  deferred.
- `docs/SLICE_INDEX.md`, `docs/SLICE_PLAN.md` — schedule moved on.

## Handoff
Wave 1's GHCR packages (Paperless, Mealie) and Wave 2's Audiobookshelf
are no longer gated on registry discovery. The next slice is S344.

# Static Container Packages

## Status
Accepted

## Decision
Catalog packages may ship browser applications as static files inside
pinned first-party container images: one service running a pinned
`nginx`-class image that serves vendored release content on its own
internal hostname through the existing edge routes. Each craft-class
package pins its image tag like any other package, serves DNS-only
static content with correct wasm MIME and cache headers, keeps no
server state, and claims no volumes or databases. Image contents are
reproducible from the upstream release artifact plus a Homestead
Dockerfile, so owners can rebuild and verify without trusting opaque
binaries.

## Context
The storytold craft suite (word/grid/pdf/deck/vector/photo/film/sound
and kin) ships static wasm web builds with no server component. Docker
Compose remains the only runtime (ADR-005), so static content rides
inside containers rather than beside them: docker-first, one container
per app, one internal host per app.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

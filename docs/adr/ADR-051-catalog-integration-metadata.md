# Catalog Integration Metadata Is Advisory

## Status
Accepted

## Context
Packages increasingly work together: an archive app indexes a document
store, a media server reads a shared library, a proxy fronts another
app. Homestead can describe those relationships so owners and the
dashboard can see them, but every one of them ends in another
application's internals — indexers, library paths, API keys, users.

ADR-014 keeps the catalog first-party and curated; it does not promise
that Homestead configures the applications it installs.

## Decision
Integration metadata in a manifest is **advisory**. It records what a
package can work with, at which level, and what the owner must do. It is
never actuation.

Four levels, from least to most Homestead involvement:

1. **L1 — coexistence**: packages run side by side with no declared
   relationship.
2. **L2 — shared network**: services reach each other over a Homestead
   network; no configuration is written into either app.
3. **L3 — shared storage**: the owner supplies a mounted path that two
   packages read; Homestead mounts it and never rewrites another
   package's volumes.
4. **L4 — application configuration**: the owner configures one app to
   talk to another (indexers, libraries, API keys). Homestead may show
   the values the owner needs; it does not write them.

Rules:

- A declaration never grants Homestead permission to configure another
  application's internals, and no code path may treat L4 as automated.
- Cross-package load gates are advisory too: they warn about capacity or
  ordering and never block an install the owner confirms.
- Whatever a declaration promises stays inspectable in `catalog show`
  and in the dashboard's "Works with" panel, in plain language.

## Consequences
- Owners keep full control of application internals, and a failed
  integration is never Homestead's hidden state.
- Homestead can add richer relationships without growing an ever-larger
  configuration matrix it must keep working across upstream versions.
- Automating an L4 relationship requires a new ADR, not a new field.

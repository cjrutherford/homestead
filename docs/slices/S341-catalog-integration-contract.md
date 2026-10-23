# S341 — Catalog Integration Contract

## Goal
Declare, in the catalog itself, how each package relates to the rest of the
appliance, so that "installed" does not have to be read as "integrated".

The expansion plan (`docs/CATALOG_EXPANSION.md`) promises four integration
levels: co-install (L1), shared identity (L2), shared library (L3) and
workflow handoff (L4). Today none of them is machine-readable. The catalog
knows an app's services, storage, secrets and SSO posture, but not that
Paperless reads the same scans directory as Nextcloud, that Seerr logs in
through Jellyfin, or that an OIDC-capable app expects Authentik to be the
provider. The dashboard cannot answer "what works with this?", and a bundle
(`F5`) cannot explain why a package is in a profile.

This slice makes the relationships data. It adds no runtime wiring: nothing
here creates an OIDC client, mounts a shared path, or configures an app's
internals. Actuation stays with F1 and with the owner inside the app.

## Dependencies
S150,S152,S153,S154,S161,S202,S203,S204,S276

## Scope
Implement only the functionality required for this slice. Preserve all
accepted ADRs and customer-ownership guarantees. Do not pull deferred
features into scope without an ADR and slice update.

### The declaration

`catalog.Manifest` gains one optional list:

```go
// Integrations declares how this package relates to the rest of the
// appliance (S341). Advisory metadata: nothing in the install, update or
// start path reads it, and it never causes Homestead to configure
// another app.
Integrations []Integration
```

```go
type Integration struct {
    Kind   IntegrationKind // identity | library | handoff | companion
    Target string          // catalog package name, or a platform component
    Note   string          // 1-200 chars, owner-facing, written for Homestead
}
```

`Level` is **derived from `Kind`**, never authored, because a hand-written
level invites two packages to disagree about what the same relationship is:

| Kind | Level | Meaning |
|------|-------|---------|
| `identity` | L2 | `Target` is the identity provider for this app's SSO |
| `library` | L3 | `Target` and this app mount the same host directory |
| `handoff` | L4 | One app calls the other at runtime, owner-configured |
| `companion` | L1 | Useful together, no shared identity or data |

Known platform components (`authentik`, `jellyfin`, `nextcloud`, `immich`)
are catalog packages too, so a target resolves against the shipped catalog
and needs no special-casing. A target that names no shipped package is a
load failure, not a warning, because a metadata field that can lie is
worse than no field.

### Hard gates the loader enforces

- Unknown `Kind`, duplicate `(Kind, Target)`, self-reference, empty or
  over-long `Note`.
- `identity` requires the package's effective SSO class to be something
  other than `unsupported`. An app that cannot do SSO must not claim an
  identity provider.
- `identity` requires `Target` to name the registered identity provider,
  resolved from the identity provider registry (`internal/identity`) rather
  than hard-coded to `authentik`. Pointing an app at a non-provider is a
  load failure.
- An `identity` declaration for a package whose SSO class is `optional`,
  `recommended` or `required` is **required**, not optional. This is the
  gate that keeps future packages from shipping SSO posture with no stated
  provider.
- `library` requires both packages to declare at least one `bulk` data
  path (S276). Two apps cannot share a library neither of them places on
  bulk storage.
- `handoff` requires the target package to be installed for the declaring
  app to be useful; the plan renderer surfaces that as a soft note, and
  install does not refuse (L4 is owner-configured, so a missing companion
  is a degraded experience, not a broken install).

### Surfaces

- `GET /api/v1/catalog/{name}` returns the declarations with the derived
  level and the target's title, so no surface has to re-resolve names.
- CLI `catalog show <package>` prints `integrates:` lines, one per
  declaration: level, kind, target title, note.
- The dashboard package page gains a "Works with" panel grouped by level,
  each entry linking to the target package page. Absent integrations
  render nothing; the panel is not a placeholder.
- The install plan (`POST /api/v1/catalog/{name}/plan`) gains a single
  advisory line listing declared handoff targets that are not installed.
  It is advisory only and changes no verdict.

### Shipped declarations

The core apps declare their real relationships so the gates are exercised
against live manifests. All three SSO-capable packages already declare
`optional` with protocols, so the provider gate applies to each of them:

- `nextcloud` — identity `authentik`; library `jellyfin` (shared bulk
  media directory).
- `jellyfin` — identity `authentik`; library `nextcloud`.
- `immich` — identity `authentik`.
- `authentik` — no declaration: it is the provider, and its own SSO class
  is `unsupported`.

`companion` and `handoff` have no truthful pair in the shipped catalog
yet, so their gates are covered by fixture manifests rather than by
production data. Craft packages stay undeclared: their SSO class is
`unsupported` and they declare no bulk path, so no gate applies.

## Acceptance Criteria
- Catalog integration contract implemented and documented
- `Integration` and `IntegrationKind` exist in `internal/catalog/schema.go`
  with the validation rules above; `Manifest.Validate` rejects every case
  in the gate list
- Cross-package validation runs at catalog load: dangling targets,
  `identity` targets that are not the identity provider, and `library`
  pairs without bulk paths fail the load with the package and target named
- The core manifests above declare their integrations (`nextcloud`,
  `jellyfin`, `immich`; `authentik` declares none) and the shipped catalog
  still loads
- `GET /api/v1/catalog/{name}` includes resolved integrations (kind,
  level, target name, target title, note); a package without declarations
  returns an empty list, not `null`
- CLI `catalog show` prints the `integrates:` block and the install plan
  prints the advisory handoff line; the dashboard package page renders the
  "Works with" panel with links to target package pages
- Parity holds: `docs/PARITY.md` records the extended catalog detail
  response and the package-page panel; the parity test passes
- Editing a manifest's `integrations:` changes both surfaces with no code
  change — a test asserts this by loading a fixture manifest
- Automated tests cover primary behavior
- Main build remains green

## Out of scope
- Creating OIDC clients or writing app credentials (F1)
- Mounting a shared path automatically, or rewriting another package's
  volumes (L3 stays an owner-supplied volume at install)
- Configuring any app's internals: indexers, libraries, users, API keys
- Bundles and capacity profiles (F5); this slice only supplies the data
  F5 will read
- Health checks, `depends_on` and start ordering
- Any new catalog package

## Required Documentation
- New `docs/CATALOG_INTEGRATION.md`: the four levels, the declaration
  format, what Homestead promises and what stays owner-configured.
- New `docs/adr/ADR-051-catalog-integration-metadata.md` (Accepted):
  integration metadata is advisory, not actuation — the catalog may
  describe a relationship but must never configure another app's internals.
- Update `docs/PARITY.md` and the catalog schema section of
  `docs/ARCHITECTURE.md`.
- Record newly discovered deferred work in `docs/DEFERRED.md`.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to
begin without hidden manual state. F1 (OIDC client actuation) is the next
slice and consumes the `identity` declarations this slice introduces.

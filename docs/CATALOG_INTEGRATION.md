# Catalog Integration Metadata (S341; ADR-051)

A package manifest may declare how the app relates to the rest of the
appliance. The declarations are **advisory**: they tell the owner what can
work together and what they must do, and nothing in install, update, start
or backup reads them. Homestead never configures another application's
internals from a declaration.

## Declaration format

```yaml
integrations:
- kind: identity
  target: authentik
  note: Sign in through Authentik; local accounts stay available for recovery.
- kind: library
  target: jellyfin
  note: Mount the same bulk media directory into Nextcloud external storage and Jellyfin.
```

- `kind` is one of `identity`, `library`, `handoff`, `companion`.
- `target` is a catalog package name. A target the catalog does not ship
  fails the load: a metadata field that can lie is worse than no field.
- `note` is 1-200 characters, written for an owner: what to expect, and
  what stays theirs to configure.

## Levels

The level is **derived from the kind**, never authored, so two packages
cannot disagree about what the same relationship means.

| Kind | Level | Meaning | What Homestead does |
|------|-------|---------|---------------------|
| `companion` | L1 coexistence | Useful together, no shared identity or data | Nothing beyond installing both |
| `identity` | L2 shared identity | The target is the app's identity provider | Shows the relationship; S342 actuates OIDC clients when configured |
| `library` | L3 shared storage | Both packages keep data on bulk storage | Provisions each package's own paths; the owner mounts the shared directory |
| `handoff` | L4 application config | One app calls the other at runtime | Shows the values the owner needs; prints an advisory plan note when the target is not installed |

## Load gates

The catalog refuses to load when:

- a `kind` is unknown, a `(kind, target)` pair repeats, the target is the
  package itself, or the note is empty or over 200 characters;
- a target names a package the catalog does not ship;
- an `identity` target is not a provider this build drives
  (`internal/identity.Builtins`), or the declaring package's SSO class is
  `unsupported`;
- a package advertises SSO (any class but `unsupported`) without naming an
  `identity` provider: an SSO posture with no stated provider is an
  unactionable promise;
- a `library` package does not place anything on bulk storage (S276), on
  either side of the pair.

These gates run at catalog load, not at install: a curated typo fails the
build instead of surfacing as an empty panel months later.

## Surfaces

- `GET /api/v1/catalog/{name}` returns the manifest plus `Works`: each
  declaration with its derived level and the target's title. A package
  without declarations returns `[]`, never `null`.
- `homesteadctl apps show <package>` prints `integrates:` lines.
- The dashboard package page shows a "Works with" panel with links to the
  target package; absent declarations render nothing.
- The install plan gains one advisory note per declared `handoff` target
  that is not installed. It changes no verdict.

## What stays owner-configured

- Sign-in setup inside each app, even when an OIDC client exists. The
  owner pastes the client credentials; Homestead does not edit app state
  beyond the declared client actuation (S342).
- Shared directory contents and permissions.
- Indexers, libraries, API keys, and every other L4 handoff. Automating one
  of those requires a new ADR, not a new field.

# S344 — Catalog Bundles and Capacity

## Goal
Let an owner answer "does this set of apps fit my appliance?" before
installing twenty packages one at a time: four curated profiles, each
graded for combined memory and summed storage.

Foundation item F5 in `docs/CATALOG_EXPANSION.md`.

## Dependencies
S152 (curated catalog), S154, S172 (recommended suite and joint grading),
S341 (integration contract). All complete.

## Scope

### Bundle model (`internal/catalog`)
- **`Bundle`** is a curated profile: a stable name, a title, a blurb, and
  an ordered package list. Membership is a decision, never a directory
  listing.
- **`Bundles`** ships four profiles, smallest commitment first:
  - **starter** — Nextcloud, Jellyfin, Vaultwarden, Linkding, FreshRSS,
    Uptime Kuma.
  - **family** — the starter set plus Immich, Paperless, Mealie, Vikunja,
    Actual Budget, Audiobookshelf, Navidrome, Gitea and Memos.
  - **media** — the starter set plus the *arr family, SABnzbd, Seerr,
    Komga and Calibre-Web Automated.
  - **full** — the family and media sets plus BookStack.
- Every profile includes the starter set, so an owner grows a homestead
  without starting over.
- **Membership is intent, not availability.** Most curated members are
  scheduled in later slices. A member the catalog does not carry is
  reported as missing and named; it is never silently dropped, and it
  never fails capacity grading on its own.
- **`Bundle.Validate`** refuses a missing name or title, a name that is
  not a single word, an empty list, blank members, and duplicates.

### Grading (`CheckBundle`)
- **Combined memory**: the sum of available members' memory floors must
  fit the host. No memory data for the host fails the check — missing
  data never reads as satisfied (same rule as `Evaluate`).
- **Summed storage**: each class's floors are summed and compared with
  that class's free space. An unassigned class fails with "no space
  data" rather than passing on a guess.
- **Ports are not graded.** Requirement ports are container ports on
  separate networks and routes never publish them, so two members
  listening on 80 is normal; host-port and exposure checks stay
  install-time concerns.
- **Per-package verdicts, host ports and exposure stay install-time
  concerns.** A bundle preview answers "does this set fit", not "is every
  package installable right now" — an installed member's own bound port
  must not make the bundle look impossible.
- An incomplete bundle still grades what exists today; `Complete` is
  informational.

### Surfaces
- **API**: `GET /api/v1/catalog/bundles` grades every profile against
  live host facts. Host-discovery failure returns 502 rather than
  inventing capacity.
- **CLI**: `homesteadctl apps bundles` prints each profile's verdict,
  per-class storage, and the members still missing from the catalog.
- **Dashboard**: the catalog page lists the profiles above the package
  list with the same verdict, storage lines, and missing members.
- `docs/PARITY.md` gains the route row (CLI `bundles`, dashboard
  `catalogBundles`).

## Non-goals
- One-click "install this bundle": bundles are capacity previews. An
  owner still reviews and installs each package, so exposure, storage
  mounts and credentials get the attention they need.
- Bundle-level exposure, backup or update policy. Member manifests own
  those.
- Automatic bundle membership from integration metadata (S341), or
  filtering bundles by installed packages.
- Reservation of ports or disk across future installs.

## Acceptance Criteria
- Four curated profiles validate, are unique, and all include the starter
  set.
- Grading sums memory and per-class storage over available members, names
  missing members, and fails on missing host data.
- Two members that listen on the same container port still fit: ports are
  per-network and unpublished.
- A bundle with no available members is blocked, not "fits with 0
  packages".
- The API, CLI and dashboard show the same verdict, storage lines and
  missing members.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — bundles section next to the S172 suite record.
- `docs/PARITY.md` — the new route row.
- `docs/DEFERRED.md` — F5 leaves the scheduled list.
- `docs/SLICE_INDEX.md`, `docs/SLICE_PLAN.md` — schedule moved on.

## Handoff
Every foundation item Wave 1 needs is now in place. The next slice is
S345 `vaultwarden`, the first Wave 1 package; its arrival turns the
starter profile from "2 of 6 available" into real capacity numbers.

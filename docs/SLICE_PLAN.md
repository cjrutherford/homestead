# Remaining Slice Plan

Status: proposed schedule (2026-10-23). Scope is every remaining slice from
`docs/CATALOG_EXPANSION.md` and the Everything repackaging assessment in
`docs/reports/catalog-expansion.html`. Slice IDs are assigned here so the
two tracks cannot collide; each slice's contract file is written when it is
scheduled, not now.

Numbering continues from S341 (catalog integration contract). The plan ends
at **S381** — 41 slices. Work with no ID is listed in section 7 and is not
committed to.

## 1. Rules that apply to every slice below

- One slice, one commit on main. A slice that turns out to need two is two
  slices.
- Read `docs/PROJECT.md`, `docs/ARCHITECTURE.md`, the accepted ADRs and the
  slice file before coding.
- Tests plus docs, and `docs/DEFERRED.md` for anything discovered but not
  done.
- `docs/PARITY.md` updated whenever a route, CLI command or dashboard
  surface changes. The parity test enforces it.
- Every catalog package declares its `integrations:` (S341). A package that
  cannot name its identity provider or shared library fails the load.
- No package ships with a `latest` tag, an undeclared writable path, a
  privileged container, host networking, or a secret in an environment
  variable.

## 2. Track A — Docker catalog

### 2.1 Foundations

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S341 | Catalog integration contract | S150,S152,S153,S154,S161,S202-S204,S276 | `integrations:` manifest metadata, derived L1-L4 levels, cross-package load gates, `apps show` and dashboard "Works with". ADR-051. **Done.** |
| S342 | OIDC client actuation and secret delivery (F1) | S341,S202,S204,S266 | Create Authentik OIDC clients from the catalog declaration; write the client secret through the secret store; supervised entrypoint that exports 0600 files into the app environment at start. ADR-052 amends ADR-021. Gates all of Wave 1. **Done.** |
| S343 | GHCR update discovery (F3) | S190,S263 | Token-based GHCR tag discovery beside the existing Docker Hub path; provenance recorded per package. **Done.** |
| S344 | Catalog bundles and capacity (F5) | S152,S154,S172,S341 | Starter/Family/Media/Full bundles with combined RAM and storage checks, shown in the plan and the dashboard. **Done.** |

### 2.2 Wave 1 — everyday suite

Each is one manifest, one test set, one slice. Gated on S342; `freshrss`
and `mealie` also wait on S343.

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S345 | `vaultwarden` | S342 | Owner signup, admin page off by default; local/overlay exposure. **Done.** |
| S346 | `paperless-ngx` | S342,S343,S276 | Postgres + Redis; shared scans volume with Nextcloud; OCR languages. **Done.** |
| S347 | `gitea` | S342 | `ROOT_URL` from the route host; HTTPS-only, no SSH in v1. **Done.** |
| S348 | `freshrss` | S342,S343 | Debian image so OIDC exists. **Done.** |
| S349 | `linkding` | S342 | Simplest package in the wave. **Done.** |
| S350 | `mealie` | S342,S343 | GHCR image. **Done.** |
| S351 | `uptime-kuma` | S342 | Documented local-auth exception; never a public status page. **Done.** |

### 2.3 Second foundation

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S352 | MariaDB/MySQL backup export (F2) | S180,S266 | `mariadb`/`mysql` server kinds, consistent dumps with each engine's client, documented restore, password secrets. Required before BookStack. **Done.** |

### 2.4 Wave 2 — family library

No wave-level gate. `audiobookshelf` needs S343; `calibre-web-automated`
needs the S341 library declaration to name its shared ingest path.

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S353 | `audiobookshelf` | S343 | Bulk audiobook library shared by volume. **Done.** |
| S354 | `navidrome` | S276 | Local-auth exception; same music mount as Jellyfin. **Done.** |
| S355 | `komga` | S341 | JVM heap cap in the manifest so it cannot crowd the appliance. **Done.** |
| S356 | `calibre-web-automated` | S341 | Community image; OIDC re-verified at slice time or documented as local-only. **Done.** |
| S357 | `vikunja` | S341 | CalDAV against Nextcloud. |
| S358 | `actual-budget` | S276 | OIDC verified at slice time, else local-only exception. |
| S359 | `memos` | S341 | Complements Nextcloud Notes; does not replace it. |

### 2.5 Third foundation, then Wave 3

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S360 | Community-image trust policy (F4) | S279 | Written policy plus ADR-035: trusted publishers, multi-arch requirement, pinned tags, provenance, update source. Gates every LinuxServer.io package. |

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S361 | `prowlarr` | S360 | Indexer manager; local only. |
| S362 | `sonarr` | S360,S341 | TV; library paths must match Jellyfin exactly. |
| S363 | `radarr` | S360,S341 | Movies; same shape as Sonarr. |
| S364 | `sabnzbd` | S360 | Usenet client; owner supplies the provider. |
| S365 | `seerr` | S341 | Jellyfin login; LAN only. |
| S366 | `bookstack` | S352,S360 | MariaDB export, OIDC, LinuxServer image. Last slice of the catalog track. |

## 3. Track B — Everything apps

Track B is independent of Track A until an app slice declares catalog
integration metadata (S341) and reuses the craft static-image shape
(S249/S279). It does not need F1-F5.

### 3.1 Bridge and build pipeline

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S367 | Puter bridge and app build pipeline | S279,S341 | `vendor/everything/pin.yaml`; per-app patch that swaps the CDN script and adds a fail-closed CSP; `bridge/` implementing `auth`, `env`, `fs`, `kv`, `ui`, `perms`, `exit`; multi-stage image that builds the pinned commit and serves it; upstream test suite run against the bridge; ADR-036 records the model (pin + patch + bridge, no fork, no vendored SDK). |

S367 is the riskiest slice in the plan. If the bridge cannot pass
upstream's own tests for `tasks`, Track B stops at S367 and nothing after
it is scheduled. Treat S367 as a spike with a hard exit criterion.

### 3.2 Apps that ship clean

Order is by bridge surface exercised, not by appeal: `tasks` is the pilot
because it needs one file write.

| ID | App | Also needs | Why here |
|----|-----|-----------|----------|
| S368 | `tasks` | — | Pilot. 1 fs, 3 auth. Proves the pipeline end to end. |
| S369 | `projects` | — | 24 fs, 4 ui, 2 kv. First real `fs` consumer. |
| S370 | `records` | — | Tables and forms; the Airtable gap. |
| S371 | `notes` | — | AGPL. AI assist disabled behind a clear message. |
| S372 | `contacts` | — | `kv` + `ui` heavy. |
| S373 | `inventory` | — | Household inventory. |
| S374 | `time-tracking` | — | `fs` + `kv`. |
| S375 | `circuits` | — | One `fs` call; hobbyist gap. |
| S376 | `parametric` | — | One `fs`, one `ai` (disabled). |
| S377 | `3d` | — | IndexedDB + File System Access API; Chromium-only caveat in the copy. |
| S378 | `solitaire` | — | Cheapest possible package; proves the build for a 245 KB app. |

### 3.3 Cheap extras

| ID | App | Depends on | Note |
|----|-----|-----------|------|
| S379 | `sudoku` | S367 | Same shape as solitaire. |
| S380 | `expense` | S367 | Local-only; do not ship the "AI OCR" claim. |
| S381 | `api-client` | S367 | Needs the `puter.net` bridge extension — the only surface addition after S367. |

## 4. Critical path and recommended order

The longest chain is:

`S341 → S342 → S344 → S345…S351 → S352 → S353…S359 → S360 → S361…S366`

That is 25 slices and cannot be shortened much: Wave 1 is gated on S342 by
owner decision, and Wave 3 is gated on S360 and S352.

Track B is the only place to win time. Recommended interleave, so the
first owner-visible win lands early and the riskiest slice is tested before
the catalog is half-built:

1. S341 — catalog integration contract (done)
2. S342 — OIDC actuation and secret delivery
3. S367 — bridge and build pipeline (**spike exit check**)
4. S368 — `tasks`
5. S343 — GHCR discovery
6. S344 — bundles and capacity
7. S345 → S351 — Wave 1, one per slice
8. S369 → S372 — four Everything apps
9. S352 — MariaDB export
10. S353 → S359 — Wave 2
11. S373 → S378 — remaining clean Everything apps
12. S360 → S366 — Wave 3
13. S379 → S381 — extras

Rationale: S367 sits third because it is the only slice that can invalidate
a whole track. Finding that out after twenty catalog packages would be an
expensive discovery.

## 5. Definition of done — app slices

Every package slice (S345-S351, S353-S366, S368-S381) closes only when:

- the manifest pins a non-`latest` tag, both architectures, and declares
  every writable path on mounted storage or as ephemeral;
- requirements declare memory, CPU, storage floors and host ports;
- the package declares `integrations:` — identity provider or documented
  exception, shared library where one exists;
- secrets arrive through the S266/S342 path, never as plaintext env;
- backup covers the database export and file paths, with the restore
  story written down;
- exposure defaults to local/overlay, with any widening documented;
- CLI plan and dashboard agree (parity);
- tests cover manifest validation, plan rendering and install;
- `docs/DEFERRED.md` records anything found but not done;
- the slice doc exists in `docs/slices/` and is referenced here.

## 6. ADR ledger

| ADR | Slice | Decision |
|-----|-------|----------|
| ADR-051 | S341 | Catalog integration metadata is advisory, not actuation. |
| ADR-052 | S342 | Secret delivery may export a 0600 file into the app environment at start, under supervision. Amends ADR-021. |
| ADR-035 | S360 | Community image trust: accepted publishers, multi-arch, pinned tags, provenance. |
| ADR-036 | S367 | Everything apps are vendored at a pinned commit with a Homestead bridge; no fork, no vendored SDK. |

## 7. Unscheduled — no ID

Recorded so nothing is silently lost. None of these blocks a scheduled
slice.

**Owner-operator apps** (bridge-clean, wrong default suite): `accounting`,
`cap-table`, `crm`, `pos`, `AssetDesk`. Schedule on request, one slice
each, after S381.

**Blocked on a capability we are not building:** `calendar`, `email`,
`meetings`, `teamchat`, `wiki`, `signatures`, `help-desk`, `field-service`,
`fleet`, `legal`, `childcare`, `property-management`, `shifts`, `hiring`,
`invoicing`, `contracts`, `whiteboard`. `survey` becomes shippable if a
worker shim ever exists.

**Craft-suite overlap:** `documents`, `spreadsheet`, `presentation`, `pdf`,
`vector-studio`, `photo-studio`, `music-studio`, `audio-studio`,
`video-studio`, `cad`, `building-design`.

**Catalog candidates already deferred:** `qbittorrent` (VPN network-mode
design), `collabora`/`onlyoffice` (Nextcloud app management), `immich-kiosk`
(companion), `ntfy` (no notification pipeline to feed it).

## 8. What could renumber this plan

- **S367 fails its exit check.** Track B stops; S368-S381 are released.
- **A package fails its hard gates at slice time.** The ID is not reused;
  the slice is withdrawn and the next package moves up.
- **The Compose generator gains a field** (healthcheck, `depends_on`).
  That is a slice of its own and would insert ahead of S361 if any Wave 3
  app needs a readiness gate.
- **F1 cannot deliver supervised export.** Then Wave 1 reverts to
  owner-supplied credentials and S342's scope changes, not its ID.

# S373 - Inventory as an Everything catalog package

## Goal and dependencies
Sixth Everything app (ADR-057): `inventory` (household inventory: products, SKUs
and barcodes, stock by location, suppliers and reorder lists, stocktakes, lots and
expiry, reports, photos, CSV import and export) from HeyPuter/everything at the
pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Plan row: "Household
inventory." Depends on S368 to S372.

## Source and licence (MIT)
`third_party/everything/inventory-source.tar.gz` (203 entries, 6,365,897 bytes,
SHA-256 `cd9c23d16cb5844db332719ee498644559e5b9a8cf46bd60a9e008d53c3a345b`),
built with the same recipe as the other archives (reproduced against the records
digest first). The repository root `LICENSE` is MIT ("Copyright (c) 2026 Puter
Technologies Inc."). `src/inventory` has no licence file of its own, its
`package.json` has no licence field, and its README says the code is under the
repository's MIT licence. Not AGPL. One runtime dependency is bundled: `lucide`
1.28.0 (ISC). Recorded in `docs/THIRD_PARTY.md` and `third_party/everything/pin.yaml`.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `kv.get/set/del` | `lib/cloud.js` | supported |
| `kv.list(prefix*)` (keys) | `lib/cloud.js` | supported (1000-key limit unchanged; the app lists by prefix only without values) |
| `fs.write` (with `overwrite`, `createMissingParents`), `fs.read`, `fs.delete` | `lib/photos.js` (product photos under `inventory/images/`) | supported, already in the bridge; person-scoped, counted against quota; the app downscales to 800 px WebP first, so files are tens of KB |
| `auth.isSignedIn/getUser` | `main.js`, `lib/store.js` | supported; `uuid` is used only to tell people apart in the browser mirror; a patch keeps the opaque id off screen |
| `auth.signIn/signOut` | `views/auth.js`, `views/settings.js` | unreachable (`env` is `app`: no sign-in screen, no Sign out) |
| `env` | `ui/shell.js`, `views/settings.js` | supported (`app`) |
| `ui.*`, `ai`, `net`, `perms`, `events`, sharing, `fs.getReadURL`, `fs.readdir/stat` | not used | not used (refused or absent in the bridge anyway) |
| `indexedDB` (`inv-offline`), `localStorage`, `navigator.mediaDevices`, `navigator.clipboard`, `URL.createObjectURL`, `createImageBitmap`, service worker | offline mirror, theme, barcode scanner, copy, photos | browser-native; the CSP allows `blob:` images and same-origin workers |

No bridge or shim change was needed. ADR-057's table already covers every call.

## Analytics and third-party scripts
Upstream loads Plausible (`plausible.io`) in `index.html` and again in every page of
its generated handbook, and loads the hosted SDK from `js.puter.com` (in
`index.html`, in the handbook pages and in its own service worker). All are gone:
the Plausible block is a pinned patch, the SDK tag is replaced by the local shim by
`prepare_app.py`, the service worker is Homestead's, and the handbook site is not
built. `images/everything/Dockerfile` already failed the build on `js.puter.com` in
`dist`; it now fails on `plausible.io` too (one `grep -rIlE`). Final check of the
built output: no `js.puter.com`, no `plausible`, and the headless run shows only
same-origin requests.

## Patches (12, each with a `why` in `scripts/everything/apps/inventory.json`)
1. `index.html` - Remove the Plausible analytics script and its stub: no third-party tracking, and the CSP would refuse the script anyway.
2. `site/content.js` - Do not build the public inventory.zone origin into canonical, Open Graph and sitemap metadata; this copy is not that site (upstream's own empty override).
3. `site/plugin.js` - Do not emit the 50-odd static handbook and guide pages: they carry Plausible and the hosted SDK on every page, link to the root of the host, and are marketing for the public site.
4. `vite.config.js` - Do not pre-render the public landing page (promotional links to other sites and to /docs/ and /guides/) into index.html; the app builds the signed-out view itself, and behind Homestead sign-in is always present.
5. `src/views/settings.js` - Remove the Documentation and Guides links: they point at the host root (/docs/, /guides/), where no handbook is served.
6. `src/views/settings.js` - Say Homestead, not Puter.
7. `src/views/settings.js` - Remove the outbound link to developer.puter.com and the claim that data is in a Puter account.
8. `src/lib/store.js` - The bridge's user name is the opaque sign-in subject; keep it out of the sidebar and settings.
9. `src/ui/shell.js` - Say Homestead, not Puter.
10. `src/ui/onboarding.js` - Say Homestead, not Puter.
11. `src/ui/productform.js` - Say Homestead, not Puter.
12. `src/main.js` - Say Homestead, not Puter.

A runtime-mode service worker is installed from `inventory-sw.template.js` (as for
Contacts: network-only navigations, a fixed offline page, only content-hashed
assets cached).

## Tests
- Upstream, pristine tree with `npm ci --ignore-scripts`: 722/722 (34 files).
- `tests/test_s373_inventory_package.py` (archive, licence, SDK calls, patches
  apply, worker, Dockerfile guard, catalog posture, bookkeeping), plus catalog
  rows (`everything_test.go`, `data_test.go`, `sso_test.go`, `curated_test.go`) and
  `appsso` sign-in routes for inventory.
- Headless Chrome 156 (`scripts/everything/gate/run_browser_check_inventory.sh`):
  CSP-clean boot; worker scope; same-origin requests only; no hosted SDK or
  analytics text; sidebar shows no raw subject and no Sign out; no Puter wording on
  the onboarding and product form; a product with a photo created through the form
  (record in KV, photo as a small file); reload keeps both and renders the
  thumbnail; offline page without data; second person sees no products, no photos
  (and cannot read the first person's photo path) and the browser mirror of the
  first person is wiped.
- Gate bridge mode and the Go and Python suites: see the run in the report.
- Image built once on the appliance recipe: 15.6 MB (15,591,447 bytes), ID
  `5ab568929698`; removed.

## Unproven
Not run on VM 2 or through Authentik (owner checklist in
`docs/reviews/EVERYTHING_REVIEW_PLAN.md`). Camera barcode scanning, CSV import
of a large file, stocktakes, reports and the printing screens were not driven in a
browser (headless Chrome has no camera; the rest rely on the upstream tests).
Upstream tests were not run against the real bridge. See `docs/DEFERRED.md`.

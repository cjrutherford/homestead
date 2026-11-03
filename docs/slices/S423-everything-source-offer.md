# S423 - In-app source offer for Everything apps with copyleft code

## Goal and dependencies
Close the AGPL section 13 and LGPL source-offer gaps recorded in DEFERRED for S371 (Notes),
S377 (3D) and S376 (Parametric). Owner decision 2026-11-02: "we can link to the agpl or lgpl
source directly on the page we serve." Depends on S368 to S377. ADR-057 amended in place.

## Design
- Declaration: `source_offer` in `scripts/everything/apps/<app>.json` (licence, licence URL,
  upstream repository and pinned commit, `tarball` true/false, bundled `components` with
  name, licence, version, source URL, optional licence file served by the app, optional
  `link_offset` for where the corner link sits). Only `notes`, `three-d` and `parametric`
  declare one; `tests/test_s423_source_offer.py` enforces that and that the commit, licence
  and component version match `pin.yaml` and the app's lockfile.
- Build: `scripts/everything/make_source_offer.py` runs in the image's prepare stage and
  writes `offer.json` and, for AGPL apps, `<app>-source.tar.gz` to `/srv/source`. The pinned
  archive was already in the build context (`artifact/`), so nothing new is fetched or
  added to the context. The tarball holds the archive verbatim, the app definition (patch
  list), added files, `puter-bridge.js`, `prepare_app.py`, the service-worker template, the
  Dockerfile and a README; sorted names, root owner, mtime 0, gzip without a timestamp, so it
  is reproducible. Running the README command from the extracted tarball gives the same
  prepared tree as the build (tested).
- Bridge (`internal/everything/source.go`): `GET <base>/_homestead/source` (HTML, CSP
  `default-src 'none'; style-src 'unsafe-inline'`, nosniff, no-referrer, DENY framing) and
  `GET <base>/_homestead/source/<app>-source.tar.gz` (`application/gzip`, attachment,
  Range supported). A missing or malformed `offer.json` (https links only, 40-hex commit,
  tarball name, size and digest shape, unknown fields refused) means no offer: 404 and no
  link. `HOMESTEAD_EVERYTHING_SOURCE_DIR` (default `/srv/source`) names the directory.
- Link: `index.html` gets `<a id="homestead-source" ... style="position:fixed;...">Source</a>`
  after `<body>`, with an accessible name naming the licence. No script, so the hashed
  inline-script policy is untouched.
- Sign-in: required (page redirects to sign-in, tarball 401). The offer is owed to the people
  who use the app; they are signed in, and the catalog is closed by default.

## Sizes
Notes tarball 1,441,086 bytes; 3D tarball 11,593,160 bytes (it carries the 11.6 MB pinned
archive, which is already compressed); Parametric has no tarball (it offers upstream links
and the licence text the app already serves). The image carries one extra copy of the
archive for the two AGPL apps.

## Tests
Go: page content, no script, headers, tarball bytes and headers, sign-in needed, no offer
means 404 and no link, link injected after `<body>` without changing the policy, offset,
component-only (LGPL) offer, malformed offers ignored. Python: declarations, reproducibility,
rebuild from the tarball, Dockerfile and context copy, DEFERRED closed.
Headless Chrome (`scripts/everything/gate/run_browser_check_source_offer.sh`): link visible,
on top, bottom edge, covers no control, page CSP-clean, source page script-free, tarball
downloaded by clicking the link and matching its checksum (Notes, 3D); Parametric licence
text served and no tarball.

## Out of scope
See DEFERRED (S423): VM 2 and Authentik not run; the Go bridge and npm dependencies are not
in the tarball; no LGPL relinking kit; one viewport only.

## Image evidence
`scripts/everything/build-image.sh notes` built a 19.4 MB image; its `/srv/source/notes-source.tar.gz`
has the same SHA-256 (`3e30311f...6a69`) as the one built on the host, so the tarball is reproducible
across the Python 3.13 alpine build stage and the developer machine. The image was removed afterwards.

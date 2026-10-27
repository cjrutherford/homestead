# S320 — Craft apps under one host, by path

## Goal
From the owner's VM 2 acceptance run (2026-10-27):
- Only Nextcloud, Jellyfin and Authentik follow `<server>/apps/<name>`.
- Publishing a craft app through the tunnel failed with "route.tunnel:
  craft app routes are not delivered by the tunnel yet".

The owner asked for the craft apps to follow the same convention and to be
deliverable through the tunnel.

## Decision
ADR-045 (amends ADR-044 and ADR-032).

## Scope
- **Catalog.** The thirteen craft packages are `serve: path` with
  `strip_prefix: true`, at `/apps/<name>/`. The catalog accepts
  `serve: path` for a craft package only with `strip_prefix: true`. All
  thirteen release artifacts were checked: they load assets by relative
  paths and register no service worker.
- **Edge.** A craft path route renders a nested handle. `<path>_homestead/*`
  goes to the craft socket with these headers set by `header_up`:
  - the app;
  - the request's host;
  - the app's prefix (`X-Homestead-Craft-Prefix`);
  - `X-Forwarded-Proto https`.

  Everything else goes to the craft container. The tunnel entry gets the
  same handle once the app is published. Route validation accepts a craft
  path route that strips its path. A craft route on a name of its own still
  takes no tunnel names; the error now says to move it first.
- **Daemon.**
  - The craft listener serves a prefix-relative copy of the wrapper page
    (`internal/craftfiles/wrapper`, embedded) at `/_homestead/open.html`,
    `open.js` and `open.css`, with the image's CSP.
  - The wrapper may be opened directly; everything else stays
    same-origin.
  - The prefix header is validated.
  - The craft cookie's path is `<prefix>/_homestead/files/`.
  - The launch URL and its binding use the name the dashboard was opened
    on, when it is one of the route's names.
  - The start page links path apps at `https://<server>/apps/<name>/`.
  - Three new craft-socket routes are classified `craft-session`.
- **Signed image.** It is unchanged. Its wrapper copy is unused on path
  routes, and a test pins the daemon's copy to the image's copy apart from
  the paths.
- **Existing installs.** Craft apps installed on names of their own move
  with the ADR-044 move offer; there is no silent move.

## Out of scope
- Rebuilding or re-signing the craft images.
- Item 4 (craft apps get the person's own Nextcloud files by default) is a
  later slice.

## Tests
- craftfiles:
  - the wrapper is served with its CSP;
  - direct navigation is allowed for the wrapper only;
  - prefix validation;
  - the cookie path under the prefix;
  - the path launch URL and its binding;
  - the wrapper drift guard against the image copy.
- docker:
  - the craft path handle on the local site;
  - the craft path handle on the tunnel entry, only once published;
  - a craft host route with tunnel names is refused.
- server:
  - the start page URL of a path app;
  - craft route classification (14 routes).

## Live check (VM 2)
- A throwaway Caddy ran the rendered `/apps/cadcraft/` handle against the
  real cadcraft container and the real craft socket:
  - app 200, wasm and JS assets 200, bare path 308;
  - the daemon's wrapper 200 with its CSP;
  - session without a cookie 401;
  - other `_homestead` paths 404;
  - unknown app 404.
- Moving the installed CADCraft and DeckCraft is left to the owner (see
  `docs/DEFERRED.md`).

## Live check follow-up (2026-10-27)
On VM 2, cadcraft and deckcraft were installed before S320, so they still
answered at names of their own. Their Public address section asked for a
name: the dashboard's public name was refused as "already a name of the
edge route homestead.lan", and `<name>/apps/cadcraft` as "invalid hostname
label". Now:
- A craft app still on a name of its own has no name field. Its Public
  address section says to move it to `/apps/<name>/` first (Review the
  move, or `homesteadctl apps move <name>`), then publish it.
- A public name with a path or scheme, and the dashboard's own public
  name, are refused in plain words. For a path package, the refusal adds
  how to publish under the dashboard's name.
- After a move, the Public address section reads the app again and
  offers publishing at its path.

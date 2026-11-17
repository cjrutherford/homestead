# S465 - Read links are minted on the approved public name

## Goal and dependencies
EV-ADR058-6 (Everything acceptance run). Depends on S431 (read links) and S433 (public read-link exposure). After `homesteadctl apps read-links approve notes`, a link made in the app (from the `.lan` name) still began `https://homestead.lan/...`, because `fs.getReadURL` used the request's `Origin`. The app is not published, so the owner had no way to get a public link from the UI.

## What changed
- `internal/everything/readroute.go`: `handleReadURL` mints on the approved public origin (`HOMESTEAD_EVERYTHING_PUBLIC_READ_ORIGIN`, from the stored exposure plan) whenever one is set, whatever name the caller is on. With no approval the link stays on the caller's own origin (LAN or overlay name), unchanged. Removing the approval puts links back on the local origin.
- The public origin was already validated as a bare https public name (S433); no new route, command or surface, so `docs/PARITY.md` is unchanged. The checklist step EV-ADR058-6 already said links start with the public name.
- Links minted before the approval keep their old origin; the token is the same on either name only while the file is reachable there (only `/read/*` and `/view/*` are open on the public name).

## Tests
`internal/everything/readlinks_test.go` (`TestGetReadURLUsesTheApprovedPublicOrigin`), `internal/everything/publicread_test.go` (local caller now gets the public link), `tests/test_s465_read_link_public_origin.py`.

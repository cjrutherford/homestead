# S482 - Live acceptance of the subpath work (S469-S481, absorbs S478)

## Goal and dependencies
Prove on VM 2 (2026-11-08, head 759a289, no code change) what ADR-061 and ADR-062 promise: apps at `/apps/<app>/` load and sign in through the public name, the removed subdomains are gone, own-name apps still work, cookies are isolated, no move is pending and Doctor is clean. Depends on S481 and absorbs S478.

## What was checked (real browser, public name)
- Navidrome and the 13 craft apps open at `https://homestead.<domain>/apps/<app>/` for the owner; Alex Reed (granted Navidrome, FigCraft, GridCraft) opens those three and is refused (403) on the other 11; Casey Lowe (no apps) is refused on all 14.
- Komga, Paperless-ngx and Linkding are `publish: path` but not published on VM 2, so they were checked at `https://homestead.lan/apps/<app>/`: Alex signs in with Authentik on each; Casey is denied on Linkding.
- Cloudflare read-only: the tunnel `homestead-vm2` ingress and the DNS zone carry only `homestead`, `actual-budget`, `calibre-web-automated`, `freshrss`, `gitea`, `mealie`, `memos`. No name of a moved app remains and none resolves.
- Own-name apps: Gitea, Mealie, Memos, Actual and Calibre-Web-Automated load on the public name; Immich, Audiobookshelf, Vaultwarden, BookStack and Vikunja on their `.lan` names (unpublished). FreshRSS failed for the owner (see Findings).
- Cookies: `__Host-homestead_session` and `__Host-homestead_csrf` are set (Secure, Strict; session HttpOnly); Navidrome's cookie is scoped to `/apps/navidrome`; the edge strips the reserved cookies from requests and `Set-Cookie` on every app path; the dashboard sends `frame-ancestors 'none'`.
- `apps move status`: every path app reports it is already at its path; nothing pending.
- `doctor --live`: 0 fails; `public-name-plan` ok.
- Apps page: owner and Alex see the new subpath URLs for published path apps.

## Findings
- FAIL (medium, deferred): FreshRSS sign-in as the owner returns 500 on `/i/oidc/`. mod_auth_openidc cannot store the session (17759 bytes over `OIDCCacheShmEntrySizeMax` 16400) because the owner's Authentik token carries every group. Alex (few groups) signs in. See `docs/DEFERRED.md` (S482).

## Checklist
Steps `END-SUBPATH-1` to `END-SUBPATH-8` in `docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md`; the form is regenerated. Results: 7 PASS, 1 FAIL.

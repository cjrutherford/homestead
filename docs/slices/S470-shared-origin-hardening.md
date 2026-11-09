# S470 - Shared-origin hardening at the public edge

## Goal and dependencies
Second slice of ADR-061 (section 4). Depends on S469. Apps at `/apps/<app>/` share an origin with the dashboard and Authentik, so cookies with `Path=/` reach every app; this slice keeps the appliance's own cookies away from apps and apps away from them.

## What changed
- Dashboard session and CSRF cookies are `__Host-homestead_session` and `__Host-homestead_csrf` over TLS (Secure, `Path=/`, no Domain: a browser refuses to store them otherwise, so a sibling name or plain-HTTP page cannot overwrite them). A plain-HTTP request cannot hold a Secure cookie, so it keeps `homestead_session` and `homestead_csrf`. The daemon reads only the names that fit the request's scheme (`internal/server/auth.go`: `cookieNames`, `sessionCookie`); an unprefixed cookie over TLS is ignored, which is the point. Sign-out expires all four names. The dashboard script reads the `__Host-` CSRF cookie, then the plain one.
- **Re-login needed once:** the ADR does not ask to keep old sessions across the rename, and reading the unprefixed name over TLS would undo the protection. Existing browser sessions end at the first request after the deploy; the CLI uses a bearer token and is unaffected.
- Edge (`internal/docker/routes.go`, every app path handle on the dashboard site, the tunnel entry and the read-link handle): `request_header Cookie` removes `__Host-homestead_*`, `homestead_session`, `homestead_csrf`, `authentik_session` and `authentik_csrf` from requests to the app (inside the handle, so after any forward-auth subrequest); `header_down Set-Cookie` blanks a reserved name from the app's response (an empty Set-Cookie is ignored by browsers). Authentik at `/apps/auth/` keeps its own cookies.
- `Content-Security-Policy: frame-ancestors 'self'` and `Referrer-Policy: same-origin` on those handles, as defaults (`header ?`): a package's own stricter value (S464 Everything CSP) stays.
- Dashboard responses (`dashboardFront`) carry `Content-Security-Policy: frame-ancestors 'none'` and `X-Frame-Options: DENY`, so an app page on the same origin cannot frame the dashboard either (`'self'` on app paths would otherwise allow it).

## Not covered
The preview warning for an app known to set `Path=/` cookies is deferred (DEFERRED.md).

## Tests
`internal/docker` `TestSharedOriginHardening`, `TestReadLinkHandleStripsReservedCookies`; `internal/server` `TestHostPrefixedCookiesOverTLS`, `TestLogoutClearsBothCookieNamesOverTLS`; dashboard `readCsrf`; `tests/test_s470_shared_origin_hardening.py`.

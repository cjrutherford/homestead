# S497 - Paperless-ngx public path sign-in (CSRF)

## Goal and dependencies
Owner-approved publishing of Komga, Paperless-ngx and Linkding at `https://homestead.<zone>/apps/<app>/` on VM 2 found one defect. Depends on S475, S482 and ADR-061.

## Defect
On the public path, the Authentik button on Paperless-ngx's sign-in page answered 403 "CSRF verification failed". The Django log: `Origin checking failed - https://homestead.<zone> does not match any trusted origins`. The LAN path worked because `PAPERLESS_URL` (the route host, `homestead.lan` after the S475 move) is a trusted origin; the public host is not, and Django's same-origin fallback compares the Origin with `http://<host>` because it does not trust the edge's `X-Forwarded-Proto`.

## Fix
`PAPERLESS_PROXY_SSL_HEADER: '["HTTP_X_FORWARDED_PROTO", "https"]'` in `catalog/paperless-ngx.yaml`. Caddy sets the header on every proxied request and Paperless is reachable only through it. The same-origin rule then accepts any name the edge serves, so no per-name trusted origin list is needed.

## Not built
Existing installs and the public-plan warning (DEFERRED S497). The `.lan` redirect for ungated path apps (question for the owner, DEFERRED S497).

## Verification
`tests/test_s497_paperless_proxy_ssl_header.py`. Live on VM 2: Komga, Paperless-ngx and Linkding published through plan and approval; person A signs in through Authentik on each public path, Casey is denied, unauthenticated requests land on the app's own sign-in.

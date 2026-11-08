# S477 - FreshRSS at a subpath: checked, kept on its own name

## Goal and dependencies
ADR-061 section 3: prove FreshRSS can serve at `/apps/freshrss/` with its Authentik sign-in (Apache `mod_auth_openidc`, S412) before the package changes. Depends on S471, S479, S476. Outcome: it cannot, so the manifest keeps `publish: host`, `publish_reason: no_subpath`. Owner decision: moves go forward only; the old public name stays when the move fails. Nothing on VM 2 was changed.

## Evidence (pinned 1.30.4, throwaway container on VM 2)
- The app side has a base-path hook: `X-Forwarded-Prefix` (`lib/Minz/Request.php`, `Session.php`), used with the prefix stripped. The edge sends no such header and no manifest field makes it send one, so links would be root-relative.
- The sign-in layer is the blocker. `Docker/FreshRSS.Apache.conf` hard-codes `OIDCRedirectURI /i/oidc/` and `<Directory /var/www/FreshRSS/p/i>`; there is no environment variable for either. A container started with `OIDC_ENABLED=1` against the live Authentik issuer was asked for `/i/` with `X-Forwarded-Host`, `X-Forwarded-Proto` and, second, `X-Forwarded-Prefix: /apps/freshrss`. Both times Authentik's authorize redirect carried `redirect_uri=https://homestead.lan/i/oidc/`: mod_auth_openidc 2.4.17 ignores the prefix header (`OIDCXForwardedHeaders` knows only Forwarded, Host, Port and Proto).
- With the prefix stripped the module's redirect URI lacks `/apps/freshrss`, so it cannot be registered under the shared origin and the callback would land on the dashboard; keeping the prefix would not match `/i/oidc/` either. Fixing it needs a replaced Apache configuration file in the image, which Homestead does not mount or patch (curated, pinned images unchanged; no runtime rewriting).

## Changes
- `catalog/freshrss.yaml`: comment on `publish_reason` only; still `publish: host`, no `serve: path`.
- `TestFreshRSSStaysOnItsOwnName` in `internal/appsso/freshrss_test.go` pins host, reason and no `serve: path`; the publish fact-table fixture already lists host/no_subpath.
- Live: VM 2 untouched. `freshrss.<domain>` stays published, the owner's feeds and the S445 default user are intact.
- Checklist step SRV-FRESHRSS-2 and `docs/DEFERRED.md` record the finding (the slice ID is not reused).

## Tests
`internal/appsso/freshrss_test.go`, `internal/catalog/publish_test.go`, `tests/test_s477_freshrss_subpath_check.py`.

# Craft App Header Verification Table (S249, ADR-018, ADR-028)

One row per craft app, filled from the app's own release (its `HOSTING.md`,
`_headers`, file list) and then measured against the running container with
`scripts/craft-header-check.sh`. Never assume an app matches another's row:
S250 to S253 each add rows and verify them. The shared server rules live in
`images/craft/nginx.conf`; an app needing more than they give (for example
COOP/COEP for `SharedArrayBuffer`) adds an explicit, tested rule for that
app in its own slice.

Columns: wasm MIME, cache headers per asset class, precompression,
COOP/COEP/`SharedArrayBuffer` need, WebGPU/WebGL2/clipboard needs,
healthcheck, nginx user and port.

| App | Version | wasm MIME | Cache headers | Precompression | COOP/COEP/SAB | WebGPU / WebGL2 / clipboard | Healthcheck | nginx user:port |
|---|---|---|---|---|---|---|---|---|
| wordcraft | 0.7.0 | `application/wasm` (hashed and unhashed) | `index.html`, other unhashed files: `no-cache`. Hashed `.js`, `_bg.wasm`, `.ico` (`name-<hex16>[_bg].ext`): `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Build writes `.gz` beside `.html .js .mjs .css .wasm .svg .json .txt .ico`; `gzip_static on` serves them when the client accepts gzip, `gzip on` covers the rest. Measured directly from the container: 39.8 MB wasm served as 14.9 MB; through the Caddy edge it is NOT compressed (open finding below) | Not needed: upstream states WordCraft does not use `SharedArrayBuffer`; no COOP/COEP sent, and the check script fails if either appears | Needs a secure context for WebGPU and the clipboard (HTTPS from the internal CA, ADR-030); falls back to WebGL2 over plain HTTP or when WebGPU is absent (`?webgl` forces it); WebGL2 is the floor | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` uses it every 30 s | `nginx` (uid 101):8080 |

Notes for WordCraft v0.7.0 (measured 2026-10-17 on a locally built image
run with a read-only root, `/tmp` tmpfs, all capabilities dropped):

- Release files: `index.html`, `wordcraft-web-<hash>.js` (ES module, served
  as `application/javascript`, which browsers accept for module scripts),
  `wordcraft-web-<hash>_bg.wasm`, `wordcraft-<hash>.ico`, licences, docs and
  the unused sample `_headers` and `.htaccess`. No `parts.txt` and no
  `.part-*` files (PhotoCraft's reference shape has them; S253 adds that
  rule when it verifies PhotoCraft).
- `index.html` pulls the wasm and js through relative URLs with
  `integrity=` hashes of the uncompressed bytes; transport compression is
  transparent to them (decompressed bytes were compared with the original
  SHA-256).
- The hashed-asset rule is one regex in `nginx.conf`: a run of 8 or more
  hex digits set off by `.`, `-` or `_` before an optional `_bg` and the
  extension.
- Writable paths: only `/tmp` (pid file and request temp directories). A
  running container shows no `docker diff` entries with `/tmp` as tmpfs.
  Confirmed on the KVM appliance 2026-10-17 on the daemon-built image:
  no VOLUME, user `nginx`, healthy, `docker diff` empty.
- Verified on the KVM appliance 2026-10-17 through the real edge
  (`https://wordcraft.lan/`, Caddy 2.11.7, internal CA leaf, curl with the
  root and no `-k`, HTTP/2): `/healthz` 200 `no-store`; `index.html` 200
  `no-cache`, `X-Content-Type-Options: nosniff`; hashed `_bg.wasm`
  `application/wasm`, `public, max-age=31536000, immutable`, 39,785,016
  bytes (SHA-256 `c81a3550...` equals the wasm in the upstream zip); hashed
  `.js` `application/javascript`, immutable; no COOP/COEP, and the JS and
  wasm contain no `SharedArrayBuffer`, so none is needed.
- **Open finding (found on KVM, not fixed in this record):** behind Caddy the
  wasm is served **uncompressed** (39.8 MB, no `Content-Encoding`), so
  `scripts/craft-header-check.sh` fails its gzip check through the edge
  while passing against the container directly (14,947,550 bytes, gzip).
  Cause: Caddy's upstream request carries a `Via` header and nginx's
  `gzip_proxied` defaults to `off`, so neither `gzip_static` nor `gzip`
  answers proxied requests. A container started with `gzip_proxied any;`
  added after `gzip_vary on;` served the gzip file with a `Via: 1.1 Caddy`
  request. The fix is that one line in `images/craft/nginx.conf`, which
  changes the build-context digest, so the craft manifest must be regenerated
  and re-signed (a follow-up slice, see `docs/DEVELOPMENT.md`).

## Checking a row

```
scripts/craft-header-check.sh https://wordcraft.lan/ --cacert root.pem \
    [--resolve wordcraft.lan:443:APPLIANCE_IP] [--coop-coep]
```

It prints every response header it inspects and exits 1 on a failure:
`/healthz` 200, entry `no-cache` and `nosniff`, every `.wasm` referenced by
`index.html` as `application/wasm`, immutable and gzip, every `.js`
immutable with a JavaScript type, and COOP/COEP absent (or present with
`--coop-coep`). Use the internal CA root from the dashboard Trust page or
`homesteadctl tls root export` for `--cacert` (ADR-030).

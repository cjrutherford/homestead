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
| gridcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `gridcraft-web-<hex16>.js`, `_bg.wasm`, `.ico`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 18.9 MB served as 7.0 MB (7,045,260 bytes) (local build, KVM pending; local build tested directly and with `Accept-Encoding: gzip`) | Not needed: upstream `HOSTING.md` states GridCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` (local build, KVM pending) | `nginx` (uid 101):8080 |
| pdfcraft | 0.8.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `pdfcraft-web-<hex16>.js`, `_bg.wasm`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 38.1 MB served as 14.1 MB (14,064,403 bytes) (local build, KVM pending; local build tested directly and with `Accept-Encoding: gzip`) | Not needed: upstream `HOSTING.md` states PdfCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` (local build, KVM pending) | `nginx` (uid 101):8080 |
| deckcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `deckcraft-web-<hex16>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 25.5 MB served as 9.3 MB (9,345,219 bytes) (local build, KVM pending; local build tested directly and with `Accept-Encoding: gzip`) | Not needed: upstream `HOSTING.md` states DeckCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` (local build, KVM pending) | `nginx` (uid 101):8080 |

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

Notes for GridCraft v0.7.0, PdfCraft v0.8.0 and DeckCraft v0.7.0 (S250,
measured 2026-10-17 on locally built images from the real release
artifacts, run with a read-only root, `/tmp` tmpfs, all capabilities
dropped, header script against the container over plain HTTP; **local build,
KVM pending**: the HTTPS/edge rows and Caddy-proxied gzip are confirmed by
the KVM run):

- Layout: each zip unpacks into one `<app>-web-<version>/` directory with
  `index.html`, one hashed ES-module `.js`, one hashed `_bg.wasm`, licences,
  `README.md`, `HOSTING.md` and the unused sample `_headers`/`.htaccess`
  (dotfiles are removed by the build and answered 404; `_headers` is served
  unchanged, see docs/DEFERRED.md). No `parts.txt`, no `.part-*`, no worker
  scripts, no service worker, no extra asset classes. GridCraft ships a
  hashed `.ico`, DeckCraft a hashed `.png` favicon (both match the shared
  hashed-asset rule and were measured `image/x-icon` / `image/png`,
  immutable), PdfCraft ships no icon and carries its `NOTICE` in the zip.
  The shared image and nginx rules needed no change.
- `index.html` preloads the js and wasm with `integrity=` hashes of the
  uncompressed bytes, as WordCraft.
- Browser capabilities, from each `HOSTING.md` and a grep of the shipped
  JS: no `SharedArrayBuffer`, `crossOriginIsolated` or `Worker` anywhere in
  the JS; the PdfCraft wasm contains the string `SharedArrayBuffer` only
  inside an embedded JavaScript engine's built-in error messages (not a use
  of it). All three call `navigator.gpu.requestAdapter` and use the async
  clipboard API, so a secure context matters (ADR-030); COOP/COEP and
  `Cross-Origin-Resource-Policy` are not needed.
- Healthcheck and writable paths: all three reported `healthy` and
  `docker diff` of the running container was empty (tmpfs `/tmp` is the only
  writable path); `Config.Volumes` is absent and the user is `nginx`.
- Local image IDs (not the KVM digest): gridcraft `sha256:90b1b49768cb...`,
  pdfcraft `sha256:b2b57d93b072...`, deckcraft `sha256:4c8dbe82f805...`.

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

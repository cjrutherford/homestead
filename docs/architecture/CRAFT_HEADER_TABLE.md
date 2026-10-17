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
| wordcraft | 0.7.0 | `application/wasm` (hashed and unhashed) | `index.html`, other unhashed files: `no-cache`. Hashed `.js`, `_bg.wasm`, `.ico` (`name-<hex16>[_bg].ext`): `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Build writes `.gz` beside `.html .js .mjs .css .wasm .svg .json .txt .ico`; `gzip_static on` serves them when the client accepts gzip, `gzip on` covers the rest. Measured: 39.8 MB wasm served as 14.9 MB | Not needed: upstream states WordCraft does not use `SharedArrayBuffer`; no COOP/COEP sent, and the check script fails if either appears | Needs a secure context for WebGPU and the clipboard (HTTPS from the internal CA, ADR-030); falls back to WebGL2 over plain HTTP or when WebGPU is absent (`?webgl` forces it); WebGL2 is the floor | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` uses it every 30 s | `nginx` (uid 101):8080 |

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
  The KVM `docker diff` of the appliance-built image is recorded in
  `docs/DEVELOPMENT.md`.

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

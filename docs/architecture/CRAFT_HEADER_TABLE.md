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
| wordcraft | 0.7.0 | `application/wasm` (hashed and unhashed) | `index.html`, other unhashed files: `no-cache`. Hashed `.js`, `_bg.wasm`, `.ico` (`name-<hex16>[_bg].ext`): `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Build writes `.gz` beside `.html .js .mjs .css .wasm .svg .json .txt .ico`; `gzip_static on` serves them when the client accepts gzip, `gzip on` covers the rest. 39.8 MB wasm served as 14.9 MB (14,947,550 bytes), both directly and through the Caddy edge since the `gzip_proxied any` fix (KVM re-verification 2026-10-17) | Not needed: upstream states WordCraft does not use `SharedArrayBuffer`; no COOP/COEP sent, and the check script fails if either appears | Needs a secure context for WebGPU and the clipboard (HTTPS from the internal CA, ADR-030); falls back to WebGL2 over plain HTTP or when WebGPU is absent (`?webgl` forces it); WebGL2 is the floor | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` uses it every 30 s | `nginx` (uid 101):8080 |
| gridcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `gridcraft-web-<hex16>.js`, `_bg.wasm`, `.ico`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 18.9 MB served as 7.0 MB (7,045,260 bytes) (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states GridCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| pdfcraft | 0.8.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `pdfcraft-web-<hex16>.js`, `_bg.wasm`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 38.1 MB served as 14.1 MB (14,064,403 bytes) (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states PdfCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| deckcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `deckcraft-web-<hex16>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 25.5 MB served as 9.3 MB (9,345,219 bytes) (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states DeckCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); neither WebGPU nor WebGL2 gets an in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container reported `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| vectorcraft | 0.12.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `vectorcraft-web-<hex16>.js`, `_bg.wasm`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 64.7 MB served as 22.5 MB (22,507,398 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states VectorCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it, `?cpu` forces the CPU canvas); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); a browser with neither gets an upstream in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| photocraft | 0.9.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `photocraft-web-<hex16>.js`, `_bg.wasm`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 25.0 MB served as 11.4 MB (11,417,725 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states PhotoCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it, `?cpu` forces the CPU canvas); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); a browser with neither gets an upstream in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| lightcraft | 0.8.0 | `application/wasm` (unhashed name; the unhashed-wasm rule applies) | **Unhashed file names that stay the same between versions, so no `immutable`** (upstream `HOSTING.md` says so): `index.html`, `lightcraft_web.js`, `lightcraft_web_bg.wasm`, `worker.js`, `lightcraft_zh_hans.otf` all `no-cache`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff`. The shared rules already do this; no image change. Check with `craft-header-check.sh --unhashed` | Build writes `.gz` for html, js, wasm and worker (overwriting the artifact's own); the artifact's `lightcraft_zh_hans.otf.gz` is served by `gzip_static` (16.4 MB served as 13.6 MB, `Content-Type: application/octet-stream`, which `fetch()` does not care about); wasm 27.3 MB served as 10.5 MB (10,458,639 bytes), also with a `Via: 1.1 Caddy` request and through the Caddy edge. The artifact's `.br` copies are inert (nginx has no Brotli module) (KVM 2026-10-17) | Not needed: upstream `HOSTING.md` states the current build does not use `SharedArrayBuffer` (a future wasm-threads build will; re-check on every version bump); the JS has no `SharedArrayBuffer` or `crossOriginIsolated`; it does start module Web Workers (`worker.js`, same-origin, no isolation needed). No COOP/COEP sent and the check script fails if either appears | WebGL2 only (eframe `glow`; no WebGPU use); a browser without WebGL2 gets an upstream in-page message. Needs a secure context for the Origin Private File System library, module workers and the clipboard (HTTPS from the internal CA, ADR-030); the library lives in browser storage, which the browser may clear (upstream marks the web build experimental) | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| figcraft | 0.6.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `figcraft-web-<hex16>.js`, `_bg.wasm`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 22.5 MB served as 8.8 MB (8,802,810 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`; identity size in the notes below) | Not needed: upstream `HOSTING.md` states FigCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); a browser with neither gets an upstream in-page message | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` on the KVM appliance (2026-10-17) | `nginx` (uid 101):8080 |
| filmcraft | 0.8.0 | `application/wasm` (unhashed name; `?v=` query) | **Unhashed file names loaded with a `?v=<build hash>` query**: `index.html`, `filmcraft_web.js`, `filmcraft_web_bg.wasm`, `audio-worklet.js` all `no-cache` (upstream's `_headers` says `immutable` because of the query; the shared rules key on the name, so they revalidate with an ETag, which is safe). `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff`. Check with `craft-header-check.sh --unhashed` | Build writes `.gz` for html and js and wasm: 41.2 MB wasm served as 13.3 MB (13,257,836 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 13,257,836 bytes versus 41,183,059 identity; decoded equals identity) | Not needed: upstream `HOSTING.md` states FilmCraft does not use `SharedArrayBuffer`, and `docs/web.md` says the build is single-threaded on purpose (threads would need isolation and an atomics build). No `SharedArrayBuffer` in the JS or the wasm and no `crossOriginIsolated` read in the JS; `info()` reports the flag for a future threaded build. No COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (frames then composite on the CPU; `?webgl` forces it, `?cpu` the CPU compositor); WebCodecs `VideoDecoder` for H.264, HEVC, VP9 and AV1 with FilmCraft's own decoders as fallback (`?nowebcodecs`); Web Audio `AudioWorklet` for output (same-origin `audio-worklet.js`); OPFS for the recovery journal and kept media; clipboard. WebGPU, the clipboard and OPFS need a secure context (HTTPS from the internal CA, ADR-030). Declared as `browser:` advisories | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |
| effectcraft | 0.10.0 | `application/wasm` (unhashed name) | **Unhashed file names that stay the same between versions, so no `immutable`** (upstream `HOSTING.md` says so): `index.html`, `effectcraft_web.js`, `effectcraft_web_bg.wasm`, `worker.js`, `audio-worklet.js`, `sw.js` (must revalidate: it is the service worker), `snippets/effectcraft-web-<hash>/js/host.js` all `no-cache`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff`. Check with `craft-header-check.sh --unhashed` | Build writes `.gz` for html, js and wasm: 62.5 MB wasm served as 18.7 MB (18,652,595 bytes), also with a `Via: 1.1 Caddy` request. `manifest.webmanifest` is `application/octet-stream` (base `mime.types` lacks the extension; browsers parse manifests regardless of type) (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 18,652,595 bytes versus 62,481,427 identity; decoded equals identity) | Not needed: upstream `HOSTING.md` states EffectCraft does not use `SharedArrayBuffer`. The wasm contains JavaScript-engine error strings (`SharedArrayBuffer.grow ...`, `Atomics`) from an embedded script runtime, but the JS glue, `worker.js`, `host.js` and `sw.js` never create one; concurrency is Web Workers with transferred buffers, not shared memory. The only reads are `crossOriginIsolated` in the `info()` diagnostic, and a code comment in `sw.js` ("the page stays cross-origin isolated") that describes upstream's dev server. No COOP/COEP sent and the check script fails if either appears; the service worker caches whatever headers it is given, so it stays consistent | WebGPU through wgpu with WebGL2 fallback, including a WebGPU device per frame and job worker (`?nogpuworkers`, `?noworkers`); WebCodecs `VideoDecoder`/`VideoEncoder` where available; Web Audio `AudioWorklet`; OPFS, else IndexedDB, else memory for projects and media (`?storage=`); a service worker (`sw.js`, `?nosw` skips it), which needs a secure context, so offline start and installability need HTTPS from the internal CA (ADR-030); clipboard. Declared as `browser:` advisories | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |
| soundcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `soundcraft-web-<hex16>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 15.5 MB served as 5.9 MB (5,889,741 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 5,889,741 bytes versus 15,463,607 identity; decoded equals identity) | Not needed: upstream `HOSTING.md` states SoundCraft plays audio through Web Audio without `SharedArrayBuffer`; the JS and wasm have no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it); Web Audio (`AudioContext`) for output, which the browser starts suspended until a click; no microphone (`getUserMedia`) use found; clipboard needs a secure context (HTTPS from the internal CA, ADR-030). Declared as `browser:` advisories | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |
| designcraft | 0.8.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `designcraft-web-<hex>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 39.6 MB served as 15.3 MB (15,266,666 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 15,266,666 bytes versus 39,550,250 identity; decoded equals identity and the zip's wasm) | Not needed: upstream `HOSTING.md` states DesignCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it, `?cpu` the CPU canvas); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); preferences in `localStorage`; a browser with neither GPU path gets an upstream in-page message. Declared as `browser:` advisories | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |
| cadcraft | 0.7.0 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `cadcraft-web-<hex>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 22.5 MB served as 8.4 MB (8,449,110 bytes), also with a `Via: 1.1 Caddy` request (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 8,449,110 bytes versus 22,546,717 identity; decoded equals identity and the zip's wasm) | Not needed: upstream `HOSTING.md` states CADCraft does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears | WebGPU through wgpu with WebGL2 fallback (`?webgl` forces it, `?cpu` the CPU canvas); WebGPU and the clipboard need a secure context (HTTPS from the internal CA, ADR-030); preferences in `localStorage`; file open and save go through browser upload and download, not the File System Access API. Declared as `browser:` advisories | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |
| craft-launcher (image `craft-craft-launcher`, asset `artcraft-launcher-web-0.4.1.zip`) | 0.4.1 | `application/wasm` (hashed) | `index.html`, other unhashed files: `no-cache`. Hashed `artcraft-launcher-web-<hex>.js`, `_bg.wasm`, `.png`: `public, max-age=31536000, immutable`. `/healthz`: `no-store`. All: `X-Content-Type-Options: nosniff` | Same build rule as WordCraft; `gzip_static` served the `.gz` beside the wasm: 9.6 MB served as 4.2 MB (4,204,488 bytes), also with a `Via: 1.1 Caddy` request. The wasm hash is 15 hex digits and still matches the immutable rule (KVM 2026-10-17, through the Caddy edge with `Accept-Encoding: gzip`: 4,204,488 bytes versus 9,560,083 identity; decoded equals identity and the zip's wasm) | Not needed: upstream `HOSTING.md` states the launcher does not use `SharedArrayBuffer`; the JS has no `SharedArrayBuffer`, `crossOriginIsolated` or Worker use; no COOP/COEP sent and the check script fails if either appears. No Content-Security-Policy is sent, so the browser's calls to `api.github.com` and `raw.githubusercontent.com` (which upstream's CSP note says to allow) are not blocked | WebGPU through wgpu with WebGL2 fallback (egui `wgpu` feature); `localStorage` for settings; the only `?` flag is `?lang=xx`. **Outbound from the browser, not the appliance:** release data from `api.github.com` (60 requests per hour per IP unauthenticated) and pictures from `raw.githubusercontent.com`; the closed `browser:` capability set has no network entry, so this is stated in the package description and exposure warning instead. Declared as `browser:` advisories. Observed in Chromium on the KVM appliance 2026-10-17: at load the page makes 14 requests to `api.github.com` (one `releases/latest` per suite app, all 200) and none to `raw.githubusercontent.com`; with the internet blocked the page still renders and every card reads "NOT AVAILABLE HERE" | `GET /healthz` returns 200 `ok`; image `HEALTHCHECK` every 30 s; container `healthy` (KVM 2026-10-17: `healthy` on the daemon-built image) | `nginx` (uid 101):8080 |

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
- **Resolved (S249 fix 37fa9c2):** behind Caddy the wasm was first served
  **uncompressed** (39.8 MB, no `Content-Encoding`) because Caddy's upstream
  request carries a `Via` header and nginx's `gzip_proxied` defaults to
  `off`. `gzip_proxied any;` in `images/craft/nginx.conf` fixed it
  (build-context digest regenerated). The KVM re-verification on 2026-10-17
  recorded `scripts/craft-header-check.sh` PASS through the edge, gzip
  included: 14,947,550 bytes with `Accept-Encoding: gzip` versus 39,785,016
  identity. `TestRealCraftBuild` also requests with `Via: 1.1 Caddy`.

Notes for GridCraft v0.7.0, PdfCraft v0.8.0 and DeckCraft v0.7.0 (S250).
First measured 2026-10-17 on locally built images (read-only root, `/tmp`
tmpfs, all capabilities dropped), then **KVM-verified 2026-10-17** on VM 2
through the real edge (`https://<app>.lan/`, Caddy, internal CA leaf, curl
with the root and no `-k`, HTTP/2, daemon-built images): `craft-header-check.sh`
PASS for all three, including gzip through Caddy (`gzip_proxied any`).
Measured wasm sizes, identity versus gzip: gridcraft 18,886,586 versus
7,045,260 bytes; pdfcraft 38,059,629 versus 14,064,403; deckcraft 25,537,284
versus 9,345,219. The gzip-decoded and identity bodies have equal SHA-256 and
equal the wasm inside the upstream zip. `/healthz` 200 `no-store`, entry
`no-cache` with `nosniff`, no COOP/COEP. The same check passed again after
remove and reinstall and after the update rebuild:

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
- KVM image IDs: gridcraft `sha256:0145d7b5a524...`, pdfcraft
  `sha256:f6c54ba1dcfb...`, deckcraft `sha256:61127991a062...` (local
  workstation builds differed: `90b1b49768cb...`, `b2b57d93b072...`,
  `4c8dbe82f805...`).

Notes for VectorCraft v0.12.0, PhotoCraft v0.9.0, LightCraft v0.8.0 and
FigCraft v0.6.0 (S251). First measured 2026-10-17 on locally built images (the
real release artifact through the shared `images/craft` Dockerfile, run with a
read-only root, `/tmp` tmpfs, all capabilities dropped), then **KVM-verified
2026-10-17** on VM 2 through the real edge (`https://<app>.lan/`, Caddy,
internal CA leaf, curl with the root and no `-k`, HTTP/2, daemon-built
images): `craft-header-check.sh` PASS for all four (LightCraft with
`--unhashed`), including gzip through Caddy; the same check passed again after
remove and reinstall and after the update rebuild. Measured wasm sizes,
identity versus gzip: vectorcraft 64,737,882 versus 22,507,398 bytes;
photocraft 25,033,027 versus 11,417,725; lightcraft 27,259,177 versus
10,458,639; figcraft 22,507,647 versus 8,802,810. The gzip-decoded and
identity bodies have equal SHA-256. `/healthz` 200 `no-store`, entry
`no-cache` with `nosniff`, no COOP/COEP. LightCraft through the edge:
`worker.js` 200 `application/javascript` `no-cache` (393 bytes gzip),
`lightcraft_web.js` 200 `application/javascript` `no-cache`, and
`lightcraft_zh_hans.otf` 200 `application/octet-stream` `no-cache`, gzip
13,588,233 bytes.

- Layout: each zip unpacks into one `<app>-web-<version>/` directory. Three
  (vectorcraft, photocraft, figcraft) are the WordCraft layout: one hashed
  ES-module `.js`, one hashed `_bg.wasm`, licences, docs and the unused sample
  `_headers`/`.htaccess` (their `_headers` say hashed assets are immutable;
  the shared rules agree). No `parts.txt`, no `.part-*`, no service worker,
  no extra asset classes, no favicon file. VectorCraft adds `ASSETS.md`,
  `NOTICE` and a `licenses/` tree; FigCraft adds `NOTICE` and twenty `OFL-*.txt`
  files. PhotoCraft's and VectorCraft's optional `fonts/` folders are not in
  these zips.
- LightCraft is the exception: unhashed `lightcraft_web.js` and
  `lightcraft_web_bg.wasm`, a module `worker.js` that re-instantiates the
  wasm, a 16.4 MB `lightcraft_zh_hans.otf` that `index.html` fetches in
  parallel with the wasm, and upstream-supplied `.gz`/`.br` copies. Upstream
  deliberately forbids `immutable` on these names (an upgrade would otherwise
  mix old glue with a new module). The shared nginx rules already serve this
  correctly (`no-cache`, `application/wasm`, JavaScript type for the worker,
  gzip), so the shared image did not change and the craft-manifest context
  digest is unchanged. The only shared change is the check script's new
  `--unhashed` flag (it expects `no-cache` instead of `immutable` for the
  wasm and js); without the flag the script fails LightCraft, as intended.
- The `.htaccess` request answers 404 (dotfiles); the sample `_headers` is
  served as a plain file, unchanged from S249.
- Browser capabilities, from each `HOSTING.md` and a grep of the shipped JS
  and wasm: no `SharedArrayBuffer`, `crossOriginIsolated`, service worker or
  `.part-` use in any JS; the only `Worker` constructor is LightCraft's. Wasm
  hits for `clipboard` are API names, not isolation features. VectorCraft,
  PhotoCraft and FigCraft call `requestAdapter` (WebGPU with WebGL2
  fallback); LightCraft does not (WebGL2 only). COOP/COEP and
  `Cross-Origin-Resource-Policy` are not needed for any of the four.
- FigCraft limitation (upstream `HOSTING.md`): the web build joins a live
  session hosted by the desktop app over plain `ws://`, which a browser
  blocks from an `https://` page except to the same computer. Under ADR-030
  the route is HTTPS, so live sessions with other machines do not work there
  (recorded in docs/DEFERRED.md; nothing is added to the image).
- Healthcheck and writable paths: all four reported `healthy`, `docker diff`
  of the running container was empty, `Config.Volumes` is null and the user is
  `nginx`. KVM image IDs: vectorcraft `sha256:86362484a3f8...`, photocraft
  `sha256:93f9c69875c3...`, lightcraft `sha256:a03024d960b3...`, figcraft
  `sha256:38609e258554...` (local workstation builds differed:
  `10c798820dfa...`, `0293521da666...`, `cf098fb75e8b...`, `9db14c147be1...`).
  On the workstation the gzip-decoded wasm of each equals the wasm
  inside the upstream zip (SHA-256 compared).
- `scripts/craft-header-check.sh` PASS against each container over HTTP
  (LightCraft with `--unhashed`), and over the S280 HTTPS host on the KVM
  appliance (see above).

Notes for FilmCraft v0.8.0, EffectCraft v0.10.0 and SoundCraft v0.7.0
(S252). Measured 2026-10-17 on locally built images (the real release
artifact through the shared `images/craft` Dockerfile, run with a read-only
root, `/tmp` tmpfs, all capabilities dropped), then **KVM-verified
2026-10-17** on VM 2 through the Caddy edge with the S280 root (daemon-built
images, throwaway-signed manifest counter 9):

- Layout: each zip unpacks into one `<app>-web-<version>/` directory.
  SoundCraft is the hashed WordCraft layout. FilmCraft has unhashed glue and
  wasm plus `audio-worklet.js`. EffectCraft has unhashed glue and wasm,
  `worker.js`, `audio-worklet.js`, `sw.js`, a web manifest, icons and a
  `snippets/.../js/host.js`. None ships `parts.txt` or `.part-*` files.
- Cross-origin isolation: checked per app in `HOSTING.md`, `docs/web.md` at
  the tag, the shipped JS and the wasm strings. None needs it, so no
  COOP/COEP is added and the shared nginx and the build-context digest
  (`sha256:1880843e...`) are unchanged. The check script passes only while
  both headers are absent. If a future threaded build needs them, that is a
  per-package change to the shared image (ADR first, `docs/DEFERRED.md`).
- Browser capabilities (all non-blocking, shown as advisories from the
  manifest `browser:` field): WebCodecs (FilmCraft, EffectCraft), WebGPU with
  WebGL2 fallback (FilmCraft, EffectCraft; SoundCraft prefers WebGPU but
  needs only WebGL2), Web Audio and a working audio output (all three),
  secure context (all three), browser storage (FilmCraft, EffectCraft).
- Same-origin workers and `AudioWorklet` modules are served as JavaScript
  from the same origin, so they need no extra headers.
- Healthcheck and writable paths: all three reported `healthy`, `docker diff`
  of the running container was empty, `Config.Volumes` is null and the user is
  `nginx`. KVM image IDs: filmcraft `sha256:14bc52c4fb5a...`, effectcraft
  `sha256:a44e76b0221c...`, soundcraft `sha256:d161c915d241...` (identical
  after the update rebuild; local workstation builds differed:
  `5ead2f4b9878...`, `7d6c4e09c505...`, `521287838d8d...`). The
  gzip-decoded wasm of each equals the wasm inside the upstream zip (SHA-256
  compared).
- `scripts/craft-header-check.sh` PASS against each container over HTTP
  (FilmCraft and EffectCraft with `--unhashed`) and over the S280 HTTPS host
  on the KVM appliance (gzip through Caddy included), after install and
  again after the update rebuild. EffectCraft `sw.js` 200
  `application/javascript` `no-cache`; `manifest.webmanifest` 200
  `application/octet-stream` `no-cache` (deferred). In Chromium the service
  worker registered and activated over `https://effectcraft.lan/` (secure
  context true, `crossOriginIsolated` false, as expected).

Notes for DesignCraft v0.8.0, CADCraft v0.7.0 and Craft Launcher v0.4.1
(S253). Measured 2026-10-17 on locally built images (the real release
artifact through the shared `images/craft` Dockerfile, run with a read-only
root, `/tmp` tmpfs, all capabilities dropped), then **KVM-verified
2026-10-17** on VM 2 (daemon-built images, over the S280 HTTPS host through
the Caddy edge):

- Layout: each zip unpacks into one `<asset>-<version>/` directory (the
  launcher's is `artcraft-launcher-web-0.4.1/`, not `craft-launcher-...`).
  All three are the hashed WordCraft layout: `index.html`, one hashed `.js`,
  one hashed `_bg.wasm` and licence files (the launcher also ships a hashed
  `.png`, `NOTICE` and `ATTRIBUTION.md`). No workers, service worker,
  `parts.txt` or precompressed files. The sample `_headers` and `.htaccess`
  ship in the zip; `.htaccess` is a dotfile, removed at build and answered
  404, while `_headers` has no leading dot and is served as a harmless text
  file (200), as for the other apps.
- Cross-origin isolation: checked per app in `HOSTING.md`, the source at the
  tag, the shipped JS and the wasm strings. None needs it, so no COOP/COEP
  is added and the shared nginx and the build-context digest
  (`sha256:1880843e...`) are unchanged.
- Browser capabilities (non-blocking `browser:` advisories): WebGPU with
  WebGL2 fallback, secure context and `localStorage` preferences for all
  three; clipboard for DesignCraft and CADCraft only.
- Healthcheck and writable paths: all three reported `healthy`, `docker diff`
  of the running container was empty, `Config.Volumes` is null and the user
  is `nginx`. KVM image IDs: designcraft `sha256:25732064628610...`,
  cadcraft `sha256:28e681d6d50c...`, craft-craft-launcher
  `sha256:848b59433394...` (identical after the update rebuild). The
  gzip-decoded wasm of each equals the identity wasm and the wasm inside the
  upstream zip (SHA-256 compared). Chromium loaded each app over HTTPS (secure
  context true, `crossOriginIsolated` false, as designed); the only console
  error was the implicit `/favicon.ico` 404 (the zips ship no favicon).
- `scripts/craft-header-check.sh` PASS for all three through the Caddy edge
  over the S280 HTTPS host (entry and wasm fetched, no `-k`), at install and
  again after the update.

## Checking a row

```
scripts/craft-header-check.sh https://wordcraft.lan/ --cacert root.pem \
    [--resolve wordcraft.lan:443:APPLIANCE_IP] [--coop-coep] [--unhashed]
```

It prints every response header it inspects and exits 1 on a failure:
`/healthz` 200, entry `no-cache` and `nosniff`, every `.wasm` referenced by
`index.html` as `application/wasm`, immutable and gzip, every `.js`
immutable with a JavaScript type, and COOP/COEP absent (or present with
`--coop-coep`). With `--unhashed` (LightCraft, whose wasm and js keep the same names across versions) the wasm and js must be `no-cache` instead of immutable. Use the internal CA root from the dashboard Trust page or
`homesteadctl tls root export` for `--cacert` (ADR-030).

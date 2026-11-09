# S436 - API Client as an Everything catalog package (net.fetch)

## Goal and dependencies
Fourteenth Everything app (ADR-057): `api-client` (build and send HTTP requests, collections, environments, history,
import from cURL/OpenAPI/Insomnia/HAR) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Originally S381; un-deferred under ADR-058. Depends on S367, S434 (net
fence) and S435 (`net.fetch`).

## Source and licence
`third_party/everything/api-client-source.tar.gz` (133 entries, 506,505 bytes, SHA-256
`35a33090e2a23e96cc633c086558b5a8d2f70dbb21d7d92e33950ba4d1c79146`), same recipe as the other archives. **MIT**
(repository root). Bundled and served locally: CodeMirror and Lezer (MIT), yaml (ISC), lucide (ISC). No copyleft
licence, so no source offer (unlike S423) and no DEFERRED licence line.

## Network: net.fetch
Upstream has three transports: a browser `fetch` (blocked by the page policy `connect-src 'self'`), a raw-socket relay
and Puter's relay. In the app (`puter.env === 'app'`):
- The transport is forced to the relay and the saved/default preference is `relay`; the other options are hidden.
- `scripts/everything/files/api-client-net.js` (added as `src/core/homestead-net.js`) builds the request body, merges
  headers (default `HomesteadAPIClient` User-Agent), refuses bodies over 2 MB and bodies on GET/HEAD, races
  `puter.net.fetch` against abort and timeout, and returns upstream's response shape. Fence errors (`net_failed`,
  `net_invalid`, `net_timeout`, `net_rate_limited`, `net_too_large`, `net_disabled`) become plain messages; `net_failed`
  reads "That address could not be reached from your Homestead. Only public internet addresses are allowed."
- The package sets `HOMESTEAD_EVERYTHING_NET: "1"` in `catalog/api-client.yaml`, which the bridge reads at start (the
  `-net` flag is the same switch). No other package sets it.
- Raw sockets (`net.Socket`) throw "Raw sockets are not available on Homestead." WebSocket entries (New, Add, imports) are
  removed and a connection attempt reports "WebSocket connections are not available on Homestead." Sharing is hidden
  (`shared = null`). No AI is used.

## SDK surface
| Call | Decision |
| --- | --- |
| `net.fetch` | supported (S435 limits: 2 MiB request, 8 MiB response, 10 s connect, 30 s total, 60 a minute per person) |
| `fs.write`, `fs.read`, `fs.readdir`, `fs.delete`, `auth.getUser` | supported; the sync `readdir` of a missing folder is treated as empty by patch |
| `fs.mkdir` | absent in the shim; the app copes |
| `fs.share`, `fs.unshare`, `fs.getShares`, `fs.listShared`, `kv` sharing | refused; unreachable (sharing hidden) |
| `net.Socket`, `net.tls.TLSSocket` | refused; the entry points throw a visible message |
| `ai` | not used |

## Storage
IndexedDB workspace in the browser, mirrored to the bridge's per-person files by upstream's `CloudSync`.
`scripts/everything/files/api-client-person-storage.js` (added as `src/homestead/boot.js`) tags `localStorage` keys with a
person tag (first 8 bytes of SHA-256 of `pacl:` plus the subject, memory only if unidentified) and then loads the app.
No service worker (upstream has none worth keeping; offline use is not offered).

## Other changes
- `scripts/everything/apps/api-client.json`: 60 exact-once patches (Puter wording and links removed, relay forced,
  sockets and WebSocket disabled, sharing hidden, sync fix, entry point).
- `catalog/api-client.yaml`, `pin.yaml`, `THIRD_PARTY.md`, Go catalog and SSO tests, `tests/test_s436_api_client_package.py`,
  `scripts/everything/test_api_client_net.mjs`, `scripts/everything/gate/run_browser_check_api-client.sh` and
  `browser-check-api-client.mjs`.
- No change to prepare_app.py, the bridge, the shim or the CSP rule.

## Tests
Upstream vitest: 679 passed, pristine and patched alike. Node test of the relay mapping with a fake `net.fetch`. Python
package test. Go: catalog rows and sign-in. Real-browser check (headless Chrome, bridge started with `-net`): CSP-clean
boot and sync, no Puter wording or WebSocket entry, a send to `http://127.0.0.1:9/private` refused with the
"could not be reached" message and `blocked_address` in `everything-bridge net-log`, every request on the app origin,
a second person gets a separate workspace database and signs in separately, no console errors.

## Not proven
See `docs/DEFERRED.md` (S436 lines).

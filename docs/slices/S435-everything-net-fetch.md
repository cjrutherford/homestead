# S435: Everything net.fetch operation

ADR-058 section 5. Builds on the S434 fence. No daemon, API, CLI or dashboard change, so no PARITY change (the `net-log` tool is a bridge tool like `links`).

## Scope
The `net.fetch` data operation, its limits, `net_log`, the `net-log` tool, the `-net` switch and the `puter.net.fetch` shim.

## Design as built
- **Switch.** `everything-bridge -net` or `HOMESTEAD_EVERYTHING_NET=1` (`Store.SetNet`). Off by default: `403 net_disabled`, nothing logged, nothing dialled. Same pattern as `-sharing`.
- **Door.** Same as other writes: exact configured Origin, `Sec-Fetch-Site`, verified session, CSRF, JSON. A public-name origin can only ask for read links, so `net.fetch` is local/overlay only.
- **Transport.** `{operation:"net.fetch", url, method, headers, body(base64)}` in; `{status, statusText, headers, body(base64)}` out. Request body at most 2 MiB; response body at most 8 MiB (over the cap is `413 net_too_large`, never a short body); 10 s connect and 30 s total (the fence client); the response write deadline is extended for the call.
- **One client.** Every request uses `NetFence.Client()`. Per request the handler builds a fence copy whose dialer records the address actually used (for the log); resolver, TLS config and checks are the S434 ones.
- **Methods.** GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS (ADR-058 does not list them; this is the slice's choice). GET and HEAD with a body are refused. Anything else: `net_invalid`.
- **Request headers.** Dropped: Host, Cookie, Forwarded, `X-Forwarded-*`, `Proxy-*`, X-Real-Ip, hop-by-hop (Connection, Keep-Alive, TE, Trailer, Transfer-Encoding, Upgrade), Content-Length, Expect, Accept-Encoding (the transport negotiates and decodes; the 8 MiB cap applies after decoding). The bridge sets its own User-Agent. App `Authorization` is kept (it is the app's own credential for the target; the fence drops it across origins on redirects). At most 64 headers, names are HTTP tokens, values have no control characters and at most 8 KiB.
- **Response headers.** Allowlist (ADR gives no list; slice's choice): content-type, content-language, content-disposition, content-range, accept-ranges, etag, last-modified, cache-control, expires, link, retry-after, vary, date, age, allow, location, www-authenticate, `x-ratelimit-*`. `Set-Cookie` and everything else is dropped.
- **Limits.** Per person: 60 a minute, 5,000 a day (sliding windows), 5 concurrent. Per app: 20 concurrent. Keys include the app, so apps never share a budget. A refused attempt is not counted. Over a limit: `429 net_rate_limited`, logged as `rate_limited`. State is in memory. Every attempt that passes the limiter counts, including ones the fence then refuses.
- **Uniform errors** (`{error, message}`): `net_disabled` 403, `net_rate_limited` 429, `net_invalid` 400 (bad method/URL/scheme/userinfo/host/headers/body size), `net_failed` 502 (blocked address, DNS failure, mail port, bad redirect, connect failure: indistinguishable to the app so the operation cannot map the appliance's network), `net_too_large` 413, `net_timeout` 504, `net_unavailable` 503 (the log could not be written: fail closed). The class is only in the log.
- **net_log.** Table `net_log`: id, time, app, person (per-app pseudonym, never the sign-in subject), method, scheme, host, port, path without query or fragment (200 chars), resolved address, status, bytes in (response) and out (request body), duration, class. Never URLs with query strings, user information, headers or bodies. Kept 30 days or the newest 10,000 rows, pruned on insert. Classes: `ok`, `rate_limited`, `invalid`, and the fence classes (`scheme`, `userinfo`, `host`, `port`, `blocked_address`, `resolve`, `redirect`, `connect`), `timeout`, `too_large`, `upstream`.
- **Tool.** `everything-bridge net-log -db FILE -app APP [-limit N]` prints newest first; offline, never creates a data file.
- **Shim.** `puter.net.fetch(url|Request-like, init)` returns a `Response`. Bodies: string, ArrayBuffer, typed array, Blob, URLSearchParams. `net.Socket`, `net.tls.TLSSocket` throw `bridge_unsupported`; ws/wss/wisp URLs reject `bridge_unsupported`. ADR-057 table updated.

## Tests
- `internal/everything/netfetch_test.go`: disabled by default; round trip with header dropping/forwarding, response header allowlist and log row content; no secrets (query, cookie, token, body, subject, user information) in the log; redirect followed and redirect to loopback refused; refusals for private/mixed/loopback/decimal/metadata/IPv6/NXDOMAIN/mail port/ftp/ws/userinfo/no host/junk with uniform answers, class in the log and zero dials; input validation (methods, GET body, body cap and exactly-at-cap, CRLF header, bad name, too many headers); response cap (exactly 8 MiB passes, one more byte is `net_too_large`); door (CSRF, foreign origin, no origin); per-minute limit with cross-person and cross-app isolation, logs per app, window slide; day and concurrency limits (limiter unit); log retention (10,000 rows, 30 days); timeout classification. Run with `-race`.
- `cmd/everything-bridge/tools_test.go`: `net-log` output, app isolation, limit, usage, no file creation.
- `scripts/everything/gate/bridge-gate.test.js`: off by default (`net_disabled`), sockets and wss unsupported; with `-net`, local/metadata/mail targets are `net_failed`, bad input `net_invalid`, no session is 401.

## Unproven
No real public host or TLS handshake (fake resolver and dialer onto local servers). The browser `Response` built from a real upstream was not exercised against a public site. Limits reset on restart.

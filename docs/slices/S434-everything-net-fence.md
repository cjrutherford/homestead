# S434: Everything net egress fence

ADR-058 section 5 (address fence). Library only: no `net.fetch` operation, limits, `net_log`, tool or shim change (S435/S436). No daemon, API, CLI or dashboard change, so no PARITY change.

## Scope
`internal/everything/netfence.go`: address classifier, a dialer pinned to the checked address, and a redirect checker, exposed as `NetFence.Client()` for S435 to use.

## Design as built
- `FenceAddrBlocked(ip)`: the ADR's IPv4 and IPv6 table (plus `64:ff9b:1::/48`, local-use NAT64). IPv4-mapped (`::ffff:a.b.c.d`) and IPv4-compatible (`::a.b.c.d`) are unwrapped and judged as IPv4; NAT64 `64:ff9b::/96`, 6to4 `2002::/16` and Teredo `2001::/32` are blocked whole, so they cannot embed a refused IPv4. `fd00:ec2::254` falls in `fc00::/7`; `169.254.169.254` in `169.254/16`; broadcast in `240/4`. An invalid address is blocked.
- IP literals in any spelling (decimal `2130706433`, hex, octal, short dotted `127.1`, bracketed IPv6) are parsed inet_aton-style and judged without a lookup.
- `NetFence.Resolve`: one lookup, every answer must pass (any blocked answer refuses the host). `DialContext`: port check, resolve once, then dial `ip:port` of the checked answers in order; the name is never dialed or looked up again. The default dialer also has a `net.Dialer.Control` callback that re-checks the real socket address and port at connect time (10 s connect). `Dial` and `Resolver` are injectable for tests.
- `CheckRedirect`: at most 5 redirects, each hop re-runs scheme/userinfo/host/port checks (the dialer re-checks the address), https to http refused, `Authorization` and `Cookie` dropped when the origin (scheme, host, port) changes. The first request is checked by a wrapping round tripper.
- `Client()`: no proxy (environment ignored), no cookie jar, keep-alive off (every hop dials and checks), TLS 1.2 minimum, verification on, caller config cannot weaken it, 30 s total.
- Refusals are `*FenceError{Class, Detail}` with stable classes (`scheme`, `userinfo`, `host`, `port`, `blocked_address`, `resolve`, `redirect`, `connect`) for S435's log and uniform app-facing errors.
- Ports refused: 25, 110, 143, 465, 587, 993, 995 (ADR list).

## Tests (`netfence_test.go`)
Every blocked range in both families (boundary allowed neighbours too), embedded-IPv4 forms (mapped, compatible, NAT64, 6to4, Teredo), literal spellings, resolver answers (private, mixed public+private, empty, NXDOMAIN), pinning (dials the checked IPs, never the name), rebinding (public then private answers: one lookup per dial, second dial refused), every mail port and bad ports, URL scheme/userinfo/host, redirects to private, loopback literal, metadata v4/v6, decimal host, mail port, ftp, userinfo, redirect loop cap (5), https-to-http, credential stripping across and not within an origin, first request fenced, proxy env ignored, TLS minimum not weakened, and the real default dialer refusing a loopback listener. No real internet.

## Unproven
No real public host or TLS handshake was exercised (tests use a fake resolver and dialer onto a local server). Response size/time caps, request header/body rules and rate limits are S435.

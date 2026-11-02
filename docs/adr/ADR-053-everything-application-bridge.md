# Everything applications: independent application bridge

## Status
Accepted

Accepted as the constraints for the S367 spike. No application is shipped until its exit criteria pass; unresolved implementation details must meet these constraints.

## Context
The selected HeyPuter/Everything applications expect Puter's browser SDK. Running their static assets alone would leave identity and data dependent on a hosted service. Homestead must provide customer-owned data and ordinary Compose operation without its daemon. The existing craft file proxy is owner-only, depends on the daemon, and does not supply per-person application storage; it cannot be reused as a complete Everything bridge.

At upstream revision e7d07c48546ef0650a8f3ab94ae82b8b50d3646a, Tasks uses authentication, key-value documents, files, offline synchronization and optional sharing. Its own tests install a mock SDK, so passing them unchanged is not proof of bridge compatibility. Public share links and cross-user access require explicit authorization and exposure; no feature may silently make personal data public.

## Decision
- Pin reviewed source and its integrity; retain upstream licences and notices. Replace the hosted SDK reference with a Homestead-owned bridge and prohibit fallback to Puter cloud. Build images from pinned, verified inputs with a reproducible, reviewable process.
- Run application assets, identity integration and the data bridge in ordinary Compose services. The appliance's daemon is not required for a running app or to read and recover its data. Configure Authentik using the existing approved app sign-in flow; specify a usable, documented local recovery/sign-in path. Never hand the application an owner management session or generic management access.
- Persist application data on declared mounted storage, included in backups. Scope private data by stable authenticated subject and application, never by a client-supplied user identifier. Preserve inspectable, exportable data and document recovery without the daemon. Bound sizes and validate paths, concurrency and errors.
- Authenticate and authorize each operation server-side; check same-origin requests and CSRF on mutations. Do not assume path-based apps are separate browser origins. Deny forged, expired, revoked, cross-person and cross-application access. No broad CORS.
- Support sharing only with explicit authorization and the existing reviewed exposure policy; fail closed if required capabilities are not implemented. No token-bearing link is logged. Preserve single-use and revocation semantics where applicable.
- The spike must run meaningful upstream Tasks behavior through the actual bridge (not its in-memory mock), including storage, offline recovery, isolation and the chosen handling of sharing. Validate build, restore, and Compose operation independently of the daemon. If that cannot be demonstrated, stop S367 and do not ship dependent app slices.

## Consequences
This creates a new application data and authorization boundary. It cannot be inferred from the craft proxy or advisory catalog integration metadata. S382's serving and local-name gate remains outstanding; the new application must not be advertised as fully available until its own reachable-name and lifecycle checks pass.

## Amendment 2026-11-02: identity, sessions, CSRF and sharing (owner decisions)
Decided by the owner on 2026-11-02. These resolve the open mechanism questions above for S367.

- **Identity is OIDC inside the bridge.** The bridge is its own Authentik OIDC client with its own session. It is not edge forward-auth, and no header from a proxy is trusted as identity. It uses the authorization-code flow with PKCE (S256), a random `state` bound to the browser by a short-lived HttpOnly cookie, and a random `nonce`. The ID token is accepted only after signature verification against the issuer's JWKS and checks of `iss`, `aud` (and `azp` when several audiences), `exp` and `nonce`. Unsigned and symmetric algorithms are refused.
- **Subject is the Authentik `sub` claim**, stable per person, and is never client-supplied. Data scope is (application, `sub`).
- **Server-side sessions.** The cookie is an opaque random identifier (only its hash is stored), `HttpOnly`, `Secure`, `SameSite=Lax`, with `Path` limited to the application's path. Sessions have an idle and an absolute expiry. Logout deletes the session server-side, so a copied cookie stops working.
- **CSRF.** Each session has a random token. The bridge injects it as `<meta name="homestead-csrf">` when it serves `index.html` (signed-in sessions only; an unauthenticated load redirects to sign-in). Every mutation, including data writes and logout, requires that token in `X-Homestead-CSRF` and an exact, allowlisted `Origin`.
- **Client secret** is read only from a file (`*_FILE` convention, ADR-021/ADR-052), never from an environment value or argv. A CA bundle file may be given for a private issuer.
- **Local recovery.** The bridge runs without the Homestead daemon, and all data is an ordinary SQLite file on declared mounted storage that the owner can open, read and export with stock `sqlite3` while the bridge is stopped. Signing in depends on Authentik, as for every other approved app; there is no separate bridge password. If Authentik is unavailable, the owner recovers data from the SQLite file, not through the app.
- **Sharing.** Tasks' Share button and calendar-feed (ICS URL) setting are hidden in the build by a patch applied in `prepare_tasks.py` against the pinned upstream commit; a test fails if the patch target changes. The bridge still refuses signed public URLs. The private snapshot file that Tasks writes into the person's own scope before the refused share URL is accepted and needs no refusal.

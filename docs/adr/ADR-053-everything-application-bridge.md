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

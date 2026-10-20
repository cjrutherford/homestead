# Slice Index

See `docs/slices/` for individual slice contracts.

Phases:
0. Constitution and ADRs
1. Native appliance kernel
2. Dashboard foundation
3. Host discovery
4. Host firewall
5. Operation engine
6. Docker platform
7. Local networking
8. Domain and public DNS
9. TLS
10. Remote access
11. Public exposure
12. Router integration
13. Mounted storage
14. Catalog
15. Jellyfin vertical slice
16. Base suite
17. Backups
18. Updates
19. Identity/SSO
20. Expert management
21. Bootstrap
22. Recovery
23. Distribution

Craft app slices after the phase list (S249 to S254, S279 to S281):
S254 Nextcloud craft integration (hooked apps: GridCraft, CADCraft, PdfCraft
full; FilmCraft, EffectCraft open-only), then S281 Craft apps start page
(`docs/slices/S281-craft-start-page.md`, governed by ADR-031, Accepted 2026-10-17).

Dashboard design slices (owner direction 2026-10-18, extending S248 under
ADR-019), one at a time: S282 visual foundation
(`docs/slices/S282-dashboard-visual-foundation.md`), S283 states,
transitions and work orders (`docs/slices/S283-dashboard-states-and-transitions.md`),
S284 page passes (`docs/slices/S284-dashboard-page-passes.md`).

Dashboard order of work (owner direction 2026-10-18): S282 (done) → S283 (done) →
S285 catalog install wizard (`docs/slices/S285-catalog-install-wizard.md`) →
S286 dashboard sign-in with Authentik, governed by ADR-033, Accepted
(`docs/slices/S286-dashboard-sso-authentik.md`) → S287 post-claim setup
walkthrough, governed by ADR-034, Accepted 2026-10-18
(`docs/slices/S287-post-claim-setup-walkthrough.md`) → S284. The
catalog install flow moved from S284 to S285; S284 now covers the catalog list
only.

Then S288 open bug fixes from the S285–S287 reviews (owner direction
2026-10-19; `docs/slices/S288-open-bug-fixes.md`).

Then S289 (done, KVM pending) dashboard on a local network address (ADR-035 first; `docs/slices/S289-lan-dashboard-address.md`) → S290 public routes for apps on the tunnel, including adopting an existing tunnel (`docs/slices/S290-public-app-routes.md`). Owner direction 2026-10-19: setup testable without the tunnel, and public routes defined in the app.

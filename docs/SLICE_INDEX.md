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

Then S291 step-by-step setup wizard (ADR-036 supersedes ADR-034; `docs/slices/S291-setup-wizard.md`), run before S290. Owner direction 2026-10-20: wizard with inline steps, linear with skip, new Storage step, outside-Homestead tasks explained in each step.

Then S292 setup walkthrough fixes (`docs/slices/S292-setup-walkthrough-fixes.md`): the bugs found walking setup from scratch on the KVM VM, owner direction 2026-10-20. S293 storage step UX, S294 domain and tunnel (ADR first) and S295 app names, app SSO and backups in setup (ADR first) follow.

Then S293 storage step UX (`docs/slices/S293-storage-step.md`): data disks first, class explanations, a suggested layout applied in one approval, and the actor recorded from the session. S294 domain and tunnel (ADR first) and S295 app names, app SSO and backups in setup (ADR first) follow.

Then S294 domain and tunnel plan (ADR-037; `docs/slices/S294-domain-and-tunnel-plan.md`), S295 app names, app SSO and backups in setup (ADR first), and S296 public dashboard behind Authentik (ADR-037; `docs/slices/S296-public-dashboard-behind-authentik.md`). Owner decisions 2026-10-20.

Then S295 app names (ADR-039; `docs/slices/S295-app-names.md`): `<app>.local` over mDNS and an optional daemon resolver for `.lan`. S296 public dashboard behind Authentik, S297 nightly backups in setup (`docs/slices/S297-nightly-backups-in-setup.md`) and S298 app sign-in through Authentik, Jellyfin's pinned plugin included (`docs/slices/S298-app-sign-in.md`), follow. Owner decisions 2026-10-20.

Then S299 uninstall leftovers (`docs/slices/S299-uninstall-leftovers.md`): uninstall refuses while an app has public names or sign-in with Authentik, and Authentik while apps sign in through it; `leave_sign_in` acknowledges leaving the sign-in in Authentik.

Then S300 tunnel connector token off the command line (`docs/slices/S300-connector-token-off-argv.md`): the connector reads `TUNNEL_TOKEN` from its root-only environment file instead of `--token` in argv, and install restarts a running connector.

Then S301 claim first, then the tunnel (`docs/slices/S301-claim-before-tunnel.md`): documentation only; claim locally or over an SSH forward, and a hand-laid tunnel adopted after claim as the last resort.

Then S302 adopted tunnel zone (`docs/slices/S302-adopted-tunnel-zone.md`): the tunnel's zone name is stored, so an adopted tunnel without a setup domain proposes app public names and remove plans name the zone.

Then S303 tunnel plan refusal in view (`docs/slices/S303-tunnel-plan-error-in-view.md`): a refused tunnel plan shows under Review plan, and the adopt hint says a reset appliance names a new tunnel.

Then S304 setup names a needed sign-in re-plan (`docs/slices/S304-setup-sign-in-replan.md`): after a tunnel, the setup Sign-in and Domain step details say to re-plan Sign-in.

Then S305 walkthrough wording (`docs/slices/S305-walkthrough-copy.md`): the install wizard says the review can connect sign-in, and the nightly policy review lists the apps it covers.

Then S306 factory reset (`docs/slices/S306-factory-reset.md`): `scripts/factory-reset.sh` returns an appliance to the unclaimed state for a new owner, removing only what Homestead owns.

Then S307 install guide (`docs/slices/S307-install-guide.md`): `docs/INSTALL.md` takes an appliance from bare metal to public apps behind Authentik, and names the prerequisites Homestead does not install.

Then S308 bootstrap installs Docker and cloudflared (`docs/slices/S308-bootstrap-docker-cloudflared.md`, ADR-042): vendor apt repositories behind pinned key fingerprints, existing installs kept, opt-outs.

Then S309 dashboard design language (`docs/slices/S309-dashboard-design-language.md`): a Hedgerow sidebar with a current-page notch, a masthead sentence on every page, fields with state furrows, a Needs you field on Status and Setup, ledger definition lists, and every plan review as a permit with a digest seal and an Approved or Not approved stamp.

Then S310 lamplight (`docs/slices/S310-lamplight-depth-and-dark-default.md`): dark by default (Dark, Light, Match my system, High contrast), one light source and four tokenised depths (hedgerow, ground, plate, lifted, plus the well), lamps only for attention, a contour band and horizon under the masthead, the permit as lit paper, and the Status Appliance facts as a ledger.

Then S311 lamplight on every page (`docs/slices/S311-lamplight-on-every-page.md`): every routed page's sections are fields (plates) under the masthead, empty states are wells, claim and login are a centred lifted gate, failed rail orders are lamps, and `theme/lamplight.spec.ts` enforces it for every route.

Then S332 release 0.0.1 (`docs/slices/S332-release-0-0-1.md`): `VERSION` file, `make release` (linux amd64 and arm64 binaries and ADR-025 `SHA256SUMS`), a tag-triggered draft-prerelease workflow, `CHANGELOG.md` and `docs/RELEASING.md`.

Then S334 Authentik step-up on the public address (`docs/slices/S334-authentik-step-up-on-the-public-address.md`): `POST /api/v1/sso/step-up`, a fresh Authentik sign-in that steps an oidc session up for five minutes so the five People step-up routes work on a public dashboard name (error code `stepup-required`, ADR-049 §1-2).

Then S335 step-up messages (`docs/slices/S335-step-up-messages.md`): the dashboard shows a plain message for a failed Authentik step-up (`?sso_error=`) and drops the param; step-up routes that are not People routes answer 403 `local-only` on a public name and the dialog points to the local address.

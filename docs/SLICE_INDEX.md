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

Craft app slices after the phase list (S249 to S254, S279 to S281, S382):
S254 Nextcloud craft integration (hooked apps: GridCraft, CADCraft, PdfCraft
full; FilmCraft, EffectCraft open-only), then S281 Craft apps start page
(`docs/slices/S281-craft-start-page.md`, governed by ADR-031, Accepted 2026-10-17).
S382 is the later validation gate: it re-runs all 13 craft packages and
closes the local DNS advertisement gap that the S249-S253 KVM runs bypassed
with `--resolve` or host-resolver rules.

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

Then S336 Authentik's login page tells people to ask the owner (`docs/slices/S336-forgot-password-asks-the-owner.md`): no self-service recovery link; the sign-in plan sets the default authentication flow title to a hint, covered by the digest, with a re-plan reason for earlier sign-ins (ADR-049 amendment).

Then S337 the apps list API (`docs/slices/S337-apps-list-api.md`): `GET /api/v1/me/apps` and `GET /api/v1/people/{id}/apps`, with `homesteadctl apps mine` and `people apps PERSON` (ADR-050 §4 contents, §5); no person sessions yet.

Then S338 person sessions (`docs/slices/S338-person-sessions.md`): a person signs in through the SSO callback and gets a `person` session limited to an allowlist, deny-by-default for every other route; the `homestead-people` Authentik group and dashboard binding (ADR-050 §1–§3).

Then S339 the apps page (`docs/slices/S339-apps-page.md`): `/` is the apps page for the owner and every person, administration moves under `/admin` with redirects from the old paths, and a person never reaches or calls the owner screens (ADR-050 §4).

Then S340 app icons (`docs/slices/S340-app-icons.md`): the manifest `mdi:` icons render on the apps page and the catalog from a bundled, offline map; Jellyfin's icon is no longer a competitor's logo.
Planning (not scheduled): `docs/CATALOG_EXPANSION.md` evaluates additional
catalog applications, foundation work, and wave ordering.

Completed foundation: S341 Catalog integration contract
(`docs/slices/S341-catalog-integration-contract.md`), S342 OIDC client
actuation with supervised secret delivery
(`docs/slices/S342-oidc-client-actuation.md`), S343 GHCR update discovery
(`docs/slices/S343-ghcr-update-discovery.md`), S344 catalog bundles and
capacity (`docs/slices/S344-catalog-bundles.md`), and S341 catalog
integration metadata (`docs/slices/S341-catalog-integration-contract.md`),
which had been scheduled in a docs-only commit and was implemented before
Wave 1 so new manifests can declare their relationships.

S345 `vaultwarden` (`docs/slices/S345-vaultwarden.md`) is the first Wave 1
package: owner signup, no admin surface by default, local exposure.
S346 `paperless-ngx` (`docs/slices/S346-paperless-ngx.md`) adds the
document archive: Postgres metadata, a Valkey queue, all four image volumes
declared, supervised secret export, routed OCR configuration, and the
first real `library` integration (Nextcloud mounting the consume folder).
S347 `gitea` (`docs/slices/S347-gitea.md`) adds the private forge:
environment-only configuration with the installer locked, `ROOT_URL` from
the routed host, HTTPS-only clones, SQLite on the data volume with a
consistent export, and private-by-default views.
S348 `freshrss` (`docs/slices/S348-freshrss.md`) adds the feed reader: the
Debian image so OIDC is available, scheduled background refresh, both
writable directories on application storage as `www-data`, and an
owner-completed web installer.
S349 `linkding` (`docs/slices/S349-linkding.md`) adds the bookmark manager:
one container, SQLite on application storage, the image's
`./bootstrap.sh` named as the startup the supervisor execs, an exported
first-superuser password, and a CSRF origin that follows the routed host.
S350 `mealie` (`docs/slices/S350-mealie.md`) adds the recipe manager: the
pinned GHCR image, SQLite on the backed-up data volume, a routed base URL,
and closed signups behind the upstream-seeded administrator.
S351 `uptime-kuma` (`docs/slices/S351-uptime-kuma.md`) closes Wave 1 with
the monitoring exception: the administrator account is created in the app,
SSO is unsupported, and exposure recommends local and refuses widening.

S352 MariaDB/MySQL backup export (F2)
(`docs/slices/S352-mariadb-mysql-backup.md`) landed the server engines
BookStack needs: `mariadb` and `mysql` database kinds, dumps through
`mariadb-dump`/`mysqldump` with one consistent snapshot, password
secrets resolved for scheduled backups, and documented restore steps.

S353 `audiobookshelf` (`docs/slices/S353-audiobookshelf.md`) opens Wave 2
with the pinned GHCR image, SQLite on application storage with a
consistent export, owner media volumes for the shared library, and
optional in-app OIDC.

S354 `navidrome` (`docs/slices/S354-navidrome.md`) adds the local-auth
music server: SQLite on application storage with a consistent export, the
artwork and transcode cache on bulk, and the owner music volume shared
with Jellyfin.

S355 `komga` (`docs/slices/S355-komga.md`) adds the comic and manga
library: SQLite on application storage with a consistent export, the
comics library as a bulk owner volume, and a JVM heap cap so the server
stays inside its declared memory.

S356 `calibre-web-automated` (`docs/slices/S356-calibre-web-automated.md`)
adds the self-filing e-book library: the library and the Nextcloud-shared
ingest folder on bulk storage, the application databases on application
storage with a consistent export, and optional in-app OIDC.

S382 `craft serve and DNS advertisement validation`
(`docs/slices/S382-craft-serve-and-dns-advertisement-validation.md`) is a
validation slice with fixes, not a catalog package: it runs all 13 craft
packages through build, serve and lifecycle on a fresh appliance, then wires
the accepted S84-S86 local DNS capability into route reconciliation so
`<app>.lan` resolves from another LAN client instead of only through
`--resolve` or a host-resolver override. It closes the gap that leaves the
Craft apps start page links unresolvable, verifies removal and foreign
rewrites, and records the remaining mDNS/IPv6/DHCP limits.

Next scheduled: S382 `craft serve and DNS advertisement validation`
(`docs/SLICE_PLAN.md`). After it, S357 `vikunja` (`docs/SLICE_PLAN.md`) adds
the task manager whose CalDAV support points at Nextcloud.

The full remaining schedule is `docs/SLICE_PLAN.md`: S341 to S382 across a
catalog track, an Everything-app track and the craft validation gate, with
dependencies, the critical path and the recommended interleave.

S383 separates curated container listening ports from actual host-port requirements (`docs/slices/S383-catalog-container-port-requirements.md`).

S367 bridge planning and upstream baseline are in `docs/slices/S367-everything-bridge-and-build.md`; ADR-053 sets the constraints; S367 finished as a spike on VM 2 (plain Compose, hand-made Authentik client, pending only the owner's browser sign-in) and Homestead packaging is a later slice.

S384 adds a sealed Audiobookshelf first-run recovery administrator (`docs/slices/S384-audiobookshelf-first-run.md`); S385 will add its Authentik adapter.

S386 schedules local-address setup for normal CLI installs (`docs/slices/S386-cli-install-address-setup.md`).

S387 adds CLI install review of Authentik sign-in and people (`docs/slices/S387-cli-install-sign-in.md`).

S388 preflights install-time people selections on the daemon (`docs/slices/S388-install-people-preflight.md`).

S389 defines explicit per-person access decisions (`docs/slices/S389-install-user-access.md`); S390 will enforce them at installation (`docs/slices/S390-install-user-access.md`).

S394 makes catalog bundles emit empty lists instead of null and list their member apps (`docs/slices/S394-catalog-bundles-empty-lists.md`).

S395 keeps Authentik published on the tunnel when it moves to its path with the dashboard tunneled and sign-in linked (`docs/slices/S395-authentik-move-keeps-public-sign-in.md`).

S396 keeps Nextcloud's trusted domains in step with every name Homestead serves it on, and repairs a stopped GridCraft link on resume (`docs/slices/S396-nextcloud-trusts-its-names.md`).

S397 lets public plans and installs go ahead, with a warning, when the Cloudflare token cannot read certificates (`docs/slices/S397-publish-without-certificate-read.md`).

S398 lets apps that keep their own accounts install again, with a plain notice and no per-person choices (`docs/slices/S398-install-self-managed-apps.md`).

S399 makes publishing or unpublishing an app bring its Authentik sign-in (issuer and redirect URIs) along in the same operation (`docs/slices/S399-publish-keeps-sign-in-in-step.md`).

S400 makes app moves work for every path-served app and keeps the app's sign-in and the craft files link in step (`docs/slices/S400-moves-keep-sign-in-and-files-in-step.md`).
S401 fixes the deadlock that hung moves which write seed files but change no environment (`docs/slices/S401-seed-only-move-deadlock.md`).
S402 adds the generic `env-oidc` sign-in adapter for apps configured by environment variables; the client secret is a declared package secret (`docs/slices/S402-env-oidc-adapter.md`).
S403 turns on Authentik sign-in for Mealie through the `env-oidc` adapter and wires the declared secret into stacks installed before it (`docs/slices/S403-mealie-authentik.md`).
S404 makes env-oidc sign-in leave the owner's settings alone: placeholders render as validated, off removes only what sign-in set, replaced owner values are warned about, and Mealie's settings are checked against its docs (`docs/slices/S404-env-oidc-keeps-owner-settings.md`).
S405 makes Mealie's sign-in work on a real appliance: its secret file is owned by the image user and the appliance root is trusted only for the local issuer (`docs/slices/S405-mealie-sign-in-on-appliance.md`).
S406 makes re-applying app sign-in remove a trust variable the current issuer no longer wants (`docs/slices/S406-stale-trust-variable.md`).
S407 makes single-base-URL apps (Mealie, Vaultwarden) use their public address once published, with a previewed base-URL step in the public add/remove plan and a refresh for apps already public (`docs/slices/S407-public-base-url.md`).
S408 makes apps served by path (`/apps/<svc>/`) sign in at Authentik's public address whether or not they are published; host apps keep the ADR-047 rule (`docs/slices/S408-path-apps-public-issuer.md`).
S409 signs Linkding in through Authentik with the env-oidc adapter: endpoints built from a new `{{authentik_base}}` placeholder, the client secret exported by the existing supervisor, and the supervisor script refreshed for installs made before (`docs/slices/S409-linkding-authentik.md`).
S410 gives Linkding a combined trust bundle (the host's public CAs plus the appliance root) for a private issuer, so `REQUESTS_CA_BUNDLE` no longer cuts it off from public HTTPS sites (`docs/slices/S410-combined-trust-bundle.md`).
S411 signs Komga in through Authentik with the env-oidc adapter (Spring OAuth2 client by environment, secret exported by the supervisor, local login kept), adds a generated Java trust store (`trustbundle: java`) for the private issuer, and puts pre-supervisor installs under the supervisor when the secret is wired (`docs/slices/S411-komga-authentik.md`).
S412 signs FreshRSS in through Authentik only (owner exception to the local-login rule, ADR-052 amendment): `mod_auth_openidc` by environment with a supervised client secret and crypto key, declarative in-app commands (`onapply`/`onoff`) that switch FreshRSS between `http_auth` and form login, a system CA bundle mount for the private issuer, and plan warnings about the admin rule (`docs/slices/S412-freshrss-authentik.md`).
S413 signs Paperless-ngx in through Authentik beside its local login form: the provider JSON contains the client secret, so a new derived secret file (`secretfile`, `initial`, ADR-052 amendment) is rendered at apply time into a private file the supervisor exports, never into Compose or the override (`docs/slices/S413-paperless-authentik.md`).
S414 signs Vaultwarden in through Authentik beside its password login: the package moves from 1.34.3 to 1.37.4 (SSO needs 1.35.0 or later), sign-in is environment alone with the client secret read from a file (`SSO_CLIENT_SECRET_FILE`), and the plan warns that the master password is unchanged (`docs/slices/S414-vaultwarden-authentik.md`).
S415 signs Calibre-Web Automated in through Authentik beside its local login by writing the app's own `app.db` settings from a guarded inline script (`onapply`/`onoff`, new `restart: true` so the app re-reads them), with a pin test that forces re-verification on every CWA update (ADR-052 amendment, `docs/slices/S415-calibre-web-automated-authentik.md`).
S416 signs Gitea in through Authentik beside its local login by writing its `login_source` row from a guarded `sh` script that reads the secret file with the image's sqlite3 `readfile()` (no secret in argv), with `ROOT_URL` following `{{app_url}}`, account linking by local password, admin from the Authentik group, and a pin test that forces re-verification on every Gitea update (ADR-052 amendment, `docs/slices/S416-gitea-authentik.md`).
S417 signs Audiobookshelf in through Authentik beside its local login by writing its own `server-settings` row with a guarded `node:sqlite` script (secret read from the 0600 secret file in-process, never in argv, logs, Compose or env values; `/api/auth-settings` not used), then restarting; admin/user roles come from the groups claim (`adminvalue`/`uservalue`), a version guard and pin test force re-verification, and the app's userinfo logging at first login is owner-accepted and warned in the plan. Implements S385.
S418 adds the Authentik forward-auth engine (ADR-055) and turns it on for Navidrome through the existing `apps sign-in plan|apply [--off]`: an edge gate (identity headers stripped, outpost proxied, bypass for `/rest`, `/share`, player assets), an Authentik proxy provider joined to the embedded outpost, Navidrome trusting only the edge for `X-Authentik-Username`, a canonical-name redirect, a collision/admin refusal and a doctor outpost probe.

S420 pins the forward-auth header trust (ADR-055 §5 amendment) to the edge container's own `/32` on `homestead-edge` instead of the network subnet, which included the bridge gateway the host reaches containers from. Plans refuse when the edge is not running; a recreated edge shows as re-plan needed in sign-in status and a Doctor warning.

S419 gates Uptime Kuma with the S418 forward-auth engine for Authentik administrators only (owner and admins group, no users group), with no identity header and no database write: Kuma keeps its own login behind the gate (ADR-055 §6 amendment, 2026-11-01), the status page, badge, asset, upload and push paths are open and `/metrics`, `/dashboard`, `/status-page` and `/socket.io/` stay gated.

S357 adds the `vikunja` package (2.7.1) with Authentik sign-in through the env-oidc adapter: Vikunja reads its OpenID providers only from a config file, so the provider list is rendered at apply time into a private secret that a new secret-consumer `path` mounts read-only at `/etc/vikunja/config.yml` (long-form Compose secret, ADR-052 amendment S357). The client secret stays out of Compose, the override, argv and the environment; Vikunja has no administrator role, so none is mapped, and its local login stays.

S358 adds the `actual-budget` package (actual-server 26.10.0) with native Authentik sign-in through the env-oidc adapter: `ACTUAL_OPENID_*` by environment, the client secret as `ACTUAL_OPENID_CLIENT_SECRET_FILE`, `ACTUAL_USER_CREATION_MODE=login`, callback `/openid/callback` on a host of its own. Actual makes the first person to sign in the permanent owner and administrator and takes no group roles. A version-guarded node script confirms the switch (on) and removes Actual's stored OpenID row (off); a pin test fails on any version bump.

S359 adds the `memos` package (Memos 26.10.0, `neosmemo/memos`, port 5230, SQLite on application storage, host serving) with Authentik sign-in through the env-oidc adapter and Memos's own declarative configuration: Homestead renders one `identityProviders` JSON document (fixed `/application/o/` endpoints, `preferred_username` identifier) into a private secret mounted at `/etc/memos/config/homestead.json`, so no script and no database write is needed. Sign-in off writes an explicit `removed` entry (destroy) because Memos refuses to start when a managed provider vanishes. SSO people are ordinary users; the administrator is the first password account. A pin test fails on any version bump.

S360 writes the community-image trust policy (ADR-056, accepted 2026-11-01): publisher classes `upstream`, `linuxserver` (only Prowlarr, Sonarr, Radarr, SABnzbd, BookStack) and `community` (case by case, with a recorded justification), the per-image requirements (multi-arch, pinned tag, digest recorded in the slice, public source, maintenance, declared update source), LinuxServer specifics and provenance rules. Two optional manifest fields, `publisher` and `justification`, are validated by the schema, and `TestCuratedImageTrust` fails the build on an unclassified image. Every existing package is classified (17 upstream, calibre-web-automated community with its justification). Gates S361-S366. See `docs/slices/S360-community-image-trust-policy.md`.

S421 adds the host firewall for gated apps (ADR-055 §5 amendment): while a forward-auth gate is on, a second Homestead nftables table `inet homestead_gate` drops non-root host connections out of every Docker bridge and new routed flows into one that did not come from a bridge (published ports exempt; interface-based, IPv4 and IPv6, no subnet tracking), reconciled with the gates, at start and every five minutes, with a Doctor check and a `requireshostblock` manifest flag that makes a future auth-off app's sign-in plan refuse without it. Kuma's login stays on until S422. See `docs/slices/S421-gated-app-host-firewall.md`.
S422 turns Uptime Kuma's own login off behind the gate and the host block (ADR-055 §6 second amendment): a manifest `ownloginoff` block runs a Kuma 2.5.7-pinned script that chooses SQLite, creates one account with a random unknown password (hash passed on stdin) only on a Kuma with no user, and sets `disableAuth`; Kuma is restarted, and turning sign-in off puts the login back before the gate is removed. See `docs/slices/S422-uptime-kuma-single-sign-in.md`.
S366 adds BookStack 26.09.2 (LinuxServer image) with a MariaDB 11.8.9 service and Authentik sign-in in the same slice: secrets only as `FILE__*`/`*_FILE` files (a new `encoding: laravel-key` secret for `APP_KEY`), native OIDC by environment with exclusive sign-in, roles from the groups claim through a version- and schema-guarded script that writes the Admin and Editor roles' External Authentication IDs (ADR-052 amendment), a `bookstack` first-run routine that replaces the seeded `admin@admin.com` / `password` at install, S352 database backup plus the config volume. See `docs/slices/S366-bookstack.md`.
S382 re-scopes the craft validation gate to what later slices built: local names derive from the route table (ADR-039, S295: mDNS `.local` plus the optional `internal/localdns` `.lan` resolver), and craft apps are served by path (S320) at `homestead.lan/apps/<app>/`. S382 adds Go tests pinning that the thirteen craft path apps add no names, removal drops names and public routes never enter local answers, corrects the stale unsigned-manifest notes in DEFERRED, and carries the exact VM 2 runbook for the 13-app build/serve matrix and second-client resolution. See `docs/slices/S382-craft-serve-and-dns-advertisement-validation.md`.
S368 adds Tasks as the first Everything catalog package (ADR-057): a shared digest-pinned image recipe built on the appliance from the pinned source as `homestead/everything-tasks`, served at `/apps/tasks/`, Authentik sign-in through the normal plan with the `env-oidc` adapter, fail-closed CSP, closed by default, backed up with the bridge database. See `docs/slices/S368-tasks.md` and `docs/reviews/EVERYTHING_REVIEW_PLAN.md`.
S369 adds Projects as the second Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-projects`, served at `/apps/projects/`, with new bridge file operations (`stat`, `readdir`, recursive delete, modified stamp), a runtime-mode service worker, and no source patches; see `docs/slices/S369-projects.md`.
S370 adds Records as the third Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-records`, served at `/apps/records/`, no bridge changes, three wording patches, and `prepare_app.py` accepting an app without a service worker; see `docs/slices/S370-records.md`.
S371 adds Notes as the fourth Everything catalog package (ADR-057): the first AGPL-3.0-only app, pinned from HeyPuter/everything and built on the appliance as `homestead/everything-notes`, served at `/apps/notes/`, ten pinned patches (AI assist replaced by a clear "not available" message with no request, public links and cloud picker hidden, backup restore decoded locally), no bridge changes, and the AGPL obligations recorded in `docs/THIRD_PARTY.md`; see `docs/slices/S371-notes.md`.
S372 adds Contacts as the fifth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-contacts`, served at `/apps/contacts/`, sixteen pinned patches (relative asset base, Plausible and hosted-SDK references removed, Smart add hidden, service worker and manifest under the path), a runtime-mode service worker, and one bridge addition: `kv.list(prefix*, true)` reads key-value pairs in bounded pages so an address book longer than 1000 entries loads; see `docs/slices/S372-contacts.md`.
S373 adds Inventory as the sixth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-inventory`, served at `/apps/inventory/`, twelve pinned patches (Plausible analytics, the public handbook site and landing page, Puter wording and links removed; opaque user id kept off screen), a runtime-mode service worker, and no bridge change (product photos use the existing `fs.write/read/delete`); the image build now also fails on `plausible.io` in the built output next to `js.puter.com`; see `docs/slices/S373-inventory.md`.
S374 adds Time tracking (Hourly upstream) as the seventh Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-time-tracking`, served at `/apps/time-tracking/`, thirteen pinned wording and link patches (opaque user id kept off screen, outbound Puter links removed), a runtime-mode service worker, and no bridge changes; see `docs/slices/S374-time-tracking.md`.
S375 adds Circuits as the eighth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT) built on the appliance as `homestead/everything-circuits`, served at `/apps/circuits/`, nineteen pinned patches (Puter wording, sharing, the Puter file picker and outbound links removed; opaque user id kept off screen), one bridge addition (`fs.rename`, a same-folder move that never replaces a file), and a generic policy change: a build that ships a `.wasm` file gets `'wasm-unsafe-eval'` so the app's Rust geometry kernel can compile; see `docs/slices/S375-circuits.md`.
S376 adds Parametric as the ninth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT app; the bundled OpenCascade wasm from npm is LGPL-2.1-only) built as `homestead/everything-parametric`, served at `/apps/parametric/`, 37 pinned patches (Puter wording, sharing, outbound links removed; AI hidden and fail-closed; crash recovery moved to the per-person fs), no bridge change; see `docs/slices/S376-parametric.md`.
S377 adds 3D as the tenth Everything catalog package (ADR-057): pinned HeyPuter/everything source (AGPL-3.0-only, source offered as for Notes) built as `homestead/everything-three-d`, served at `/apps/three-d/`, 17 pinned patches (AI, Python, Report a Bug removed; relative asset base; script console refused under the policy), a Homestead entry point that moves settings to per-person bridge storage and gives IndexedDB and the browser file system a per-person name, generic `add_files` and `sdk_tag: null` in `prepare_app.py`, and the inline-WebAssembly rule in the Content-Security-Policy; no new SDK calls.
S378 adds Solitaire as the eleventh Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT; vendored Unlicense card deck) built as `homestead/everything-solitaire`, served at `/apps/solitaire/`, 11 pinned patches (Puter links and hosted-site addresses removed), a per-person localStorage entry point so statistics and saved game do not leak between people on one browser, and Homestead's own service worker (offline page). No bridge, shim or CSP change. Real-browser check and upstream tests (209) pass. See docs/slices/S378-solitaire.md.
S379 adds Sudoku as the twelfth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT; OFL font and ISC icons bundled locally) built as `homestead/everything-sudoku`, served at `/apps/sudoku/`, 8 pinned patches (Puter link removed; account label "Homestead user", sign-in and sign-out made unreachable), a per-person localStorage entry point, and a Homestead service worker added through `add_files` (offline page). No bridge, shim or CSP change. Real-browser check and upstream tests (333) pass. See docs/slices/S379-sudoku.md.
S380 adds Expenses as the thirteenth Everything catalog package (ADR-057): pinned HeyPuter/everything source (MIT; lucide icons ISC bundled locally) built as `homestead/everything-expense`, served at `/apps/expense/`, 36 pinned patches (the "AI OCR" receipt reading and every claim of it removed, any remaining path fails closed with "AI is not available on Homestead. Nothing you write is sent anywhere."; Puter wording and links removed; "Homestead user" shown; receipts limited to the bridge's 8 MB; PDF preview replaced by download), a per-person localStorage entry point, and Homestead's service worker replacing upstream's. No bridge, shim or CSP change; no new SDK calls. Real-browser check and upstream tests (79) pass. See docs/slices/S380-expense.md.

S381 (`api-client`) is deferred by the owner (2026-11-02): it needs a bridge network capability (`puter.net`) that ADR-057 does not allow. See docs/DEFERRED.md.

S423 adds the in-app source offer (ADR-057 amendment): Notes and 3D (AGPL-3.0-only) and Parametric (bundles LGPL-2.1-only replicad-opencascadejs) serve `/_homestead/source` to signed-in people, a reproducible Corresponding Source tarball for the AGPL apps, and a small "Source" link in the served page. See docs/slices/S423-everything-source-offer.md.

S425 adds the Everything bridge people registry (ADR-058): sign-in requests `openid profile`, records the person in a `people` table, `auth.getUser` returns the person's own username and a per-app uuid, an optional group check, and the offline `people purge` tool. See docs/slices/S425-everything-people-registry.md.
S426 adds the Everything bridge share grants store (ADR-058): a grants table with resolution to the owner's scope and quota, PurgePerson revoking grants, export format /2 with import, and the offline grants list|revoke tool. See docs/slices/S426-everything-share-grants-store.md.
S427 adds the automatic Everything purge (ADR-058): removing a person, or revoking an Everything app, in People runs the bridge's own `people purge` inside the app's container (docker exec); a stopped container leaves it pending in SQLite, retried every two minutes, audited as `people.purge`, and listed in `GET /api/v1/people` (`purges`). See docs/slices/S427-everything-automatic-purge.md.
S428 adds KV sharing to the Everything bridge and shim (ADR-058): `kv.share/unshare/listShared/listSharedByMe` with cursors, the kv `{handle}` option, `perms.request` and `appID`, the 60 per hour share-attempt limit and the 24 hour session recheck for recipient-side operations; off unless the bridge runs with `-sharing`, and the Tasks UI stays hidden (S437).
S429 adds FS sharing to the Everything bridge and shim (ADR-058): `fs.share/unshare/getShares/listShared`, the reserved `~shared/<id>/...` paths for read, write, stat, readdir, rename and delete through a grant, with the same switch, rate limit, 24 hour recheck and uniform errors as KV sharing. See docs/slices/S429-everything-fs-sharing.md.
S430 adds events to the Everything bridge and shim (ADR-058): a Server-Sent Events stream (`GET <base>/_homestead/events?client=<id>`), CSRF-protected `events.subscribe`/`events.unsubscribe`, `events.onLocal`/`events.on` in the shim; subjects `notif:app-user` (kv and fs share received/revoked), `kv:app-<app>:<prefix>*` and `kv:<handle>:*`, `gap` on overflow, reconnect and revoke; values never sent. See `docs/slices/S430-everything-events.md`.
S431 adds read links to the Everything bridge and shim (ADR-058): `fs.getReadURL(path, expiresIn)` mints a 32-byte token (SHA-256 at rest, 7 day default, 365 day cap, 1 minute minimum, 10 live per path and 500 per owner), `GET|HEAD <base>/_homestead/read/<token>` serves the file's current content with no sign-in, a uniform 404, sandbox CSP, `nosniff`, `no-store` and per-client/per-app rate limits on an address believed from `X-Forwarded-For` only when the peer is the edge; deleting, renaming or tree-deleting the file and People purge revoke links; `everything-bridge links list|revoke`. See `docs/slices/S431-everything-read-links.md`.
S432 adds the read-only viewer to the Everything bridge (ADR-058): `GET|HEAD <base>/_homestead/view/<token>` uses the S431 token lookup, limiter, client address and uniform 404 and renders Tasks `tasks.shared-list` snapshots and Notes `notes-shared-page` pages as a script-free page under a hashed-stylesheet policy; other files get a "no viewer" page. See [docs/slices/S432-everything-viewer.md](slices/S432-everything-viewer.md).
S433 opens an Everything app's read and view links on the public name (ADR-058) as a planned, previewed, approved and audited exposure: `apps read-links status|plan|approve` (`/api/v1/apps/{name}/read-links`) adds a `read_links` flag to the app's path route so the tunnel entry serves only `GET|HEAD <base>/_homestead/(read|view)/<token>` and answers 404 for everything else, and tells the bridge the public origin (`HOMESTEAD_EVERYTHING_PUBLIC_READ_ORIGIN`) so it mints links there and answers `read_links_not_exposed` otherwise. Dashboard panel on the app page. See `docs/slices/S433-everything-public-read-links.md`.
S434 adds the Everything net egress fence (ADR-058): `internal/everything/netfence.go` with an address classifier, a dialer pinned to the checked address with a connect-time socket check, and a redirect checker, tested with fake resolvers and dialers. Library only; the `net.fetch` operation is S435. See docs/slices/S434-everything-net-fence.md.
S435 adds the Everything `net.fetch` operation (ADR-058): a CSRF-protected same-origin data operation that sends every request through `NetFence.Client()`, with method, header and size rules, 60 a minute / 5,000 a day / 5 concurrent per person and 20 concurrent per app, a persistent `net_log` (30 days or 10,000 rows, no query strings, headers or bodies), the `everything-bridge net-log` tool, the `-net` switch (off by default) and the `puter.net.fetch` shim. See docs/slices/S435-everything-net-fetch.md.
S436 adds API Client as the fourteenth Everything catalog package (ADR-057, ADR-058): pinned HeyPuter/everything source (MIT) built as `homestead/everything-api-client`, served at `/apps/api-client/`, 60 pinned patches. Requests go through `puter.net.fetch` (the transport is forced to the relay; `scripts/everything/files/api-client-net.js` maps the fence errors to messages); the package sets `HOMESTEAD_EVERYTHING_NET=1` so the bridge starts with `-net`. Raw sockets, WebSocket, sharing and the hosted-sync wording are removed or fail with a visible message. The real-browser check proves a private address is refused (`net_failed`, `blocked_address` in the net log), a separate workspace per person and a CSP-clean boot. Details in `docs/slices/S436-api-client.md`.
S437 turns on Tasks sharing under ADR-058: the package sets `HOMESTEAD_EVERYTHING_SHARING=1`, the share and calendar-feed hiding patches are removed, invite-by-name (KV sharing), view-only links (the S432 viewer URL) and live updates by events work, Puter wording is replaced, and a three-person real-browser check covers share, live update, revoke, read link and a person without a grant. See [docs/slices/S437-tasks-sharing.md](slices/S437-tasks-sharing.md).
S438 turns on Notes read-only links under ADR-058: the Share dialog's "Create Link" is shown again (hiding patch removed), the link handed out is the S432 viewer URL (`/_homestead/view/<token>`) which renders `notes-shared-page` text and marks pictures and handwriting "not shown", `fs.getReadURL` moves to supported, AI and net stay off, the sharing switch stays off (Notes has no person-to-person sharing), and a real-browser check covers publish, viewer, republish, a person without the link, and stop sharing. See [docs/slices/S438-notes-read-links.md](slices/S438-notes-read-links.md).
S439 turns on Circuits sharing under ADR-058: the package sets `HOMESTEAD_EVERYTHING_SHARING=1`, the Share dialog and "Shared with me" are shown (hiding patches removed, wording says Homestead username), recipients are people with Circuits access, view and edit modes, edits land in the owner's file, and a three-person real-browser check covers share, view-only refusal, edit write-back, a person without a grant and revocation. Circuits has no read links, so no viewer is involved. The shim no longer reports a write that answered 404 as saved. See [docs/slices/S439-circuits-sharing.md](slices/S439-circuits-sharing.md).
S440 turns on Parametric sharing under ADR-058: the package sets `HOMESTEAD_EVERYTHING_SHARING=1`, the Share button, File > Share and the "Shared with me" list in Open are shown (hiding patches removed, wording says Homestead username), recipients are people with Parametric access, a view-only share opens as the recipient's own copy, an edit share writes to the owner's file, and a three-person real-browser check covers share, view-only, edit write-back, a person without a grant and revocation. Parametric has no read links and no viewer is involved; AI and net stay off; the LGPL kernel notice and source offer are untouched. See [docs/slices/S440-parametric-sharing.md](slices/S440-parametric-sharing.md).
S441 closes ADR-058 (docs only, no code change): ADR-057's SDK surface table now states the current truth per surface and which packages enable each (sharing switch: Tasks, Circuits, Parametric; read links: Tasks, Notes; net: `api-client`), the review plan gains one ADR-058 owner acceptance section that cross-references the S436-S440 sections, DEFERRED lists each unproven-live item once with its review-plan step, and ADR-058 is marked implementation complete. Slice file `docs/slices/S441-adr058-review-and-acceptance.md`.
S442 records ADR-059 (docs only): no catalog app may need a manual first run, except that Authentik's own first run stays with the owner; every app with a login gets single sign-on. Amends ADR-055.
S443 fixes the platform acceptance run's defects: claim no longer strands the owner between the token and the account (`setup_pending` on identity resumes the account step), an unclaimed appliance sends visitors to claim, 409/422 show the server's reason, the Authentik review keeps the typed token through approve, `storage mounts` lists each mount once, and checklist text is refreshed.
S444 creates Gitea's administrator at install (first-run routine, sealed revealable password) and closes local registration. Slice file `docs/slices/S444-gitea-first-run.md`.
S445 installs FreshRSS at install with its own CLI (default user named for the owner's Authentik username, sealed revealable password on stdin), so no web installer is shown. Slice file `docs/slices/S445-freshrss-first-run.md`.
S446 creates Memos's administrator at install (first-run routine, sealed revealable password on stdin) and has the sign-in file manage password sign-in and registration, so there is no first-account screen. Slice file `docs/slices/S446-memos-first-run.md`.
S447 makes the appliance owner the Actual Budget server owner at install (first-run routine seeds the owner in Actual's account database, verified against 26.10.0, pin-tested) and adds the missing dashboard curated install copy. Slice file `docs/slices/S447-actual-budget-owner.md`.
S448 replaces the well-known seeded logins of Mealie and Calibre-Web Automated at install (first-run routines, verified against the pinned images, generated sealed revealable passwords; Mealie's seeded email goes too, which removes its credentials banner and first-login wizard), and turns off Mealie's email-verified check that Authentik 2026.8 cannot satisfy. Slice file `docs/slices/S448-mealie-cwa-seeded-login.md`.
S449 makes a published single-origin app (Gitea, Actual Budget, Mealie, Calibre-Web Automated) redirect its LAN and .local names to its public name (ADR-060, orchestrator decision pending owner review), so sign-in from the LAN name finishes on the public name. Slice file `docs/slices/S449-single-origin-lan-redirect.md`.
S450 gives Navidrome and Uptime Kuma their administrator (first-run routines, sealed revealable password) and plans the forward-auth gate with the install (ADR-059 point 3, amends ADR-055), and Kuma's Trust Proxy is set automatically. Slice file `docs/slices/S450-navidrome-kuma-install-gate.md`.
S451 enforces that Uptime Kuma is never published: manifest field `never_publish` refused on every public-address, install and CLI path, not offered in the wizard or app page, Doctor drift check `never-publish`; its live public name was removed through the product's own path. Slice file `docs/slices/S451-uptime-kuma-never-published.md`.
S452 gives Komga and Audiobookshelf a usable default library at install on the bulk storage class (Comics; Audiobooks and Podcasts), created by their first runs with nothing to do by hand, and fixes Komga single sign-in found live (secret without client id, redirect URI, username attribute, email verification). Slice file `docs/slices/S452-komga-audiobookshelf-default-library.md`.
S453 makes Paperless-ngx usable from install: a first run creates the Homestead people group (documents, tags, UI settings) that every sign-in joins, the owner and Admin-role people become Paperless administrators through the groups claim, local sign-up is pinned closed and the local admin password is revealable. Slice file `docs/slices/S453-paperless-default-group.md`.
S454 closes Vaultwarden local registration (SIGNUPS_ALLOWED=false) while Authentik sign-in still creates each granted person's account; Authentik 2026.8 sends email_verified=false, which Vaultwarden refuses, so a new manifest field `verifiedemail` adds email_verified=true to the app's groups claim. Each person still chooses their own master password (ADR-059 exception). Verified live on VM 2: owner and person A signed in and saved an item, person C refused, registration refused.
S455 closes Vikunja local registration (VIKUNJA_SERVICE_ENABLEREGISTRATION=false) while Authentik sign-in still creates each granted person's account, fixes the Vikunja checklist note that referred to Vaultwarden, and adds a registration-closed step. Verified live on VM 2: owner and person A signed in and A created tasks, person C refused, register endpoint 404.
S456 makes turning an app's sign-in back on restore people's access: the sign-in apply adds every person with a recorded grant to the app's freshly made Authentik groups, so nobody shows as drifted. Slice file `docs/slices/S456-signin-restores-grants.md`.
S457 makes uninstall remove the app's edge routes (LAN, .local, path) and reconcile the edge, so a reinstall can publish again and no route is left behind. Slice file `docs/slices/S457-uninstall-removes-routes.md`.
S458 makes Linkding's local administrator password revealable (it was generated and sealed but 403 on reveal). Slice file `docs/slices/S458-linkding-admin-revealable.md`.
S459 turns Nextcloud's first-run welcome video off when sign-in is applied, so nobody dismisses it on their first page. Slice file `docs/slices/S459-nextcloud-no-welcome-wizard.md`.
S460 verifies and pins Immich's admin role through the `immich_role` claim (owner is an Immich administrator, people are users) and records why the per-person welcome tour cannot be skipped. Slice file `docs/slices/S460-immich-admin-role-onboarding.md`.
S461 fixes `homesteadctl people reset-mfa` (and the dashboard message), which failed to decode the daemon's device counts. Slice file `docs/slices/S461-people-reset-mfa-counts.md`.
S462 makes Doctor report an app container that was stopped by hand (it exits 0, which the probe used to excuse as a finished job). Slice file `docs/slices/S462-doctor-stopped-container.md`.
S463 sizes Docker's default address pools for a full 46-app catalog: the installer merges a small-subnet pool into daemon.json (restarting Docker only on change) and Doctor warns when it is missing. Slice file `docs/slices/S463-docker-address-pools.md`.
S464 fixes Parametric's geometry engine and API Client's pre-request scripts with per-app, manifest-declared CSP allowances (eval for that app's worker script; a separate sandbox page) while the page policy stays strict. Slice file `docs/slices/S464-everything-app-csp.md`.
S465 mints read links on the approved public name once read-link exposure is approved (local-only apps stay on the LAN name). Slice file `docs/slices/S465-read-links-approved-public-origin.md`.
S466 stops the sign-in page's "Forgot password?" link ending in a dead end: the recovery flow denies visitors without an owner-issued one-time link and tells them to ask the owner. Slice file `docs/slices/S466-recovery-flow-owner-links-only.md`.
S467 removes first-run dialogs from Everything apps (3D splash and Quick Setup; Welcome dialogs of Tasks, Time tracking, Projects, Expense, Inventory) and rewords hosted-Puter text app-wide through a shared build-time rule list. Slice file `docs/slices/S467-everything-no-first-run.md`.
S468 makes the Everything service workers serve their offline page when a navigation gets a 502/503/504 from the edge (container stopped) instead of showing a bare gateway error. Slice file `docs/slices/S468-everything-offline-page-on-stop.md`.
S469 adds the manifest field `publish` (`path`, `host` with a reason, `never`) to all 46 packages, derived from serve/allowwider/single-origin and validated, with the ADR-061 fact table as a test fixture. Slice file `docs/slices/S469-manifest-publish-field.md`.
S470 hardens the shared origin at the public edge: __Host- dashboard cookies over TLS, the edge strips dashboard and Authentik cookies from app requests and reserved Set-Cookie names from responses, and path routes get frame-ancestors 'self' and Referrer-Policy same-origin. Slice file `docs/slices/S470-shared-origin-hardening.md`.
S471 extends the existing move so an installed path-package app on its own public name moves to its subpath on the dashboard's public name in one digest-bound change: the preview says old links stop working and nothing redirects, the old CNAME, tunnel entry and route are removed last, `publish` replaces `never_publish`, a Path=/ cookie warning and a Doctor `subpath-publish` check are added. Slice file `docs/slices/S471-move-plan-subdomain-to-subpath.md`.
S479 makes the public-name move safe, ahead of S472: the new subpath address is requested through the public edge before the old name is removed (the run stops and keeps the name if it does not answer), a move that stopped halfway shows as unfinished and is finished through the same planned, digest-approved flow, Doctor `public-name-plan` flags public names no route carries, and the two audit-timing tests are deterministic (the audit waiter is awaited, not raced). Slice file `docs/slices/S479-public-name-move-safety.md`.
S472 moves Navidrome to `/apps/navidrome/` (`publish: path`, `ND_BASEURL`): the forward-auth gate renders inside the dashboard site with the bypass regexes prefixed, the public outpost path is added, one canonical name (public if published, else the dashboard Host) with other names redirecting, and the move and sign-in steps carry an existing gate to the path. Forward only. Details in `docs/slices/S472-navidrome-at-subpath.md`.
S473 checks Audiobookshelf at `/apps/audiobookshelf/` and keeps it on its own name (`publish: host`, `no_subpath`): `ROUTER_BASE_PATH` moves the server routes but the web client has `/audiobookshelf/_nuxt` compiled into its assets, so the page never loads under another prefix; evidence, the VM 2 restore and the pin test are in `docs/slices/S473-audiobookshelf-subpath-check.md`.
S474 moves Komga to `/apps/komga/` (`publish: path`, `SERVER_SERVLET_CONTEXT_PATH`): trialled in a throwaway container first, the web client follows the context path; its Authentik OIDC redirect and sign-in URL move with it, first run uses the prefix, live on VM 2 through `apps move`.
S475 moves Paperless-ngx to `/apps/paperless-ngx/` (`publish: path`, `PAPERLESS_FORCE_SCRIPT_NAME` and `PAPERLESS_STATIC_URL`): trialled in a throwaway container first, the Angular client and static files follow the prefix; the allauth redirect URI and sign-in URL move with it, live on VM 2 through `apps move`. Details in `docs/slices/S475-paperless-at-subpath.md`.
S476 moves Linkding to `/apps/linkding/` (`publish: path`, `LD_CONTEXT_PATH`): trialled in a throwaway container first, the UI, static files and admin follow the prefix; its OIDC redirect and sign-in URL move with it, live on VM 2 through `apps move`. Details in `docs/slices/S476-linkding-at-subpath.md`.
S477 checks FreshRSS at `/apps/freshrss/` and keeps it on its own name (`publish: host`, `no_subpath`): the image's Apache config hard-codes the OIDC redirect path `/i/oidc/` and mod_auth_openidc ignores `X-Forwarded-Prefix`; evidence and the pin test are in `docs/slices/S477-freshrss-subpath-check.md`. VM 2 untouched.
S480 puts every gated app at a path behind one shared Authentik proxy provider on the dashboard's name (ADR-062): the edge admits each app's groups by exact match on the outpost's groups header and answers 403 to the rest, client identity headers are deleted first, and Navidrome moves onto the gate through a re-planned sign-in (its own provider is retired first). Live on VM 2 (owner and A in, C 403, Subsonic bypass, forged headers ignored). Details in `docs/slices/S480-shared-subpath-gate.md`.
S481 publishes the 13 craft apps at `/apps/<app>/` behind the shared Authentik gate (`publish: path`, `sso.forwardauth`, ADR-062 section 6): the gate is tunnel-only so the LAN stays open (owner decision), a published craft route without its gate answers 404, and only members of each app's groups pass on the public name. Live on VM 2. Details in `docs/slices/S481-craft-apps-at-subpath.md`.
S482 is the live acceptance of the subpath work on VM 2 (no code change): subpath apps and craft apps load and sign in through the public name, old subdomains are gone from DNS and ingress, cookies are isolated, no pending moves, Doctor live has 0 fails; one finding (FreshRSS owner sign-in, DEFERRED). Evidence in `docs/slices/S482-subpath-live-acceptance.md`.
S483 adds the dashboard surface for configuration export and import on the Configuration page: a YAML download (`?format=yaml`, names only so no re-auth), a read-only import preview (`POST /api/v1/config/import/preview`, also `config import --dry-run`), password-field secrets sent once with an explicit confirm, and a `config.import` audit entry; see `docs/slices/S483-config-export-import-dashboard.md`.
S484 lists the recorded TLS issuance approvals and the local CA leaf certificates on the Certificates page, using the existing `tlsIssuanceApprovals` and `localCaLeaves` wrappers (no daemon change); see `docs/slices/S484-tls-read-surfaces.md`.
S485 shows the daemon version from `GET /version` (new `daemonVersion` wrapper) on the Status page, the same value as `homesteadctl version`; no daemon change; see `docs/slices/S485-version-on-dashboard.md`.
S487 adds a Version input to craft plan and build and a Craft signature file input to update rows on the Updates page, closing flag gaps G6 and G7 (CLI `craft plan|build --version`, `updates apply --craft-signature`); dashboard only, no daemon change; see `docs/slices/S487-craft-version-signature.md`.
S488 records that `apps public plan|apply --route` has no dashboard field because no catalog app has more than one edge route (every manifest exposes exactly one service); docs and tests only, no UI; see `docs/slices/S488-public-route-not-needed.md`.
S489 offers `--generate` when recreating a missing person: a second Recreate button on the People page sends `method: generated` and shows the password once in the reveal dialog; closes flag gap G9; dashboard only; see `docs/slices/S489-recreate-generated-password.md`.
S486 tightens the parity test: spec files no longer count, a CLI row with a dashboard `n/a` fails (only `admins/recover` is exempt), and `docs/PARITY_FLAGS.md` maps every `homesteadctl` flag to a dashboard field or an allowed `n/a` reason, checked against the Go sources; tests and docs only; see `docs/slices/S486-tighten-parity-test.md`.
S491 records that FigCraft, SoundCraft and DesignCraft web builds have no working document save or export (owner: upstream gap, no shims); issue drafts under `docs/upstream/`; docs and tests only; see `docs/slices/S491-craft-saves-upstream.md`.

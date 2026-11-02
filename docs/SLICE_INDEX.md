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

# Deferred Features Ledger

- Custom Homestead ISO / installer media
- Disk partitioning and formatting
- RAID/ZFS/Btrfs creation and management
- Clustered/multi-node Homestead
- Fleet management
- Community/third-party catalog
- Automatic domain purchasing
- Broad router automation beyond validated adapters
- Hosted HAI coordination/control service
- Multi-appliance dashboard
- Live storage migration between devices
- Connector binary installation: S246 setup refuses with
  instructions when cloudflared is absent; packaging or supervising
  the binary itself belongs to a later slice.
- Pre-S246 tunnel adoption: tunnels created before the routes table
  existed serve fine but read as unrecorded; adopting them into
  management belongs to S247.
- AdGuard Home install-wizard automation (`POST /control/install/configure`):
  needed before friendly names (S086) work end-to-end against a fresh
  instance; kept out of S085 as version-brittle and unverifiable without
  a live server.
- Route 53 STS session-token flow: the S094 adapter takes static IAM keys
  only; short-lived credentials need a refresh flow no slice owns yet.
- Overlay-view DNS application: S098 computes overlay effective tables,
  but no overlay DNS provider exists to receive them; overlay names are
  served through local DNS until a provider lands.
- Internal CA distribution: closed by S280 (ADR-030). The CA is persisted,
  leaves are issued through the edge, the root downloads with a verified
  fingerprint, and the per-OS runbook is docs/architecture/INTERNAL_CA_TRUST.md.
  Still out of scope: MDM or automatic device enrolment (installing the root
  stays manual by owner decision); an ACME server for local names; DANE or
  Certificate Transparency for local names; trust-store injection for native
  apps (they need their own handling); name constraints follow the fixed local
  suffix set, not an owner-chosen suffix.
- Certificate issuance gaps left by S278: only ACME dns-01 issuance has an
  owner-approved flow; http-01/tls-alpn-01 stay Caddy-native and are not
  fingerprinted. The internal CA the baseline (S222) mints is now
  persisted (S280). Placed certificates are served only by automatic-TLS routes
  whose host they cover; applying the issuance to a route that does not
  exist yet needs a reconcile afterwards. Manual-provider DNS-01 (owner
  pastes TXT values) has no issuance flow. S272 re-issues placed dns-01 certificates unattended. A reload refused after a successful
  issuance leaves the certificate placed and recorded and requires
  re-running the apply to retry the reload (a reload-only action is not
  provided).
- Exit-node operation: the S110 model refuses default-route
  advertisement, so serving as an exit node needs its own slice with
  explicit owner consent.
- Headscale server-side issuance: S113 enrols with owner-pasted
  pre-auth keys; minting keys through the server API (no out-of-band
  CLI step) needs a Headscale API client slice.
- Headscale relay exposure: the S114 profile publishes the control
  port only; embedded DERP/STUN ports for NAT-traversed relay stay
  undeployed until remote nodes need them.
- Tailscale admin-side DNS: MagicDNS names, search, and nameservers for
  Tailscale tailnets stay admin-console owned; no API-key flow manages
  them from Homestead.
- Public accept-all firewall rules: the S053 expander errors on
  zone-wide public/overlay accepts, so serving the public internet
  needs an explicit accept-all shape before S227 applies planner
  output.
- Container device passthrough: S162 detects GPU stacks, but compose
  generation has no devices/runtime fields, so installs cannot pass
  GPUs through yet; planning the fragment belongs with generation.
- ARM SoC transcoding: S162 covers Intel, AMD, and NVIDIA only;
  Rockchip and other SoC bindings need per-board validation.
- Backup restore follow-ups (S275): the runbook
  (`docs/architecture/BACKUP_RESTORE_RUNBOOK.md`) restores with plain
  `age`; restore execution is still package-level only (no API route),
  and SSH-key recipients are not accepted (ADR-022 names X25519 only).
- App data on anonymous volumes: KVM validation (2026-10-12) found
  generated catalog stacks render no `volumes:`, so app data lives in
  anonymous Docker volumes rather than mounted storage, and scheduled
  backups find no sources. Planned as S276 (declared placement for new
  installs) and S277 (migration of existing installs plus a backup
  source guard).
- ghcr registry checks: S190 discovers Docker Hub tags only; ghcr
  listings need a token flow before Immich updates discover
  upstream.
- Platform release acquisition: S194 models desired versions only;
  fetching releases belongs with distribution work (S264 verifies
  owner-supplied local files but fetches nothing; S265 applies them).
- Platform apply follow-ups (S265): the plan still says a state backup
  precedes platform moves, but no daemon-state (SQLite/YAML) backup
  exists yet, so apply does not run one (back up with the documented
  backup tooling first); only one previous binary is kept per
  component, so applying a second time after a failed health check
  overwrites the last good previous; the
  apply unit trusts the trusted keys and baseline stored in the database,
  so a compromised daemon could still alter trust.
- Platform rollback follow-ups (S273): a rollback keeps the binary it
  replaced only as the last two `<binary>.rolledback-<stamp>` copies and
  does not make it the new "previous", so there is no one-step
  roll-forward.
- Production release signing key: the HAI public key embedded by S264
  (`internal/release.EmbeddedPublicKey`) is a labelled placeholder
  whose private half was discarded. The real key, signing ceremony,
  and release tooling that publishes `version=`/`counter=` checksums
  and `.minisig` files are pending; owners may import a key by
  configuration meanwhile.
- Identity provider adapters: S202 defines the integration contract
  only; driving a provider's API (Authentik first, beyond the
  connection check) belongs to a later slice.
- SSO client actuation: S204 records reviewed integration intent,
  but writing provider credentials into application configuration
  has no owning slice yet.
- Dockge route reconciliation: S211 models and links the local-only
  route, but no flow writes it into the proxy yet; the direct UI
  port serves until then.
- Live drift refinements: S262 compares images, published ports,
  networks, and mounts; it does not compare environment, command,
  healthchecks, resource limits, tmpfs sizes, compose `build:`
  services, interpolated values, `extends:`, or `include:`. A fully
  stopped stack reads as a state, not drift, because Homestead persists
  no per-stack desired run state; an accidentally stopped stack is
  indistinguishable until that state exists.
- ~~Theme selection~~ closed by S282: themes follow `prefers-color-scheme`,
  with a persisted manual choice (system, light, dark, high contrast) on the
  Account page.
- Visual regression automation: the validation board is checked
  structurally in unit tests; screenshot comparison and an automated
  375px overflow check need a browser harness.
- Docker Engine bootstrap: S220 installs the Homestead kernel
  only; engine installation on clean machines (S071 owns the
  operation) has no bootstrap wiring yet.
- Remote bootstrap source: no canonical repository URL exists,
  so bootstrap runs from a checkout; curl-pipe installation
  waits on distribution work.
- mDNS coexistence and probing: S221 answers while the port is
  free and warns otherwise; sharing 5353 with Avahi, name-conflict
  probing, IPv6 advertisements, and scoping answers away from
  container bridge addresses belong to later slices.
- Management TLS provisioning: S280 (ADR-030) persisted the internal
  authority and serves local-only edge hosts from it. Serving management TLS
  from the daemon itself did not fall out of the edge work and remains
  deferred; management stays on the local/overlay listener over HTTP.
- Domain onboarding surface: S223 gates, proves, and records in
  code only; API, CLI, and dashboard affordances for connecting
  a domain belong to later slices.
- Split-DNS surfaces: S224 establishes and records in code
  only; API, CLI, and dashboard affordances belong to later
  slices.
- Overlay view application: S225 reviews validated overlay
  intent, but no overlay DNS provider exists to receive it;
  applying overlay answers belongs to a later slice.
- Certificate issuance: S226 decides and records strategy only;
  fetching certificates (ACME orders, DNS-01 TXT orchestration,
  manual material intake) belongs to later slices.
- Exposure apply surfaces: S227 approves and applies in code
  only; S245 adds API and CLI review, approval, and apply,
  leaving the dashboard affordance for a later slice.
- Validation surfaces and external vantage: S228 validates from
  the appliance with caller-named management hosts; scheduled
  re-validation, status surfaces, and best-vantage external
  probing belong to later slices.
- Backup runs endpoint: scheduled runs fire (S260) and show as
  operations, but there is no endpoint listing retained run
  directories per package, so S267 shows destination, schedules, and
  scheduler state only, not a runs list.
- API casing consistency: backup schedules are snake_case JSON, but
  other responses (for example ExposurePlan and UpdatePlan) still
  serialize Go field names in PascalCase. Normalizing them needs a
  coordinated API, CLI, and dashboard change.
- Backup-run restoration: S234 rebuilds from export documents
  plus suite reinstalls; restoring application data from backup
  runs awaits the same backup surfaces.
- Corrupt-database repair: S230 snapshots state and refuses a
  missing database as disaster recovery; repairing corrupt
  state belongs to the disaster-recovery flow (S234).
- Edge live status: the edge routes read back the table the reconciler
  last wrote, not what Caddy is serving, and there is no Caddy runtime
  status route. Reconcile ownership starts at S270: snippets a
  pre-S270 reconcile wrote are not in the record, so reconcile never
  removes them (delete them by hand).
- Dashboard embed in install flow: serving the UI needs the
  node toolchain plus a manual embed build (proven working in
  KVM); prebuilt packaging of the daemon and CLI belongs to distribution
  (S240). Craft images are not part of it: they are built on the appliance
  from signed manifests (S279, ADR-028) and no image is ever published.
- Session boundary follow-ups (S255, S257): rejection audit entries are
  always actor `anonymous` (per-actor attribution of rejected requests
  is unscheduled), and an open SSE stream re-verifies its session only on
  each heartbeat (30s), so a revoked session can finish up to one
  interval of streaming; a push-style revocation signal is unscheduled.
- Session transport follow-ups (S256): CSRF, cookie, and idle/absolute
  rules are enforced since S257. Not done: an `Origin` /
  `Sec-Fetch-Site` check as a second CSRF layer; a "list / revoke my
  sessions" surface (CLI and dashboard); a CLI `login --no-store` flag and
  OS keyring storage for the CLI token; an admin-visible session expiry
  warning in the dashboard; trusted-proxy configuration for
  `X-Forwarded-Proto` (today any client may claim https, which only
  hardens the cookie); and a `Secure`-cookie caveat for plain-HTTP LAN
  access: browsers accept `Secure` cookies only over HTTPS or localhost,
  so TLS-less LAN dashboards rely on the cookie being non-Secure there.
- Operation cancel route: `Runner.Cancel` exists but no API, CLI, or
  dashboard surface calls it, so a hung operation that blocks a
  scheduled job (ADR-023) can only be cleared by restarting the daemon.
- Package backups spanning mounts (S276): runs are now laid out per
  storage class, but one mount assigned several classes is still copied
  once under its first class, and two assigned mounts of the same class
  holding `<package>` would still share a destination and be refused
  with a clear error (the assignment model allows one mount per class,
  so this only arises from hand-built sources).
- Catalog data placement follow-ups (S276): every persistent image path
  of the four first-party apps lives on mounted storage; none needed an
  exception. Not done: Immich's Valkey `/data` and any similar queue
  state is ephemeral (tmpfs) by decision, so a restart drops queued jobs;
  `backuppaths` in manifests is still informational (backups copy the
  whole `<mount>/<package>` tree, including Jellyfin's bulk `cache` and
  Immich's `model-cache`). Database directories (`database: true`) of a
  package with a declared export (Authentik) are now excluded from the
  file copy, which keeps only the empty directory, and the run records
  that; where no export is declared (Immich, Nextcloud's SQLite in
  `data/` beside user files) they are still copied, files that vanish
  mid-copy are skipped and counted in the run warnings, and the run says
  the copy is not crash-consistent. A consistent Immich Postgres and
  Nextcloud SQLite export is still undeclared;
  restore has no CLI/API surface yet (S276 KVM restored a run through
  the `internal/backup` plan and definition directly). (S266 resolved
  the Immich database environment and credentials.)
- App data migration follow-ups (S277): migration is per app and
  host-side. Not done: data that sits in a container's writable layer
  (a declared path with no mount at all, for example Authentik's
  `/media`, `/templates`, `/certs` on a pre-S276 stack) is not carried
  over, because no volume holds it; the plan says so and the new bind
  starts empty (copying with `docker cp` is possible later); two services
  that each hold their own volume for one shared declared path are
  refused rather than merged; the pre-migration backup reuses the file
  backup machinery (in-tree symlinks and ownership preserved since the
  S276 KVM fixes, links escaping the tree still refused), and it is a
  safety copy for the owner, not the rollback path (rollback uses the
  untouched old volumes); the backup guard does not
  judge read-only binds or owner-supplied extra read-write binds outside
  the package directory; progress and downtime figures are estimates
  from a fixed copy throughput; directories moved aside by a rollback
  (`<target>.rollback-<stamp>`) are never cleaned up automatically; the
  package claim that serialises migration against configure, remove,
  drift restore, and update is checked when those operations are built,
  not inside their steps, so an operation built just before the claim
  could still interleave; old volumes the migration did not record
  (for example volumes of apps removed earlier) are not managed here.
- Secret-backed variables follow-ups (S266): adoption needs the stack's
  compose services to use mapping-form `environment:` (generated stacks
  do; hand-edited list form is refused with a pointer). Non-database
  secrets are only captured from a running container's environment or
  registered. Drift restore still cannot recreate a missing secret file
  (it now reports it; configure re-materializes). The startup
  reconciliation retries for about four minutes, then leaves the pending
  row for the next daemon start. Nextcloud and
  Jellyfin declare no secrets.
- Storage unregister and unassign (closed in S288: routes, CLI and Storage page
  actions exist; impact is judged by `<mount>/<package>` directories, as below).
  Re-assigning a class is refused with a 409 package list
  until acknowledged; impact is judged by `<old mount>/<package>`
  directories of installed stacks and, when the catalog is available, by the
  storage classes installed apps' manifests require (S288 review fix); an
  unreadable disk refuses with a 409. S276 per-path declarations are still
  not consulted.
- Audit server-side query (S269): `GET /api/v1/audit` accepts only
  `limit` (max 1000), so the dashboard Audit page filters (actor, action,
  outcome, time range) and pages client-side over the loaded window, and
  entries older than that window are unreachable from the dashboard and
  CLI. Server-side filters, a cursor, and audit-list CLI
  filter flags belong to one coordinated API, CLI, and dashboard change.
- Unattended re-issue gaps (S272): http-01 and tls-alpn-01 ACME
  certificates stay Caddy-native and are never re-issued by Homestead. A
  re-issue whose edge reload keeps failing is retried (reload only) with the
  same backoff but has no escalation beyond status and doctor warnings.
  Retry-After is bounded at 7 days. The CA terms URL is recorded when an
  account is registered, not at approval (approval contacts no CA); accounts
  registered before this change have none recorded and show no "changed"
  flag. Re-issue refuses, rather than adapts to,
  any drift of CA directory, zone, names, provider, or challenge; the owner
  re-approves. Real Let's Encrypt staging was not exercised.

## Craft build pipeline (S279, ADR-028)

- Production craft signing key: closed 2026-10-17. The binary embeds the
  HAI craft key `B03DB3F5AA66DB3E` (published as `catalog/craft/craft.pub`;
  private half held offline by HAI). Still open: an offline, multi-person
  key ceremony and a documented rotation drill; the platform release key
  remains a placeholder.
- storytold release source: recorded 2026-10-15 as
  `https://github.com/storytold` (ADR-028). S249 added the first craft
  package (WordCraft), its unsigned manifest and the catalog validation of
  craft tags; S250 to S253 add the other apps.
- CI automation of manifest generation and signing: `scripts/craft-manifest.sh`
  and `scripts/sign-craft-manifest.sh` are run by hand on the signer's
  machine.
- Read-only root filesystem: the image writes only under `/tmp`, so a
  package can declare `/tmp` as its one ephemeral tmpfs, but the catalog
  schema has no read-only-root field.
- Multi-arch builds: the base is pinned by its multi-arch index digest and
  the appliance builds for its own architecture; there is no per-arch
  digest record or cross-build.
- Build caching across updates: every build runs from scratch against the
  fresh context; there is no layer cache reuse policy beyond Docker's own.
- Doctor and scheduler: no doctor check reports a placeholder-only craft
  key set or a recorded image that no longer matches (drift and
  `craft status` report it), and no scheduled re-verification exists.
- Counter baselines are per app and move when a build is recorded, not
  when the update applies; a failed update after a successful build leaves
  the baseline advanced (an older manifest then needs the audited
  override).
- Pruning only removes the previous version's tag after a successful
  update; an image a container still uses, or a version record orphaned by
  uninstalling the package, stays until the next update or a manual
  `docker rmi`.
- Update apply takes one craft move per package; a package mixing several
  locally built images is refused.

- S280 review follow-up: a first-class CLI/API action to discard an orphaned
  internal CA key (today documented as a manual `DELETE FROM secrets`), and
  automatic retry of old-generation CA key destruction.

## Static craft packages (S249, ADR-028)

- Owner signature: `catalog/craft/craft-manifest.yaml` is committed
  unsigned; no `.minisig` exists until the owner generates the craft key
  offline and signs it (`docs/architecture/CRAFT_SIGNING_RUNBOOK.md`). Until
  then WordCraft cannot be built or installed outside a throwaway-key test.
- Read-only root filesystem: still no catalog schema field. The craft image
  runs with a read-only root and `/tmp` tmpfs in tests, but Compose output
  does not set `read_only: true`, `cap_drop` or `no-new-privileges` for it.
- Craft apps' document storage: documents live in the browser and download
  to the device until the Nextcloud WebDAV wiring (S254, ADR-029); widened
  exposure stays discouraged until then.
- Runtime craft listing: catalog validation checks the tag shape and
  version; "listed in the signed craft manifest" is enforced at install and
  start by the recorded-build gate (a build exists only from a verified
  manifest) and statically by the shared test over the shipped manifest.
  There is no catalog-load check against the manifest the daemon last
  verified, because an unsigned or absent manifest must not hide the whole
  catalog.
- `requirements.ports` serves two jobs: the dashboard's default container
  port and the host-port conflict check. Craft packages declare no
  `requirements.ports` (the container publishes no host port and the daemon
  itself listens on 8080, so declaring it refused every install); the
  dashboard defaults a `homestead/craft-` service to container port 8080
  instead. The fields should still be split so a container port has its own
  manifest field.
- Edge network aliases: every service in a routed stack joins
  `homestead-edge` and routes target the bare service name, so two stacks
  using the same service name (for example `web`, `db`) would share an alias
  on that network. Craft packages name their one service after the app; a
  multi-service stack should attach only its exposed service to the edge or
  route by container name. The planner now joins only routed services to an
  edge network it adds itself; an edge entry supplied by the caller (the
  dashboard) still attaches to every service.
- Scheduler surface: a stateless package has no scheduler job, so its
  "no-op" on `GET /api/v1/scheduler/jobs` is absence; the explicit
  `stateless` marker is on the backup schedule routes, `backup schedules`
  and the Backups page. The unwired backup wizard
  (`internal/backup/wizard.go`) does not check statelessness.
- Header checks beyond WordCraft: COOP/COEP, `.part-*` files and any
  Brotli (`ngx_brotli` is not in the base image) are verified per app in
  S250 to S253; the nginx config has no per-app include yet.
- The shared nginx serves `README.md`, `HOSTING.md`, the sample `_headers`
  and `.htaccess` from the upstream archive unchanged.
- S249 KVM re-verification leftover: an update whose craft image was swapped
  for a no-op apply (package already current) still fails at `rewrite` with a
  message that names an image ID rather than the tag; cosmetic.

## Office craft packages (S250)

- Owner signature: the four-app craft manifest (counter 2) is unsigned;
  GridCraft, PdfCraft and DeckCraft cannot be built outside a throwaway-key
  test until the owner signs it.
- KVM acceptance for the three apps is pending (see
  `docs/DEVELOPMENT.md`); the `knownImageVolumes` rows and header-table
  rows are marked pending until then.
- First-load size: the uncompressed wasm is 18.9 MB (GridCraft), 38.1 MB
  (PdfCraft) and 25.5 MB (DeckCraft), served gzip at about 37 percent;
  Brotli would shrink it further but `ngx_brotli` is not in the base image.
- Upstream MPL-2.0 code dependency in DeckCraft (symphonia, unmodified) is
  recorded in `docs/THIRD_PARTY.md`; a future change that patches it needs
  a source-availability review.

## Graphics craft packages (S251)

- Owner signature: the eight-app craft manifest (counter 3) is unsigned; the
  four graphics apps cannot be built outside a throwaway-key test until the
  owner signs it. A KVM appliance that already holds a baseline at counter 3
  or higher from throwaway manifests (the S249 VM held 4) refuses this
  manifest; use a fresh appliance or a higher throwaway counter there.
- KVM acceptance for the four apps is pending (see `docs/DEVELOPMENT.md`);
  the `knownImageVolumes` rows and header-table rows are marked pending until
  then.
- VectorCraft moved to v0.12.0 between planning (v0.11.0) and S251; a bump
  after this commit needs the usual manifest regeneration and re-signing.
- LightCraft keeps its library in browser storage (OPFS) and is marked
  experimental upstream; it uses module Web Workers and states a future
  wasm-threads build will need COOP/COEP. Re-check `SharedArrayBuffer` on
  every LightCraft bump; adding COOP/COEP for one app would need per-package
  nginx configuration, which is an architectural change (ADR first).
- LightCraft ships `.br` copies and the Chinese font `.otf` (served
  `application/octet-stream`, `.gz` only); the `.br` files are inert without
  `ngx_brotli`, and a font MIME type is not needed for `fetch()`.
- FigCraft's web build joins desktop-hosted live sessions over `ws://`, which
  browsers block from the HTTPS route; sessions with other machines need the
  upstream standalone server (`figcraft-server-<version>-*.tar.gz`, not
  packaged here) plus `wss://`.
- First-load size: uncompressed wasm is 64.7 MB (VectorCraft, 22.5 MB
  gzip), 25.0 MB (PhotoCraft), 27.3 MB (LightCraft, plus a 16.4 MB font) and
  22.5 MB (FigCraft); Brotli is not available in the base image.
- Client-side capability advisory (WebGPU/WebGL2/secure context) is text in
  the package descriptions only; the S252 mechanism is not a dependency.
  S252 added the `browser` manifest field, but the four graphics manifests
  do not declare it yet (see the S252 section).

## Media craft packages (S252)

- Hard install-time refusals on browser capabilities stay deferred (owner
  decision 2026-10-14): the appliance cannot see the browser, so S252 shows
  non-blocking advisories only (`browser` manifest field, install plan
  `BrowserAdvisories`, dashboard panel, CLI `browser:` lines). Nothing in
  the daemon refuses install, update or start on browser grounds.
- The `browser` field is declared for filmcraft, effectcraft and soundcraft
  only. The six earlier craft manifests (wordcraft, gridcraft, pdfcraft,
  deckcraft and the four S251 graphics apps) still carry the WebGPU/WebGL2
  note as description text; adding structured `browser` entries to them is a
  catalog-only follow-up.
- Owner signature: the eleven-app craft manifest (counter 4) is unsigned;
  FilmCraft, EffectCraft and SoundCraft cannot be built outside a
  throwaway-key test until the owner signs it. A KVM appliance holding a
  baseline at counter 4 or higher from throwaway manifests refuses it.
- KVM acceptance for the three apps is pending (see `docs/DEVELOPMENT.md`);
  the `knownImageVolumes` rows and header-table rows are marked pending.
- None of the three needs `SharedArrayBuffer`, COOP or COEP (upstream
  builds are single-threaded; their `HOSTING.md` says so). Upstream's local
  dev servers do send COOP/COEP and EffectCraft's service-worker comments
  say the page is "cross-origin isolated", but nothing in the shipped JS
  reads or needs it. A future threaded build (wasm atomics, `rayon`) would
  need per-package COOP/COEP in the shared nginx config and an ADR first;
  re-check `SharedArrayBuffer` and `crossOriginIsolated` on every bump. The
  S254 same-origin proxy design is unaffected today.
- FilmCraft loads its unhashed `filmcraft_web.js` and `_bg.wasm` with a
  `?v=<build hash>` query; upstream's `_headers` marks them `immutable`, but
  the shared nginx keys on the file name only, so they are served
  `no-cache` (revalidated with an ETag, 304 when unchanged). Correct, just
  not cached for a year. A query-aware rule would change the shared image
  and the build-context digest; not done.
- EffectCraft's `manifest.webmanifest` is served as
  `application/octet-stream` (the base `mime.types` lacks the extension).
  Browsers parse manifests regardless of type, so install-as-app is not
  expected to be affected; unverified in a browser. Fixing it is a one-line
  shared nginx change plus a manifest re-sign.
- FilmCraft and EffectCraft web zips ship no `NOTICE` and no OFL texts, but
  their NOTICE lists SIL OFL 1.1 fonts embedded in the wasm (FilmCraft
  also craft-fonts families). The built image therefore carries the MIT and
  Apache texts only. `docs/THIRD_PARTY.md` keeps the NOTICE verbatim; ask
  upstream to include NOTICE and `OFL-*.txt` in the web zip (FigCraft's
  already does), as Homestead does not alter vendored files.
- Heavy workloads: wasm is 41.2 MB (FilmCraft), 62.5 MB (EffectCraft) and
  15.5 MB (SoundCraft), served gzip at 13.3, 18.7 and 5.9 MB. Large media
  lives in the browser (memory, OPFS or IndexedDB) until S254; no
  server-side limit is introduced.
- SoundCraft audio recording (microphone) was not found in the shipped JS
  or wasm strings (no `getUserMedia`); not declared as a browser capability.
  Re-check on bumps.

## Design and CAD craft packages (S253)

- **Craft apps start page (landed, S281; closed).** Craft Launcher was dropped
  from the catalog on 2026-10-17 by owner decision (compiled-in upstream suite
  list, GitHub download buttons, 14 `api.github.com` calls per load). The
  Homestead-owned replacement is `GET /api/v1/craft/apps`, the CLI `craft apps`
  command and the dashboard Craft apps page (ADR-031 option b);
  see `docs/architecture/STATIC_CRAFT_PACKAGES.md`. Left out of S281: it is
  not a standalone page for family members without a Homestead session
  (ADR-031 cost of option b), it shows `installed` rather than live container
  health (the Status and Apps pages carry that), and it does not show whether
  Nextcloud files are available for a hooked app (the Craft files page does).
- DesignCraft and CADCraft web zips ship no `NOTICE` and no `OFL-*.txt`,
  though DesignCraft's NOTICE lists SIL OFL 1.1 fonts and other
  third-party material embedded in the wasm and CADCraft's NOTICE discloses
  the MPL-2.0 `acadrust` crate. The built images carry the MIT and Apache
  texts only;
  `docs/THIRD_PARTY.md` records the licences. Ask upstream to include NOTICE
  and the OFL texts in the web zips.
- Owner signature: the thirteen-app craft manifest (counter 7 since S254) is unsigned;
  DesignCraft and CADCraft cannot be built outside a throwaway-key test until
  the owner signs it. A KVM appliance holding a baseline at counter 7 or
  higher from throwaway manifests refuses it.
- KVM acceptance for both apps passed on 2026-10-17 (see `docs/DEVELOPMENT.md`).
- No `SharedArrayBuffer`, COOP or COEP is needed by either
  (upstream `HOSTING.md`, the shipped JS and wasm strings); re-check on
  bumps. CADCraft is marked "early development" upstream and DesignCraft
  and CADCraft keep only preferences in the browser; documents and drawings
  live in the browser or are downloaded until S254.

## Nextcloud craft file proxy (S254)
- Nextcloud OAuth instead of app passwords: the link uses one app password
  for one account (ADR-029); OAuth would put a bearer token in the browser or
  need CORS on Nextcloud.
- Per-user (not single-account) Nextcloud mapping: every operator session
  reaches the one linked account and root folder.
- File locking and conflict handling beyond last-writer-wins with ETag
  checks (`If-Match` on overwrite, a confirmation on 412); no merge, no
  WebDAV locks, no version picker.
- Apps without a file hook: WordCraft and the seven others (deckcraft,
  vectorcraft, photocraft, lightcraft, figcraft, soundcraft, designcraft)
  stay browser-local. Ask upstream for `?host=parent` style hooks.
- Save for FilmCraft and EffectCraft: they are open-only (URL hooks); saves
  stay in the browser and need a manual export.
- WordCraft `showSaveFilePicker` patching: rejected as fragile.
- Craft sessions live in daemon memory, so a daemon restart ends them (the
  owner relaunches); persisting them would put session secrets in SQLite.
- The wrapper's file list is basic (folders, names). Thumbnails, search,
  new-folder and rename in the page are not built; the proxy supports the
  methods already.
- EffectCraft registers a service worker (`sw.js`); whether it caches
  responses from `/_homestead/files/` is not verified and needs a KVM look.
- The Caddy `reverse_proxy` to the craft socket has no request body size
  limit of its own; the daemon enforces the configured maximum.
- Existing route tables applied before S254 get `CraftApp` inferred for the
  five hooked apps only (by upstream name and port 8080); other craft routes
  need the field from a fresh install plan, which only matters if they ever
  gain a hook.

## S254 review follow-ups

- ADR-032 mentions adding the `homestead-craft` group to the Caddy service
  with `group_add`. That is a design statement, not a typo, so the accepted
  record is left as written; a superseding ADR should reword it if the
  unprivileged-edge case is ever supported (the edge runs as root today).
- The owner's plain-http opt-in is not persisted: rotating a link that was
  made with `allow_insecure_http` asks for the flag again. A stored flag
  would need a schema change.

## S254 local Nextcloud link follow-ups

- Existing Nextcloud installs (before the `{{route_host}}` trusted-domain
  environment) and later route-host changes need `occ
  config:system:set trusted_domains` run by the owner; Homestead does not
  exec `occ` or rewrite trusted domains on a route change. A planned,
  audited `occ` step would be the fix.
- Installs before S249 lack the edge network on routed services; doctor
  reports it but no planned flow attaches it (a package re-apply that
  regenerates the compose file would).
- A route whose exposure policy admits only LAN sources refuses the
  daemon's loopback dial to the edge; the link reports the 403 and the owner
  links the published loopback port instead.
- The daemon sends no `Retry-After` on the 429 account lockout, so the login
  page cannot show the remaining wait (the dashboard helper reads it if one
  is ever sent). A claimed appliance whose owner reloads the claim page
  before creating the administrator also lands on "already set up", because
  the claim-window wizard route answers 401 once claimed; a pending-admin
  signal on `/api/v1/identity` would let the page offer the form again.

## Dashboard visual foundation (S282)
- Stored theme flash: the manual theme is applied when the Angular app
  starts, so a stored dark or high-contrast choice shows the system theme for
  a moment on reload. An inline pre-bootstrap script would fix it but is
  deferred until a Content-Security-Policy decision for the dashboard.
- Config and Firewall keep their inline "Confirm apply" plan panels; they are
  plan reviews, not alertdialogs, and S284 decides whether they become
  dialogs.
- Page content still mixes message kinds: for example the Backups
  "no owner-held recipient" warning renders as a success status message.
  Status badges are applied only to Status checks and Audit outcomes; the
  rest of the pages adopt them in S284.
- Sidebar links keep the 44px touch target, so the page list scrolls inside
  the sidebar on short viewports; a denser pointer-fine layout is a design
  choice for S283/S284.
- The initial bundle budget warning moved from 500kB to 560kB (initial JS
  plus CSS, fonts are assets and not counted); trimming the bundle is open.
- The overflow check is a local headless-Chromium script with stubbed API
  data, not a CI test; a committed browser harness is still open (see
  Visual regression automation).

## Dashboard sign-in and setup (ADR-033, ADR-034; owner decisions 2026-10-18)
- Droplet or relay tunnels for non-Cloudflare providers (for example a
  WireGuard relay on a DigitalOcean droplet): skipped for now. DigitalOcean,
  Route 53 and manual cover DNS and certificates only; outside access for
  them is a router port forward. Cloudflare Tunnel (ADR-017) stays the only
  tunnel provider. Needs an ADR before any slice.
- OIDC login for the command line (device authorization grant or
  browser-assisted loopback flow) against Authentik: deferred by ADR-033;
  command-line sessions authenticate with the local account only.
- RP-initiated logout (ending the Authentik session on Homestead logout),
  more than one linked identity, SSO for non-owner administrators, and an
  option to disable local password login: out of scope for ADR-033.

## Dashboard states, transitions and work orders (S283)
- Expected "not set up yet" reads (exposure review, TLS issuance plans) answer
  404. Pages treat them as states and Angular logs nothing, but Chromium still
  prints "Failed to load resource" for the response. Removing that needs the
  API to answer 200 with an empty or `configured: false` body, which changes
  the CLI contract too; needs its own slice.
- Edge, TLS and Trust follow their operation's event stream themselves and the
  work-orders rail follows it again, so those pages hold two streams for one
  operation. Moving them to the shared store is a clean-up.
- The rail finds running operations from the operations list (newest 20). An
  operation older than that, still running, is missed; a `status=running`
  filter on `GET /api/v1/operations` (with a CLI flag) would be exact.
- Operation titles for operations found on load come from a type table
  (`core/operation-labels.ts`) and fall back to the type name; the daemon does
  not store the app name in a field the dashboard can read.
- Operation detail has no cancel or retry action; the rail only reports.
- Backups and Updates have no operation of their own to follow from the page
  (backup runs start on a schedule); they appear in the rail when running.
- The `app-dialog` exit transition is an inert copy of the dialog; a native
  `closing` state or `@starting-style` exit with `allow-discrete` transitions
  would replace it once the browser floor allows.
- Several pages still lead with long prose and plain `<ul>` lists; page
  structure is S284.
- S283 KVM review findings: (1) closed (post-S283 fix): install now creates the
  `homestead-edge` network itself (idempotent, no proxy deployed), and a failed
  install rolls back its record so the retry is allowed. (2) closed by S285: the dashboard has an Uninstall action. (3) The browser's own console still logs 404/422 network lines for the
  not-set-up probes (TLS, Exposure, Backups destination); the pages show states,
  but silencing the log needs a 200 "not set up" reply from the daemon.
- S285 discoveries:
  - Closed after the S285 KVM review (Fix S285: give installed apps an address
    through the wizard): the wizard adds the app's route through the edge routes
    API and offers to set up the edge. Left: the add happens in the dashboard
    after the install operation, so an install started and abandoned in the
    browser gets its route when the owner returns to the wizard; a daemon-side
    "install and route" operation would not depend on the browser. A package
    installed by the CLI still has no route until `edge routes set`.
  - Two packages whose served container has the same service name (Authentik and
    Immich both use `server`) would collide on the shared edge network. Route
    ownership in the dashboard is matched by host name as well for that reason.
  - The sign-in step explains SSO but offers no install-time choice: there is no
    API to wire an app to Authentik at install. S286 owns that.
  - Data removal on uninstall names `<mount>/<package>` for each storage class
    from the owner's assignments. An app installed on a mount chosen only in the
    install request (not assigned) would fail the last step with a missing
    directory; the daemon could report the real mounts per installed app.
  - The requirements check runs through the plan route, so Requirements shows
    one failing check at a time as the daemon words it. A route returning the
    structured requirement report without planning would let the wizard list
    every check with its numbers.
  - Closed after the S285 KVM review: `requirements.ports` are host ports and
    Homestead publishes none, so Nextcloud's `ports: [80]` failed once Caddy held
    80 (confirmed on VM 2). Nextcloud now declares none (as the craft packages
    do) and the wizard defaults its container port to 80 from curated copy.
    Left: Jellyfin (8096), Immich (2283) and Authentik (9000) still declare
    their container ports as host ports, which only conflicts if something else
    on the host listens there; decide whether `ports` should mean host ports at all.
  - The plan route's storage floor check read free space only from class
    assignments, so a mount chosen in the request failed with "no space data".
    Fixed in the daemon (the chosen mount's free space is read from discovery),
    and the wizard assigns a chosen mount to a class that has none, so uninstall
    can name the data folders. S288 added the unassign and unregister routes.
  - Closed in S288: uninstall now removes the empty stack directory.
  - The Done step of an app opened later (not in the install session) lists data
    folders from assignments only; an app on a non-default mount says "on the
    storage you chose".
  - Closed in S288: the Storage page now lists a mount once when discovery
    returns it twice (the root cause in discovery is not investigated).
  - Hostnames, the container port, extra networks and public address records
    can all be set in the wizard; per-endpoint exposure other than the
    manifest's recommendation cannot, by design (ADR-006).
  - Raw operation step names are mapped to words in `core/operation-view.ts`;
    names the daemon adds later fall back to the slug with spaces.

## Dashboard sign-in with Authentik (S286)
- The KVM acceptance run on VM 2 is recorded in `docs/DEVELOPMENT.md`. The
  Authentik admin API is verified against 2026.8.3 only; the fake in tests does
  not model Authentik rejecting a provider (it accepted one with no grant types).
- The link asks Authentik for `max_age=30`, so a browser already signed in to
  Authentik less than 30 seconds ago links without a new prompt, and Homestead
  does not check the ID token's `auth_time` against it. Verify `auth_time`
  when present, or find a way to force a prompt that does not loop.
- `sso apply` needs a current plan: a second `apply` right after a successful one
  answers 409 (no current plan). Idempotence is shown by plan, approve, apply.
- The audit entry for `sso.approve` shows `keep_token=[redacted]` (a boolean,
  redacted by the field-name rule).
- Closed in S288: the CLI login command no longer prints the session token
  (`--print-token` for scripts).
- OIDC login for the CLI, RP-initiated logout, multiple links, non-owner SSO
  and disabling local login stay deferred (see the ADR-033 entry above).
- Hardening: the link URL works as a bearer credential for whoever completes
  it within its 10 minutes (the intent is bound to the admin and the state, as
  ADR-033 designs). Bind the intent to the owner's browser, or have the
  dashboard confirm the subject before the link is recorded.

## Post-claim setup walkthrough (S287)
- The walkthrough sends the owner to the existing Tunnel, Exposure, Certificates,
  Account and Sign-in pages (exempt from the gate) instead of embedding their
  review and approve steps. Embedding needs those pages split into components
  without their own heading; today each page owns a single h1.
- Step 1 does not detect a generated or temporary password: the daemon has no
  such state. Step 1 links to the Account password change and asks the owner to
  confirm explicitly. Track generated or temporary passwords so the daemon can
  require the change itself.
- The domain step records the provider and access choice and seals the DNS
  credential, but does not edit the domain wizard's zone and base; those still
  come from the domain wizard routes and the CLI.
- The setup state is per appliance, not per owner; a second admin sees the same
  walkthrough. Multiple admins are out of scope for v1.
- KVM findings left open (S287 review): the tunnel permission line for
  Cloudflare shows only once Cloudflare Tunnel is chosen (a note says so);
  the CLI tunnel teardown account ID in argv is closed in S288 and retired
  in S294 (no account ID is asked for); the CLI and the Tunnel page asking
  for the Cloudflare token again is closed in S294; skipping or
  un-skipping a step that is already done from real state prints the full
  status without saying the skip was ignored; the install wizard's "Back to
  the catalog" link is gated and lands on /setup, and the Edge, Trust and
  catalog pages have no "Back to setup" link; the apps step reads done as
  soon as an install starts, before it finishes; wizard_state has no
  in-repo factory-reset command.
- The KVM acceptance run for S287 is recorded in `docs/DEVELOPMENT.md`.

## S284 dashboard page passes

- Backups has no destination form (the destination is read-only), so inline
  validation was not added there. The recipient form still uses the older
  layout without inline messages; the schedule and Storage register forms
  have them.
- The Edge route editor table is not tuned for narrow screens beyond
  horizontal scrolling inside `.table-scroll`.
- Updates groups its craft controls under a collapsed section and Drift leads
  with what needs attention, but the per-app rows in both are still plain
  lists rather than tables.
- Exposure apply has no confirm dialog, only the `danger quiet` button and the
  preview beside it.
- Install wizard, Sign-in and Setup copy were reviewed lightly; "Cannot reach
  the appliance" strings in the wizard still differ from the shared wording.
- Doctor fix text and CLI commands are mapped in the dashboard because the API
  returns only name, status and detail. Moving them into the API would let the
  CLI show the same fix text.

## From the S284 KVM review
- Account "Unlink Authentik" and Trust "Re-issue all" run without a confirm dialog.
- Expected API 4xx responses (backups/destination 422, tls/issuance/plans 404,
  exposure/review 404, wizards/firewall 404, logged-out 401s) appear in the
  browser console.
- Catalog icons are letter placeholders.
- Atkinson's slashed zero is visible in versions and IPs.
- Config help text mentions "413"; Data page bullets and "(bind)" are jargon.
- Mono hostnames break mid-word in narrow table cells.
- The audit filter form is tall.
- "Log in" and "Sign in" wording is inconsistent.

## S288 open bug fixes
- Unassign and unregister judge impact by `<mount>/<package>` directories of
  installed stacks and by the classes installed apps require (an unreadable disk
  refuses; unassign ignores other classes sharing the mount). S276 per-path
  data declarations are not consulted. The dashboard dialogs state the effect but cannot preview the affected apps;
  the daemon's 409 names them after the owner confirms.
- Why discovery returns a mount twice is not investigated; the dashboard hides
  the duplicate.
- The claim token file is removed on a clean daemon stop but not if the daemon is
  killed hard (tmpfs, so a reboot clears it; the next start reuses it only if its
  token is still live). the `claim token` CLI command checks the daemon answers and
  the appliance is unclaimed before printing, so a leftover file is never shown.
- A stack whose `compose.yaml` is missing (files deleted by hand, records kept)
  cannot be uninstalled through the API: the route answers 404 "not installed".
  Cleanup of the leftover records and folders is manual.
- Existing deployments keep old tokens in their journal until it rotates.
- `tunnel setup` still accepts `--api-token` and `--account-id` arguments (they
  warn); remove the argv forms in a later slice.
- A tunnel cannot be set up before claim: `tunnel setup` needs a session and an
  unclaimed appliance has none. On the S288 fresh install of VM 2 the
  connector was laid out by hand (Cloudflare API plus the S246 files) so the
  public URL reaches the Claim page. Decide whether to allow a claim-token
  authorised tunnel setup, or document the manual route.
- An existing tunnel set up outside Homestead (no `tunnel_routes` row) has no
  adopt command; after claim the owner can only replace it through setup.
- Dashboard names must fall under the internal root's permitted suffixes
  (`.lan`, `.local`, `.home.arpa`, `.internal`, `.localhost`); a name under the
  owner's own domain (split DNS) is refused (S289). Supporting one needs an ACME
  or imported certificate for the dashboard route and an ADR amendment.
- The default dashboard route is created by the daemon at startup (with retry),
  not by the installer script (S289). A host that cannot run Docker at boot
  gets it once the edge can deploy.
- SSO plans approved before S289 do not list the new dashboard origins; the
  owner re-runs the Sign-in plan (S289). No automatic re-plan.
- The mDNS hostname is fixed at `homestead` (S221), so two appliances on one
  network both claim `homestead.local` (S289). A configurable hostname is not
  built.
- The dashboard route has no per-name certificate for a second owner-set alias
  and no IPv6-only name support (S289).
- The setup wizard's domain step does not embed the Certificates (TLS) page;
  it keeps its own provider and credential form and links out for certificates
  (S291).
- The wizard progress list is not clickable and the wizard is linear only; a
  free-jump mode was not built (S291).
- The Storage step is skip and unskip only through the setup API and CLI; the
  assignment itself uses the storage commands (S291).
- The Sign-in step shows no Authentik link, only guidance, when the sign-in
  defaults are unknown (S291, S292).
- The storage layout suggestion uses only names and sizes. It does not read
  whether a disk is solid-state or rotational, never suggests cache, and does
  not see disks that are attached but not mounted (ADR-013) (S293).
- Applying the suggested layout is not atomic. A refusal part way leaves the
  earlier registrations and assignments in place, and the page says so (S293).
- Disclosures (`<details>`) across the dashboard show no marker: `summary` is
  `display: flex`, which hides the native triangle, and the chevron rule in
  `base.css` targets `summary-unused`, so it never applies. Seen on the Storage
  page's "What the classes mean" (S293); not fixed there because it changes
  every page.
- Found walking setup from scratch on the KVM VM, 2026-10-20; not fixed in S292:
  - Closed in S294: the Domain step has a domain field; the tunnel plan
    creates the `<id>.cfargotunnel.com` CNAME, reuses the stored token and
    has no placeholder service.
  - Closed after S294 (S294 fixes): the progress box no longer falls back
    to "Starting" once the rail drops a finished operation; the tunnel
    status read probes the router once per 5 minutes, with UPnP and
    NAT-PMP/PCP in parallel; a Cloudflare domain is checked against the
    stored token's zones when saved; teardown puts back the records setup
    replaced (ADR-038).
  - Left open after S294: Authentik's name (chosen at install, default
    `.lan`) is not tied to the domain (S296).
  - Closed after the S294 fixes: storing the Cloudflare token checks a
    domain saved before it and warns (the token is kept); the daemon probes
    the router at start, so the first status read is served from the
    cache; a restored record keeps its original comment.
  - S294 residuals:
    - a domain saved while Cloudflare is unreachable is not checked; the
      tunnel plan reports a missing zone;
    - a status read during the start-up probe (about 3 s) still waits for it;
    - a tunnel set up before S294 deleted no records, so nothing needs
      restoring; its teardown finds the account from the route's zone or the
      token's single account, and deletes no DNS record (none was recorded);
    - the tunnel keeps one owner-entered hostname and service; the default
      dashboard and `auth.<domain>` names, edge routing and the
      Authentik-only public sign-in are S296;
    - a restored record gets a new Cloudflare ID; a tunnel set up between
      S294 and its fix (72c6702 only) has no replaced records on file, so
      nothing is restored for it.
  - App names (S295, ADR-039): every app gets a new `*.lan` name that the
    owner must add by hand to router DNS or a hosts file; only
    `homestead.local` is announced.
  - App sign-in (S298, ADR-039): apps that declare optional OIDC (Immich, Nextcloud,
    Jellyfin) are not offered a wiring to the linked Authentik, and the SSO
    redirect addresses do not include public names.
  - Backups (S297, ADR-039): setup does not offer a backup schedule for the installed
    apps.
  - The Authentik link callback still redirects to `/sign-in`; the dashboard
    forwards to `/setup` (S292) rather than the daemon carrying a return
    path.

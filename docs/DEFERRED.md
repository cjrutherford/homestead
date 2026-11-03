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
- Connector binary installation: closed in S308 (ADR-042); `bootstrap.sh`
  installs cloudflared from Cloudflare's pinned-key apt repository. S246
  setup still refuses with instructions when it is absent
  (`--no-cloudflared`).
- Docker engine installation: closed in S308 (ADR-042); `bootstrap.sh`
  installs Docker from Docker's pinned-key apt repository. The S071
  `docker.install` operation (`internal/docker/install.go`) stays unwired;
  removing it or reusing it from the dashboard is open.
- Dashboard build on the appliance (S307): `bootstrap.sh` and
  `install-systemd.sh` embed the dashboard only if `internal/dashboard/dist`
  was already built (Node.js 24 and pnpm); otherwise the daemon is API-only.
  A release artifact with the dashboard built in would remove the step.
- Closed in S290: pre-S246 tunnels can be adopted with `tunnel setup
  --adopt ID` (ADR-041 §2).
- AdGuard Home install-wizard automation (`POST /control/install/configure`):
  needed before friendly names (S086) work end-to-end against a fresh
  instance; kept out of S085 as version-brittle and unverifiable without
  a live server. S382 owns the deterministic bootstrap (a version-checked
  install API or a pre-seeded minimal configuration) and the first
  end-to-end local-name proof.
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
- Private registry discovery: S343 adds GHCR through its anonymous pull
  token, and Docker Hub stays anonymous. Images on private registries or
  private GHCR repositories report a refusal (the image may be private)
  instead of holding credentials Homestead does not have.
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
- Release automation follow-ups (S332): signing is manual, so CI cannot
  publish a signed release; `SHA256SUMS` lists per-platform file names and
  nothing renames them to the bare component names; no
  checksum-file counter automation (RELEASE_COUNTER is raised by hand);
  no release notes beyond the CHANGELOG section.
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

- S382 correction (stale, the manifest is signed at counter 8 by the HAI craft key, commit 3af901f): Owner signature: `catalog/craft/craft-manifest.yaml` is committed
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
- Genuine published host ports: S383 separates container listener metadata
  from `requirements.ports` and fixes false conflicts for proxy-routed curated
  packages. No current curated install publishes host ports. A package that
  truly needs one still requires a reviewed plan for an owner-selected host
  port, Compose mapping, conflict checks and CLI/dashboard parity; the
  container port must remain separate from the selected host port.
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

- S382 correction (stale, the manifest is signed at counter 8 by the HAI craft key, commit 3af901f): Owner signature: the four-app craft manifest (counter 2) is unsigned;
  GridCraft, PdfCraft and DeckCraft cannot be built outside a throwaway-key
  test until the owner signs it.
- S382 correction (stale, the run is recorded in docs/DEVELOPMENT.md; DNS resolution is S382's live check): KVM acceptance for the three apps is pending (see
  `docs/DEVELOPMENT.md`); the `knownImageVolumes` rows and header-table
  rows are marked pending until then.
- First-load size: the uncompressed wasm is 18.9 MB (GridCraft), 38.1 MB
  (PdfCraft) and 25.5 MB (DeckCraft), served gzip at about 37 percent;
  Brotli would shrink it further but `ngx_brotli` is not in the base image.
- Upstream MPL-2.0 code dependency in DeckCraft (symphonia, unmodified) is
  recorded in `docs/THIRD_PARTY.md`; a future change that patches it needs
  a source-availability review.

## Graphics craft packages (S251)

- S382 correction (stale, the manifest is signed at counter 8 by the HAI craft key, commit 3af901f): Owner signature: the eight-app craft manifest (counter 3) is unsigned; the
  four graphics apps cannot be built outside a throwaway-key test until the
  owner signs it. A KVM appliance that already holds a baseline at counter 3
  or higher from throwaway manifests (the S249 VM held 4) refuses this
  manifest; use a fresh appliance or a higher throwaway counter there.
- S382 correction (stale, the run is recorded in docs/DEVELOPMENT.md; DNS resolution is S382's live check): KVM acceptance for the four apps is pending (see `docs/DEVELOPMENT.md`);
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
- S382 correction (stale, the manifest is signed at counter 8 by the HAI craft key, commit 3af901f): Owner signature: the eleven-app craft manifest (counter 4) is unsigned;
  FilmCraft, EffectCraft and SoundCraft cannot be built outside a
  throwaway-key test until the owner signs it. A KVM appliance holding a
  baseline at counter 4 or higher from throwaway manifests refuses it.
- S382 correction (stale, the run is recorded in docs/DEVELOPMENT.md; DNS resolution is S382's live check): KVM acceptance for the three apps is pending (see `docs/DEVELOPMENT.md`);
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
- S382 correction (stale, the manifest is signed at counter 8 by the HAI craft key, commit 3af901f): Owner signature: the thirteen-app craft manifest (counter 7 since S254) is unsigned;
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
  - Closed by S383: curated proxy-routed packages declare container ports
    separately from host requirements, so a listener on the host no longer
    blocks an app that does not publish that port.
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
  in-repo factory-reset command (closed in S306: `scripts/factory-reset.sh`).
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
- Closed (S294, recorded in S300): `tunnel setup` no longer accepts
  `--api-token` or `--account-id`; the retired flags refuse with usage help.
- Closed by S301 (owner decision 2026-10-24): a tunnel is not set up before
  claim. Claim on the local network or through an SSH forward of the
  loopback port; a hand-laid tunnel, adopted after claim, is the documented
  last resort (`docs/architecture/LOCAL_DASHBOARD.md`).
- Closed in S290: a tunnel set up outside Homestead can be adopted with
  `tunnel setup --adopt ID` (ADR-041 §2).
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
  - Closed by S296: Authentik's public name is `auth.<domain>` through the
    tunnel; its local name stays the one chosen at install.
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
    - closed by S296: the tunnel publishes `homestead.<domain>` and
      `auth.<domain>` through the edge, with Authentik-only public sign-in;
    - a restored record gets a new Cloudflare ID; a tunnel set up between
      S294 and its fix (72c6702 only) has no replaced records on file, so
      nothing is restored for it.
  - App names (S295, ADR-039), what remains after S295:
    - the `.lan` resolver binds the LAN addresses found when it starts; an
      address that changes later (DHCP) needs it turned off and on again or
      a daemon restart;
    - turning it on stores the switch and starts it, but the port 53
      firewall rules wait for the next firewall apply (the plan says so);
    - a non-default dashboard name outside `.lan` (`nas.home.arpa`) is not
      answered by the resolver; apps installed under other suffixes get no
      `.local` name;
    - `.local` names are IPv4 only and unprobed, as `homestead.local` is: a
      clash with another device is a doctor warning, never a rename;
    - the app page shows names for routes the installer named after the app
      (`appRoutes`); a renamed route is listed on the Names page only.
  - App sign-in (S298, ADR-039): apps that declare optional OIDC (Immich, Nextcloud,
    Jellyfin) are not offered a wiring to the linked Authentik, and the SSO
    redirect addresses do not include public names.
  - Closed by S297: setup offers a nightly backup policy. What remains:
    - off-appliance destinations (a second disk, a remote target) are not
      offered; backups stay on the backup class's disk;
    - the policy runs in the daemon's local time zone; a time zone change
      on the host takes effect at the next daemon start and is not
      surfaced beyond the zone name shown with the policy;
    - every covered app runs at the same minute; runs are serialized by the
      operation queue, not staggered;
    - turning the policy off keeps existing runs; nothing prunes them
      until a later run of the same app;
    - an uninstalled app's runs are kept, as with schedules.
  - The Authentik link callback still redirects to `/sign-in`; the dashboard
    forwards to `/setup` (S292) rather than the daemon carrying a return
    path.
  - Public dashboard (S296, ADR-037 §4), what remains:
    - app routes through the tunnel: closed in S290 (`apps public`);
    - all public names must sit in one Cloudflare zone;
    - a plan cannot leave the dashboard out once sign-in is linked: the
      owner tears the tunnel down instead;
    - renaming Authentik's route drops its tunnel names; re-plan the tunnel;
    - Sign-in is not re-planned automatically after setup: the status flag,
      the Sign-in page and the setup Sign-in and Domain step details (S304)
      ask the owner to re-plan and approve;
    - after teardown, the configured sign-in keeps the public origin until
      the owner re-plans it (harmless: the edge no longer serves the name);
    - closed by S300: the connector reads `TUNNEL_TOKEN` from its
      root-only environment file, not `--token` in argv.
  - App sign-in with Authentik (S298, ADR-039 §3, ADR-040), what remains:
    - closed by S299: uninstall refuses while sign-in is on; with
      `leave_sign_in` it forgets the sealed secret and the record, and the
      provider and application stay in Authentik for the owner to delete;
    - turning sign-in off needs Authentik reachable and an API token; a
      failed removal leaves the objects for the owner to delete in
      Authentik;
    - Jellyfin's own login page gets no "Sign in with Authentik" button; the
      sign-in address is shown on the app page (a branding/login disclaimer
      edit is a later item);
    - Nextcloud keeps `allow_local_remote_servers` on so user_oidc can reach
      Authentik on the LAN; it is not turned back off with sign-in;
    - the Jellyfin SSO plugin is pinned (release and checksum) against the
      catalog's Jellyfin image; a Jellyfin image bump must re-check the
      plugin's target ABI;
    - S290's public names show as a sign-in re-plan on the app page; it is
      not re-applied automatically;
    - the Authentik API token is asked for on each apply unless the owner
      kept it sealed when setting up dashboard sign-in;
    - SAML, LDAP and apps beyond Immich, Nextcloud and Jellyfin are out of
      scope.
    - ~~the sign-in plan does not know whether Nextcloud has finished its
      first-run setup. The apply refuses in `configure-app`, after the
      Authentik provider exists, and the next apply reconciles it.~~
      Closed by S312: the apply waits, bounded, for Nextcloud's own install
      in an `app-set-up` step before any change, and a not-set-up failure is
      guided. The plan itself still does not check the container;
    - Nextcloud gets `overwriteprotocol https` only with sign-in. Without
      it, Nextcloud's own redirects say http and rely on the edge's
      redirect. The catalog could set `OVERWRITEPROTOCOL` for every
      install;
    - the pinned plugin's `meta.json` says `autoUpdate: true`. It is not
      from a Jellyfin repository, so the update task has nothing to pull,
      but a later plugin repository entry could replace the pinned build.
- Public app names and tunnel adoption (S290, ADR-041), what remains:
  - no Authentik forward-auth in front of a public app: the plan warns and
    recommends app sign-in; an edge forward-auth policy is a later ADR;
  - closed by S299: uninstall refuses while the app has public names;
  - only tunnels configured from a local `config.yml` can be adopted; a
    tunnel configured remotely in the Cloudflare dashboard is refused;
  - adoption drops ingress entries Homestead does not manage (they are
    listed in the plan); the DNS records behind them are left untouched;
  - Authentik's public name is managed only by the tunnel plan, not by
    `apps public`;
  - one public name is added or removed per plan; renaming is remove then
    add.
  - closed by S302: after adoption without a setup domain, the stored zone
    name proposes app public names and the remove plan names the zone. A
    tunnel adopted before S302 stores the name at its next applied plan.
- Uninstall leftovers (S299), what remains:
  - leftovers are removed by their own plans first; uninstall does not
    remove public names or turn sign-in off in the same operation;
  - Authentik's own public name belongs to the tunnel plan, so uninstalling
    Authentik does not check it;
  - uninstalling Authentik (no app sign-in left) still leaves the
    dashboard's own sign-in (S296) pointing at it;
  - a `leave_sign_in` uninstall cannot reach Authentik, so the owner deletes
    the provider and application there by hand (the audit names the client
    id).

## Dashboard design language (S309)

- Field panels (state furrows around a group of facts) are used on Status and
  Setup only; other pages keep plain panels until each is reviewed.
- Approval stamps live in component memory: a reload drops the Approved stamp
  and its "by" and "when", because most plan APIs do not return who approved a
  plan or when. Persisting approvals needs an API field per plan.
- Plans without a digest render a permit with no seal: firewall, exposure,
  install, uninstall, Nextcloud link, and platform updates. Adding digests to
  those plan APIs would give every permit a seal.
- The platform updates permit is a preview with no stamp; the update runs as an
  operation, not an approval.
- Danger risky blocks (Caddy deploy, reconcile, re-key, teardown, volume
  removal) stay as risky panels rather than permits.
- The craft image verify report is a report, not a permit.
- Arrows remain in non-button change text on Config and Updates (diff lists);
  replace with words when those pages are reworked.

## Lamplight (S310)

- Owners who stored "Match my system" keep it; owners with no stored choice
  who relied on the old system-follow default now get dark until they pick
  Match my system on Account.
- ~~Operations rail step glyphs get halos only for failed steps; the rail's
  own state dots and table state glyphs outside badges have no lamp halo yet.~~
  Closed by S311: a failed work order's rail glyph has a halo too; table and
  list states are badges, which are lamps.
- ~~Fields (plates with state furrows) are still used on Status and Setup only,
  so other pages' plain h2 sections sit flat on the ground until reviewed.~~
  Closed by S311: every routed page's sections are fields, enforced by
  `theme/lamplight.spec.ts`.

## Lamplight on every page (S311)

- `/sign-in` is the Authentik sign-in settings page, not a sign-in screen, so
  it takes fields like every other settings page rather than the centred
  gate that claim and login use.
- Section states are furrowed only where the page already computes a state
  for the whole section (Drift's Needs attention, Setup, Status). Sections
  whose rows mix states (Certificates renewal, Backups scheduled jobs) carry
  their state on each row's lamp; a section-level state needs a computed
  worst-state per page.
- Inputs inside a permit render as plain wells of the page theme, not paper;
  no current permit holds an input, so the paper-input treatment waits for one.
- The stamp has no ink-press shadow (removed as the accessory in review);
  revisit only if the stamp reads flat on real hardware.

## Edge certificate coverage and apps that set themselves up (S312)

- The brief's first draft refused names more than one level below the zone;
  the owner replaced that with a coverage warning (2026-10-26). The depth
  acceptance criteria are met by coverage tests instead: the domain and
  defaults are never rewritten, and an uncovered name warns.
- Coverage lamps on status pages read Cloudflare on every status request,
  once per zone. A cache, or a lamp that refreshes after ordering a
  certificate without reloading, is not built.
- A certificate pack still being issued (status other than `active`) counts
  as no certificate; the lamp does not say "pending".
- Only the certificate packs read decides "unreadable"; a refused Total TLS
  read is taken as Total TLS off.
- Nextcloud installs made before S312 keep their own admin account: the
  `admin_password` secret shows as missing and is not created for them. The
  owner keeps the password they chose in the setup page.
- Only Nextcloud's adapter detects "not set up yet". Immich and Jellyfin
  have no equivalent wait.
- Only Nextcloud's `admin_password` is declared readable; other packages'
  generated credentials stay presence-only.

## App install sets up the local and public address (S313)

- Immich, Jellyfin and Authentik have no Host allow-list to update, so a new
  name needs no app change. Not configured: Immich's external domain (share
  links still use the address the visitor came from) and trusted proxies;
  Jellyfin's known proxies (its logs show the edge's address, not the
  visitor's). Authentik's public name belongs to the tunnel plan (ADR-037),
  not to an app install.
- Nextcloud's `TRUSTED_PROXIES` is not set, so it logs the edge's address.
  Nextcloud installs made before S313 get `OVERWRITEPROTOCOL` and
  `OVERWRITECLIURL` only when their Compose file is rendered again (an
  update or a reinstall), and trust `nextcloud.local` only once a public
  name is added (the occ step adds every local name too) or the app is
  reinstalled. No migration rewrites them.
- The install route accepts an install without a digest (older clients and
  `apps install` without `--public`); only the wizard and
  `apps install --public --approve-public` bind it.
- The CLI chains plan and apply as two commands (`--public`, then
  `--approve-public D`); there is no interactive prompt. Sign-in at install
  stays a wizard-only chain; the CLI turns it on with `apps sign-in apply`.
- The reach check runs from the appliance, so a public name that answers
  there may still not resolve on a device behind another DNS; the wizard's
  own browser probe covers this device only. Repeated checks are manual
  (**Check again**); there is no background re-check.
- A public plan refused before the install (for example a duplicate name)
  shows on the Address and Review steps; the owner turns publishing off to
  install without it. There is no automatic name suggestion.

## S314

- Drift is reported by the read-back (`apps sign-in access`, `sso access`) and
  never put back; there is no "Put back" action. ADR-043 §6 leaves a repair to
  a later slice.
- Granting people and their groups is S317 (People page and CLI). Until then
  the owner adds someone to `homestead-<app>-users` in Authentik by hand.
- Other members of `homestead-owner` (added in Authentik) are left alone and
  only counted; Homestead adds the linked owner and never removes anyone.
- There is no scheduled read-back; it runs when the owner asks. An
  Authentik token is needed for it unless the dashboard sign-in kept one.
- The groups scope mapping is appended after the provider's stock mappings,
  and Authentik merges claims in order; a stock mapping that also emits
  `groups` would be overridden by ours. Not exercised against a live
  Authentik.
- Adapter admin mapping is unverified live: Nextcloud (`user_oidc`
  `--group-provisioning` / `--mapping-groups` and the `admin` alias group),
  Jellyfin (SSO plugin `AdminRoles`/`Roles`/`RoleClaim`) and Immich
  (`roleClaim` `immich_role`). The owner's VM 2 check covers them.
- Turning sign-in off removes the app's two groups, but an uninstall with
  `leave_sign_in` leaves them (the refusal text says so); Homestead cannot
  delete them then.
- A sign-in made before S314 stays open to every Authentik user until the
  owner approves a re-plan; no automatic migration, by design.
- Only one linked owner is matched, by the Authentik `uid` (the hashed user
  id) that equals the OIDC `sub`; an Authentik sub mode other than hashed
  would not match and the plan then warns that no owner is linked.

## S315

- Craft apps stay on host serving (`<app>.lan`): they are built to answer at
  `/`, and a path for them would need each craft image changed and
  re-signed. A later slice may move them.
- Immich stays `serve: host` with its own CNAME (its web app does not
  support a sub-path); the coverage check still runs for its name.
- The move is refused while the app has a public name; the owner takes the
  name off first and publishes again at the path after the move. For
  Authentik the move is refused while the tunnel publishes it: tear the
  tunnel down, move Authentik, then set the tunnel up again. No combined
  plan does this in one step.
- After a move the old `<app>.lan` / `<app>.local` address stops answering;
  there is no redirect from the old name (it would need the old route kept
  as a redirect-only route). The plan warns.
- After a move, app sign-in is re-planned by the owner (the redirect URIs
  change); after moving Authentik, the dashboard's sign-in is planned and
  linked again at the new issuer URL. Password sign-in to the dashboard
  keeps working throughout. Nothing re-plans on its own.
- A path app is published only on the dashboard's public name; it cannot
  have a public name of its own. With path Authentik, the tunnel publishes
  Authentik only together with the dashboard.
- Shared origin (ADR-044): apps under the dashboard's name share its web
  origin. Stated once on the Tunnel page and in INSTALL.md; no per-app
  isolation (separate cookie paths, CSP) beyond the dashboard's own
  cookie settings.
- Unverified live, for the VM 2 check: Caddy's handling of the path routes
  (`handle_path` versus `handle`, the `.well-known` redirects, 404 for an
  unpublished path on the public name); Nextcloud honouring
  OVERWRITEWEBROOT through the image's reverse-proxy config; Jellyfin
  reading the seeded `network.xml` with only BaseUrl; Authentik under
  AUTHENTIK_WEB__PATH (issuer, endpoints, static files, outposts); and the
  move's override environment taking effect on restart.
- The move rewrites Jellyfin's `network.xml` BaseUrl with the stack
  stopped; an owner-edited `network.xml` is replaced only in that element.

## S316

- Doctor never acts: no "fix it" buttons. Each problem names the page or the
  command; running it stays the owner's step.
- No support bundle (checks, versions and log tails in one file) and no
  scheduled background probing. Live probes run only when asked
  (`?live=1`, `--live`, Check again); the cheap checks never touch the
  network.
- The cloudflared connection count reads `/ready` (`readyConnections`) on
  127.0.0.1:20241-20245, falling back to the `cloudflared_tunnel_ha_connections`
  gauge on `/metrics`. The metrics address is not configurable here; a
  connector started with a different `--metrics` address reads as unknown.
- Container checks treat a service that exited with code 0 as a finished
  one-shot job. A long-running service that exits 0 on its own is not
  flagged.
- First-run ("set up") detection is Nextcloud only (`occ status`); other
  apps have no such probe.
- Unverified live, for the VM 2 check: the `/ready` route and its JSON on
  the installed cloudflared version; Authentik's `/-/health/live/` path
  under AUTHENTIK_WEB__PATH (a 404 reads as a warning, not a failure); and
  probe timing against a real tunnel and real containers.

## S317

People (ADR-043 §4–6): the People page and the people commands of the CLI.

- Wizard grants happen after sign-in is applied, as plain users, one call per
  person. They are not part of the reviewed plan digest and not audited as
  part of the install; each grant is its own audited people action. No admin
  grant in the wizard: make someone an app admin afterwards on People.
- Disabling a person sets them inactive in Authentik. Sessions they already
  hold are not revoked; they last until they expire.
- The audit record is written after the change. It is not fail-closed: if the
  audit write fails after a reveal, the credential was still shown once.
  Revealed values are never in the audit, logs or argv.
- Only the kept sealed token is used. A one-off token for the People page is
  not accepted.
- The Authentik `akadmin` account and any other account Homestead did not
  create appear only in the unmanaged list and are touched only by explicit
  Adopt. Service and system accounts (service types, `ak-outpost-*`) are
  never listed.
- Second-factor reset removes devices of kinds Homestead knows (TOTP,
  WebAuthn, static, SMS, duo); unknown device kinds are reported as skipped,
  not deleted.
- Drift covers name, username, email, active state and the `homestead-`
  groups. It does not compare other Authentik attributes, policies or roles.
- No bulk actions, no per-app role names beyond user and admin, no invites by
  email (Homestead sends no mail).
- Unverified against a real Authentik, for the VM 2 check: the exact response
  shape of the recovery-link and set-password endpoints, device listing paths
  under `/authenticators/admin/`, and `include_groups` on the users list.
  Checked against Authentik's published API docs only.

## S318

Immich sets itself up (first-run routine).

- Only Immich has a first-run routine; Jellyfin's startup wizard is S319.
- The administrator's email is fixed (`admin@homestead.lan`). It is not the
  owner's Authentik email, so the owner's own Authentik sign-in creates a
  separate Immich account (made an administrator by the `immich_role` claim
  when they are in the app's admin group).
- Each person's own Immich onboarding (theme, language, backup settings on
  first sign-in) is left to them; only the administrator onboarding that
  blocks the web app is finished.
- Changing the Immich administrator's password in Immich leaves the revealed
  value stale; Homestead does not detect that.
- If Immich's login is later turned off or the administrator deleted, the
  routine leaves Immich as it is and does not recreate anything.

## S319

Jellyfin sets itself up (startup wizard).

- A Jellyfin installed before S319 keeps its own setup: its wizard is
  complete, so the routine never runs, and the media folders are not added
  (only a fresh install provisions data folders). Reinstall, or add the
  folders and libraries in Jellyfin.
- Language and metadata country are fixed to en-US / US; there is no
  setting for them yet.
- The server name is "Homestead"; the appliance has no stored name of its
  own to use instead.
- Libraries are not scanned at creation (they start empty); real-time
  monitoring picks up files copied in later.
- Library folders are not shared with Nextcloud or the craft apps.

## S320

Craft apps under one host, by path (ADR-045).

- The image still ships its own wrapper (`images/craft/homestead`), which
  is no longer used on path routes. Remove it, or make it relative, at the
  next signed craft build; the drift test then needs updating.
- Craft apps installed on a name of their own now move to their paths
  when the daemon starts (ADR-045 §5, amended 2026-10-27). The old name
  stops answering with no redirect to the path; a redirect from the old
  name was not asked for. The edge handle, the image under the prefix and
  the daemon's wrapper and session path were checked live with a throwaway
  Caddy against the real cadcraft container and craft socket.
- Opening and saving Nextcloud files through the wrapper on a path route
  was not checked in a browser.
- Publishing a craft app shows the generic path-app warnings; there is no
  craft-specific note that the file path still needs an operator session.
- Craft apps share the dashboard's origin (ADR-045, Consequences).

## S296 live check (2026-10-27)

- VM 2's dashboard sign-in was planned at 13:52, before the tunnel (14:09),
  so it covers only homestead.lan and homestead.local. On the public name
  the login page offered neither the password nor Authentik. It now says
  to re-plan Sign-in on the home network. The tunnel plan still only notes
  the re-plan; folding the sign-in update into the tunnel approval would
  change the owner's 2026-10-20 decision (a separate approval) and needs
  the owner's answer first.
  The owner answered on 2026-10-27; S322 (ADR-046) folds it in.

## S322 tunnel setup updates sign-in (2026-10-27)

- Teardown leaves the public origin in sign-in. It is inert once the name
  no longer reaches the appliance; removing it would need the kept token
  and another Authentik change at teardown.
- A tunnel plan held across a sign-in change elsewhere is caught only by
  the step's own check. The digest covers the planned change, not the
  configured sign-in it starts from.
- Without a kept token, the owner still re-plans Sign-in separately; the
  tunnel flow does not ask for the token.

## S323 published apps sign in at Authentik's public address (2026-10-27)

- The certificate for the public dashboard name is a separate approval in
  TLS. Nothing adds it to the TLS strategy or plans it from the tunnel
  flow; the app sign-in plan only says it is missing.
- An Authentik on its own name (the layout before ADR-044) with a tunnel
  name keeps the local issuer for apps; ADR-047 covers the path layout
  only.
- Publishing, unpublishing, or placing the certificate does not re-plan
  app sign-in. The app's status shows a waiting re-plan for the owner to
  approve.
- An issuance plan lists the dashboard's local name as the served route
  for a certificate covering its public name, not the public name itself.
- LAN browsers sign in to published apps through the public name, so app
  sign-in needs the internet (ADR-047 §4); there is no split DNS.

## S324 the appliance runs the setup approved with an install (2026-10-27)

- Craft apps still install with their own route names. ADR-048 §4 puts
  them through the same setup at `/apps/<name>/` and links them to
  Nextcloud automatically (S326). Done in S326.
- Issuing the public certificate from the tunnel plan, and moving
  applied apps to the public issuer (ADR-048 §5–§6), are S325.
- The CLI cannot approve sign-in or people with an install. Only
  `apps install --public --approve-public D` sends an approval
  (`setup.public`). Sign-in is set up afterwards with
  `apps sign-in apply`.
- An install without a kept Authentik token cannot carry sign-in. The
  install request takes no token, so the setup records the step as
  skipped and says how to set sign-in up later.
- A setup that is running when the daemon restarts reads as stopped at
  its running step, and is resumed from the card. The daemon does not
  restart it on its own.
- Skipping ends the whole remaining setup. There is no way to skip one
  step and keep the rest.

## S325 tunnel setup issues the public certificate and moves app sign-in (2026-10-27)

- The tunnel always plans Let's Encrypt production. There is no choice
  of staging or another CA from the tunnel plan; a different CA is set
  up in TLS first, and the tunnel reuses a DNS-01 ACME entry covering
  the name.
- A TLS strategy entry covering the public name that is not ACME
  DNS-01 (manual or internal) blocks the tunnel's certificate step. The
  tunnel does not replace it.
- After a fresh tunnel setup only the dashboard and Authentik's path
  are published, so the tunnel's move list is usually empty; apps
  published later plan at the public issuer directly. The move list
  matters for certificates issued from TLS after apps were applied.
- An app that cannot be re-planned when the moves are listed (not
  installed, no address) is left out; its sign-in status shows its own
  re-plan.
- The certificate step's failure leaves the strategy entry it added in
  TLS, where the retry uses it.
- The terms URL shown is Let's Encrypt's repository page. The exact
  subscriber agreement is the one the CA's directory names at
  registration.

## S326 craft installs use the same setup, by path, linked to Nextcloud (2026-10-28)

- The automatic link uses Nextcloud's administrator account (`admin`),
  with an app password Homestead creates. A dedicated Nextcloud account
  for craft files was not asked for.
- Only the five craft apps with file hooks (cadcraft, effectcraft,
  filmcraft, gridcraft, pdfcraft) are linked. Lightcraft and the others
  keep documents in the browser.
- The link is global (one Nextcloud for all craft apps). A craft app
  installed while another Nextcloud is linked reuses that link; the
  review says where documents go.
- Installing Nextcloud when a link to another Nextcloud exists links
  nothing and does not offer to move the link.
- Waiting for a Nextcloud that is still installing is bounded at 5
  minutes. After that the step fails and is resumed from the card.
- A craft route that arrives without a path is moved to its path without
  saying so to the client that sent it; the reconcile result shows the
  path route.
- Not checked live on VM 2 (not touched in this slice).

## S327 Jellyfin sign-in button (2026-10-28)
- Live check on VM 2 that the button shows on Jellyfin's login page and
  starts a sign-in, and that turning sign-in off removes it. Not done: no VM
  access in this slice.
- An owner who edits the disclaimer in Jellyfin's dashboard by hand inside
  the Homestead markers loses that edit on the next apply. Documented by the
  markers; no separate handling.

## S333 recovery flow and the add dialog (2026-10-28)
- Forgot password: resolved by owner decision (2026-10-28, ADR-049
  amendment). People ask the owner, who resets the password from People.
  Authentik shows its "Forgot username or password?" link only from the
  identification stage's own recovery flow, which Homestead leaves unset
  (S336 shows a hint on the login page instead).
- Deferred: outbound email for self-service recovery (identification,
  email, then the recovery flow's stages; needs Authentik's email
  settings and an owner decision).
- The recovery flow has authentication "none" so Authentik can plan it
  during the administrator's API call that makes the link. Opening it
  without a link reaches the password prompt and then fails at the write
  stage (no pending user); nothing is changed. Not verified live.
- Step-up for the generated password on a public dashboard name is S334.
- Live check on VM 2 (re-plan shows the recovery flow, apply sets it on
  the brand, adding a person shows a working link). Not done: no VM access
  in this slice.

## S334 Authentik step-up on the public address (2026-10-28)
- `auth_time` from a live Authentik was verified on VM 2 on 2026-10-28
  (audit `sso.step-up` ok, then `people.mfa-reset` and
  `people.password-reset` authorised). Resolved.
- The step-up callback runs on a cross-site return, so the Strict session
  cookie is not sent. The browser is bound by the state cookie and the
  session by the id stored with the state; the session is re-checked
  (oidc, same owner, still live) when it is marked. A second browser that
  shares neither cookie cannot finish the flow.
- The failed-step-up message (`?sso_error=`) is resolved in S335.
- The step-up applies to the five People routes only. The other step-up
  routes (sso plan, approve, apply, disable, secrets rekey, identity
  export, secret reveal) stay unavailable on the public name; S335 makes
  the dashboard say so (`local-only`).
- Live check on VM 2 of the full ceremony on the public dashboard name
  (Authentik prompt, return to the People page, action succeeds, expires
  after five minutes). Not done: no VM access in this slice.

## S336 login page hint (2026-10-28)
- The hint is the title of Authentik's `default-authentication-flow`, the
  only text a flow lets Homestead set. An owner who customised that flow's
  title sees it replaced when the plan is applied. Not verified live.

## S337 apps list (2026-10-28)
- Person sessions, the owner-only boundary and the person allowlist for
  `GET /api/v1/me/apps` are S338; until then the routes are owner session
  class only.
- The apps page itself, `/admin/` paths and redirects are S339. The
  dashboard has only API client methods (`myApps`, `personApps`).
- `--json` is new with S337; the other CLI commands have no JSON
  output. Adding it everywhere is open.
- The list shows a local address even when the owner is on the overlay
  name; it follows the dashboard route's first name, not the name the
  request used.

## From S338 (person sessions)

- Drift read-back of `homestead-people` membership (people who are active in
  Homestead but missing from the group) is not reported; it would flag every
  person before the owner re-plans. Membership is synced when a person
  changes and when the re-plan applies.
- The person shell, the apps page and redirects away from owner pages are
  S339. The dashboard only learns the role (`SessionService.role`) and
  records `owner-only` answers without toast loops or session expiry.
- The CLI shows no person sign-in status.
- A person session shares the idle and absolute lifetimes of the owner's;
  no separate, shorter person lifetime.
- Records from before S338 have no stored Authentik uid; the first sign-in or
  People list asks Authentik for the user and remembers it (at most 50 per
  list). Needs a kept token for the sign-in case.

## From S339 (the apps page)
- Tiles show a large monogram of the app's title, as the catalog page does.
  RESOLVED in S340: the bundled `mdi:` icons render; the monogram is the
  fallback for an unknown name.
- DEVELOPMENT.md history entries and ADRs still name pre-S339 paths such as
  `/people`; they are records and were not rewritten.
- The sidebar still shows on the root-level owner pages (setup, claim, login,
  sign-in, account); only `/` is chrome-free.
## Catalog expansion (2026-10-23)

The evaluation and wave plan live in `docs/CATALOG_EXPANSION.md` and
`docs/SLICE_PLAN.md`. The Everything track remains planned, not implemented.

- Puter/Everything apps: S367–S380 are packaged (ADR-057) and S381 is deferred; the note below is the original evaluation. The monorepo's apps are Vite SPAs that
  load the Puter cloud SDK (`https://js.puter.com/v2/`) for identity and
  storage, have no per-app Docker packaging, and overlap the existing craft
  suite. Revisit only with an ADR for a second static-app source, a fork or
  adapter that removes the Puter cloud dependency, or a self-hosted Puter
  platform decision. S367 proposes the adapter in ADR-053; the person-scoped storage and authorization contract, daemon-independent operation and bridge-specific tests remain unproven.
- Not planned for v1: mail servers (deliverability and port-25/abuse
  burden), Matrix/chat servers (federation and identity duplication),
  Home Assistant (needs host networking or device passthrough), object
  storage, CMS/blog platforms, a second photo gallery, and Nextcloud office
  add-ons (blocked on Nextcloud app management, not on another package).
- qBittorrent: deferred until a supported VPN/network-mode design exists;
  the current schema cannot express `network_mode: service:vpn`.
- Foundation items the planned apps still need are **scheduled work, not
  deferrals**: the written community-image policy (S360). OIDC actuation
  with the supervised secret-entrypoint wrapper (S342), GHCR update
  discovery (S343), catalog bundles (S344) and MariaDB/MySQL backup export
  (S352) have landed. The remaining order is in
  `docs/CATALOG_EXPANSION.md` section 9; this ledger keeps only the work
  that is genuinely unscheduled.
- MariaDB/MySQL database password verification and rotation: S352 adds
  both engines as dump kinds and lets their manifests declare a
  `PasswordSecret` so scheduled backups resolve it, but `ALTER USER` and
  the authenticate-with-a-wrong-password check remain Postgres-only.
  Registration for these engines refuses with a `--skip-verify`
  instruction, and rotation refuses before journaling, so a secret and a
  database never disagree. Implement in the slice that first needs to
  rotate a MariaDB/MySQL password (BookStack ships a generated one that
  is not rotated), including the host part of the user grant.
- SSO actuation surfaces: S342 lands the mechanism (catalog OIDC
  declaration, Authentik client actuation, catalog-secret storage,
  supervised secret export) but no owner-facing entry point. Provider
  endpoint and API-token configuration, the API route and CLI command
  that activate or deactivate one package's client, and the dashboard
  control come with the slice that turns SSO on for an installed app.
  Vaultwarden (S345) ships local-login-first with optional OIDC declared,
  so the missing surface blocks nothing today.
- Vaultwarden's admin page: S345 installs with it off and no
  `ADMIN_TOKEN`, because a generated token would enable an administrative
  surface by default. Enabling it later — generating the token through
  the secret store and exposing the page through configure — is not
  surfaced yet; the owner can still manage users in the app itself.
- Owner-chosen first-run admin credentials: Paperless-ngx (S346) and
  Linkding (S349) declare their first superuser password as a secret, but
  secrets are generated at install and never read back, so an owner who
  does not register a value beforehand must reset the account inside the
  app or with the image's `manage.py changepassword`. A general "this secret
  must be supplied by the owner" declaration, or an install-time prompt
  that requires one, is not in the secret schema.
- Mealie's seeded administrator: S350 ships the upstream default
  credentials (`changeme@example.com` / `MyPassword`) because the image
  creates that account itself, and the owner changes them at first
  sign-in. Driving Mealie's user store from Homestead (seeding a chosen
  password or disabling the default account) is not surfaced.
- Uptime Kuma's monitor bootstrap: S351 installs the board with no
  monitors; the owner adds checks in the app. Pre-seeding one monitor per
  installed app and route (the "all local apps" intent) needs Homestead to
  create monitors through the app's API, which is not scheduled.
- Audiobookshelf's OIDC setup: S353 declares optional OIDC, but the
  issuer, client ID, and secret live in the app's SQLite server settings
  and the image reads no OIDC environment variables. Until the SSO
  actuation surface above lands, the owner configures OpenID Connect in
  the app with the callback at `/auth/openid/callback`; local accounts
  keep working.
- Audiobookshelf metadata backup scope: `/metadata` is backed up whole,
  including covers, scan logs, and any app-created backups under
  `/metadata/backups`. Per-path exclusions inside a catalog backup source
  are not modeled, so an owner who enables large in-app backups should
  move the app's backup path or accept the extra size. The owner media
  volumes (`/audiobooks`, `/podcasts`) are never backup sources.
- Navidrome reverse-proxy header authentication: S354 ships local accounts
  as the documented exception. Upstream accepts a trusted user header
  (`ND_REVERSEPROXYUSERHEADER`), but Homestead's edge does not inject an
  identity header into app requests; wiring the edge to a trusted identity
  source is deferred. The external metadata services (Last.fm, Deezer,
  ListenBrainz) stay at upstream defaults; disabling them with
  `ND_ENABLEEXTERNALSERVICES=false` is an owner setting, not a catalog
  default.
- Komga OIDC client actuation: S355 declares optional OIDC, but Komga
  reads a Spring client registration (`spring.security.oauth2.client`) in
  `application.yml` under `/config`: the issuer URI, redirect template, and
  client credentials are owner-supplied, and the catalog has no issuer
  input to build them from. The owner configures the client in Authentik
  and in that file (backed up with the config tree); local accounts stay
  available. S392 now establishes a local recovery administrator only on a
  fresh install, refusing any reused `/config` content or claimed database.
  Actuating OIDC and per-person access remains deferred because pinned Komga
  cannot disable an existing account across Basic, sessions, API keys and OIDC
  while preserving its data.
- Several SQLite databases in one package: the catalog supports one
  database export per package. Komga's main `database.sqlite` is exported
  consistently, but `tasks.sqlite` and `sessions.sqlite` are copied as
  live WAL files; both are Flyway-migrated and recreated if missing, so a
  restore loses task history and sessions rather than library data.
  Calibre-Web Automated's `config/app.db` is exported, while `cwa.db` and
  the Calibre library's `metadata.db` are copied live from their trees; a
  restore may need Calibre's database restore to rebuild the library
  catalog, and CWA's ingest settings fall back to defaults. A multi-export
  declaration is deferred.
- Calibre-Web Automated OIDC client actuation: done in S415 (`env-oidc`
  with an inline script that writes CWA's own `app.db`; see the S415 lines
  below).
- Calibre-Web Automated original-backup growth: `processed_books` under
  `/config` keeps the original of each file CWA processes, so the
  application backup grows with the library. Per-path exclusions inside a
  catalog backup source are not modeled; owners who do not want the
  originals can turn the setting off in the app or accept the extra size.
- Gitea's SSH surface: S347 installs HTTPS-only (`DISABLE_SSH`), publishes
  no SSH port and deploys no keys. Enabling `ssh://` clones — an SSH port
  through the host or an overlay tunnel, authorized keys, and clone URLs
  that match — is an explicit owner decision not scheduled yet.
- Closing Gitea registration after the first account: S347 leaves
  self-registration open so the first user becomes the administrator, and
  the owner turns it off in the app's admin settings. Homestead cannot
  observe "the first account exists" and flip the setting today.
- SAML and LDAP client actuation, provider-side secret rotation, and
  authorisation-group mapping stay out of scope until a package needs
  them; the S342 mechanism is OIDC-only.

## Craft serve and DNS advertisement validation (S382)

- S382 found the planned AdGuard route-reconcile design superseded: ADR-039 (S295) built the accepted local-name path. Names derive from the edge route table: the in-process mDNS responder announces `homestead.local` and `<label>.local` for each host route, and the optional `.lan` resolver (`internal/localdns`, off until the owner approves a plan) answers `homestead.lan` and each host route's `<label>.lan`. Craft apps are served by path (S320), so they add no names of their own. `docker.EnsureNameDefinition`, `jellyfin.LocalRewrite`, `docker.AdguardDeployDefinition` and `dns.EstablishSplitDNS` still have no reconcile caller; S382 does not wire them (a second DNS path). Removing or folding them into one path is cleanup left for a later slice.
- S382 does not add an "advertised" state to the Craft apps start page: the page links `https://homestead.lan/apps/<app>/`, which resolves through mDNS on devices that do `.local`, or the `.lan` resolver or a router entry. A per-device claim cannot be made from the appliance. The Names page states which resolver is on.
- S382 AAAA/IPv6 answers, multi-homed appliances beyond the per-subnet address choice in `internal/localdns`, and advertising the resolver to clients through DHCP stay out of scope.
- S382 live-only: the 13-app build matrix and a second-client resolution proof are run by the owner on VM 2 from the slice runbook; mDNS does not cross the VM 2 NAT, so `.local` needs a device on the same L2 segment.

- Audiobookshelf Authentik adapter (S385): its first-run recovery administrator is created by S384, but OIDC client provisioning, app-side actuation, group mapping, install-time approval, and live sign-in remain to be implemented and verified. Existing owner-created administrators must not be overwritten.

- Catalog-wide install-time Authentik: packages without a Homestead adapter still need individually verified adapters, user provisioning, role mappings, and recovery behavior. Authentik-unavailable installs and stateless or unsupported apps need clear access handling. Server-side setup validates all selected people before grants, but sequential cross-service grants still need partial-failure recovery and cannot prevent account changes after validation.

- ADR-054 install access enforcement remains S390: connect the S389 decision contract to reviewed dashboard/CLI install flows, server-side step-up and verified app adapters; classify accountless apps and safely handle partial grants.
- Tunnel plan cannot refresh an existing tunnel's routes: `buildTunnelPlan` refuses while a tunnel exists, so the only repair for stale published paths (the 2026-10-31 Authentik move) is teardown, which drops every app public name and some cannot be re-published. S395 prevents that cause for the Authentik move but adds no refresh; a "re-plan routes in place" flow is still needed.
- Other `Published = false` writers are craft-only (`pathCraftRoutes`, `MigrateCraftRoutes`), which never touch Authentik; no further Authentik unpublishing path is known.
- S396 only adds names to an app's allow-list; Homestead-added names that are no longer served (after a move, rename or public-name removal outside the app.public flow) stay trusted until the owner removes them, because Homestead does not record which entries it added. Tunnel setup/teardown that bypass `startEdgeReconcile` (they call `reconcileEdgeSet` directly) do not run the hook; the next edge change or `nextcloud-link` resume does. If the app-hosts step fails (for example Nextcloud is stopped) after the edge table is saved, retrying the same edge change skips the app because its names no longer differ; the repair is resuming the craft nextcloud-link step or the next change to the app's names. A persisted "last trusted names" baseline would let a plain retry repair it.
- S399 skips the sign-in step when the Authentik API token is not kept (the public plan then tells the owner to re-plan sign-in); a one-time token field on the public apply request would let the same operation cover it.
- S400: a move whose sign-in step fails (Authentik down) leaves the app moved with its sign-in on the old address; the owner re-plans from the app card. A one-time token field on the move apply request would let a move also cover the token-not-kept case.
- S400: the craft files link is moved by address only. Nothing checks that Nextcloud answers at the new address before the link is updated; the Craft files page shows the link's readiness afterwards.
- S401 (S400 review): a move that fails between its stop step and its up step (a seed write fails) leaves the app stopped, and the error does not say so. A failure path should bring the stack back up or name the stopped app.
- S401 (S400 review): when the craft files link step fails, the move stops before its sign-in step, and the error only mentions the link. It should also say sign-in was not updated.
- S402: the S342 `catalog.ActivateOIDC`/`DeactivateOIDC` code (`internal/catalog/oidcapply.go`) is superseded by the env-oidc adapter (ADR-052 amendment) and unused; remove it and its tests.
- S402: per-app env-oidc slices: Mealie; Linkding (no discovery, derive the endpoints from the issuer); FreshRSS (two secrets); Komga (Java truststore); Paperless (secret inside a JSON setting).
- S402: Navidrome and Uptime Kuma have no OIDC; sign them in through forward-auth (owner reopened this 2026-10-31). An ADR is needed first.
- S402 (done in S403): an install from before an app declared its client secret now gets just that secret's wiring patched into its Compose file when sign-in is applied.
- S403: the stack patch (`catalog.WireDeclaredSecret`) is for declared secrets consumed as file-path variables; it does not handle `export` consumers (entrypoint scripts) or secrets that need an owner or mode from another service. Other per-app slices should check this.
- S403: Mealie's `BASE_URL` still follows the route host only; a path-served or multi-name Mealie may need it to follow the public name.
- S404: rotating an env-oidc app's client secret does not re-sync Authentik; the configure response warns, and applying sign-in again fixes it. Chaining the re-sync needs the catalog to call the sign-in operation (an import cycle today).
- S404: the warning about owner values sign-in replaces appears at plan time and in the audit; the operation step results carry no notes, so the dashboard cannot show the "left in place" note after turning sign-in off.
- S404: turning sign-in off removes a variable the owner set to exactly the value sign-in sets (the override cannot tell the two apart).
- S405: an env-oidc app on the local issuer that also needs public roots (a later issuer switch handled by the move) is not covered by a combined bundle; only the local issuer sets the trust variable.
- S405: an existing Mealie install keeps a root-owned secret file until sign-in is applied again.
- S406: a sign-in plan reads "Nothing changes" when only the override file is stale (e.g. a trust variable left from before S405); the apply fixes it. Plans should compare the override's sign-in keys too.
- S407: apps served by path (Nextcloud's `OVERWRITECLIURL`, Immich and others under `/apps/<svc>/`) keep the local base URL after they are published; `{{app_url}}` is only rendered public for apps with a name of their own. A path app that needs the public base URL would need its own token and plan step.
- S407: with several public names on one route the base URL follows the first name; choosing which name an app builds its links on is not offered.
- S407: the dashboard lists the base-URL change in the plan's app settings but has no dedicated "refresh" button; the already-public fix is the same public add plan (CLI: `apps public plan|apply <app> --hostname <existing public name>`).
- S409: Linkding has no group claim, so every Authentik person becomes an ordinary user; the first superuser stays the local `admin` and nobody is promoted from Authentik groups.
- S410: the combined trust bundle is built from the appliance host's system CA bundle only when sign-in is applied, so host CA updates reach Linkding on the next re-apply, not automatically.
- S411: Komga has no group or role mapping, so every Authentik person it creates is an ordinary user; the first admin stays the local recovery account.
- S411: Komga signs in with explicit endpoints and no `issuer-uri`, so its OIDC `iss` check is skipped (Authentik still signs the token with the JWKS key); revisit if Komga stops needing startup discovery.
- S411: the Java trust store is built from the appliance host's system CA bundle at sign-in apply only, so host CA updates reach Komga on the next re-apply.
- S411: the Komga sign-in button and local-login coexistence are confirmed from source, not yet from a live VM.
- S412: the sign-in plan states the FreshRSS admin rule but does not read the default user from the app; a plan could show the actual username once the app exposes it cheaply.
- S412: the FreshRSS sign-in step needs the web installer finished; an unfinished install fails the in-app command with guidance rather than waiting like Nextcloud's set-up wait.
- S412: the combined CA bundle laid over FreshRSS's system CAs is built at sign-in apply only, so host CA updates reach it on the next re-apply.
- S412: FreshRSS sign-in is confirmed from source, not yet from a live VM (mod_auth_openidc return URL behind the edge, auto-registration, off restoring the form login).
- S413: Paperless-ngx sign-in gives everyone an ordinary account with no permissions; admin rights and group sync (`PAPERLESS_SOCIAL_ACCOUNT_SYNC_GROUPS`, needs matching Paperless groups created first) are not wired.
- S413: a re-apply that changes only the derived provider file (an issuer move between two private names) does not recreate the running container, so Paperless keeps the old setting until it restarts.
- S413: Paperless sign-in is confirmed from docs and source, not yet from a live VM (login-page button, callback behind the edge, public-name CSRF origins via PAPERLESS_CSRF_TRUSTED_ORIGINS if needed).
- S413: `oidc_client_secret` is immutable here; rotating a client secret is a re-apply of sign-in, not a secret rotation.
- S414: Vaultwarden sign-in sets no refresh-token scope (Homestead's Authentik provider has only the authorization-code grant and no `offline_access` mapping), so `SSO_AUTH_ONLY_NOT_SESSION=true` keeps Vaultwarden's own session lifetime; revisit if sessions should follow Authentik.
- S414: sign-in completes only at the address Vaultwarden's `DOMAIN` names (the app address); other routes of the same app are listed as redirects but fail inside Vaultwarden.
- S414: Vaultwarden sign-in is confirmed from release notes, wiki and source, not yet from a live VM (Authentik `email_verified`, existing-account email match, 1.34.3 to 1.37.4 data migration).
- S414: no Authentik group to Vaultwarden organization mapping; SSO only signs people in, organizations and master passwords stay in Vaultwarden.
- S409: a Linkding stack from before S349's supervisor (no entrypoint in compose.yaml) is not repaired by the secret wiring; reinstall it.
- S409: OIDC accounts match existing Linkding users by email; an Authentik person with no email gets a new account rather than the matching one.
- S415: Calibre-Web Automated sign-in depends on CWA's internal `app.db` schema (`oauthProvider`, `settings`) and on `/app/CWA_RELEASE`. Every CWA package update must re-verify the script first; a pin test (`TestCWASignInPinnedToVerifiedVersion`) fails until it is, and the script refuses another version or a missing column. If CWA gains an environment or file configuration for OAuth, replace the script with it.
- S415: Calibre-Web Automated stores the redirect host in `app.db`, so an address change (public refresh or move) needs sign-in planned and applied again; Homestead does not re-run the script by itself. The script also assumes `/app/CWA_RELEASE` reads like `v4.0.8`; if it is absent it relies on the column check alone.
- S415: Calibre-Web Automated's client secret sits in plaintext in `app.db` (application storage and backups); the app has no secret-file form. Group-to-role mapping is only admin (Homestead admin group at each sign-in); non-admin roles stay at CWA's default role.
- S415: sign-in off clears the generic provider's client id, secret, discovery URL and admin group, and turns login type and group admin management off; it leaves the stored redirect host and button text, which are harmless.
- S415: the Calibre-Web Automated sign-in script and restart are confirmed against the image's source and a v4.0.8 container's schema, not yet by a full live apply on VM 2 (restart timing, `/app/CWA_RELEASE` content, `groups` claim).
- S416: Gitea sign-in depends on Gitea's internal `login_source` schema (nine columns, type 6 = OAuth2, the `oauth2.Source` JSON) and on `gitea --version`. Every Gitea package update must re-verify the script first; a pin test (`TestGiteaSignInPinnedToVerifiedVersion`) fails until it is, and the script refuses another version or a missing column. If Gitea gains a secret-file or stdin form of `admin auth add-oauth` or an admin API for sources, replace the script with it.
- S416: per-person `none` is not enforced for Gitea: local registration stays open (`DISABLE_REGISTRATION` false) and local passwords keep working, and sign-in has no required-claim gate. Closing registration needs a proven local recovery administrator first (S391 notes); a `RequiredClaimName`/`RestrictedGroup` mapping from Homestead's `user` and `none` choices is also deferred.
- S416: Gitea's client secret sits in plaintext in `gitea.db` (application storage and backups); the source has no secret-file form. Off clears it and deactivates the source but leaves the row, so linked accounts keep their link.
- S416: Gitea sign-in off leaves `external_login_user` links and every account in place; a Gitea account created through Authentik has no local password until the person sets one.
- S416: an Authentik group is mapped to Gitea administrators only; no group-to-organization or team mapping (`GroupTeamMap`) and no SSH key or avatar sync.
- S416: Gitea reads its discovery document at startup, so Authentik must be reachable when the app restarts after apply; if it is not, Gitea logs the error and the source is not offered until the next restart. Homestead does not re-run the script by itself.
- S416: the Gitea sign-in script and restart are confirmed against the v28.2.0 source and a live gitea/gitea:28.2.0 container with a stand-in discovery server (row written, listed by `gitea admin auth list`, `/user/oauth2/authentik` redirects to the authorize URL), not yet by a full apply against Authentik on VM 2 (token exchange, account linking page, admin sync, TLS trust with the combined bundle).
- S417 Audiobookshelf sign-in must be re-verified on every Audiobookshelf update (schema, writer, redirects, groups, logging); `TestAudiobookshelfSignInPinnedToVerifiedVersion` forces it. Revisit if upstream gains a secret-safe settings API or file.
- S417 With no automatic account matching, a person whose Authentik username equals an existing Audiobookshelf username gets a second account (the app does not check). No per-person "none" or link choice yet.
- S417 The OpenID client secret sits in plaintext in `absdatabase.sqlite` (and its backups); the app offers no other store.
- S417 Audiobookshelf logs the signed-in person's profile (name, email, groups) at info level on first sign-in; owner accepted, no mitigation available.
- S417 Live check on VM 2 pending: Audiobookshelf is not installed there. Needs token exchange, `NODE_EXTRA_CA_CERTS` trust, the groups claim, mobile redirect, recovery login after apply and re-apply after an address change.
- S417 Audiobookshelf advanced permissions claim and the `guest` role are not mapped.
- S417 Turning sign-in off leaves accounts created by it; they keep a link and can no longer sign in with OpenID.
- S418 Forward-auth is wired for Navidrome only: MariaDB-backed apps, a second (non-embedded) outpost and other apps are not done (Kuma is S419).
- S418 The trusted source was the edge subnet, not a /32 pin of the edge container; closed in S420 (edge /32, drift reported as re-plan and a Doctor warning).
- S418 ADR-054 install-review does not yet show forward-auth apps (`installAccessMode` still says self-managed for Navidrome).
- S418 `ReadAccounts` runs `docker cp` on every status call for an enabled gate; no cache.
- S418 Owner username is not in the people table, so owner/local-account collisions are not checked; a person added later whose username matches an existing Navidrome account is only covered by a plan warning.
- S418 Moving a gated app to a path route fails validation.
- S418 Live check on VM 2 pending (see the slice file's "Verify live").
- S420 Sign-in status gives "the app's names changed" as the re-plan reason when only the edge address moved; it should name the trust change.
- S419 Kuma's own login stays on behind the gate (two sign-ins). The route back to a single login is a host firewall rule restricting non-root access to Kuma's port (the host reaches containers through the bridge gateway, including `--internal` networks on Docker 26); then `disableAuth` could be reconsidered.
- S419 Kuma's Trust Proxy setting (client addresses in Kuma) is a manual step and is not set.
- S419 MariaDB-backed Kuma and Prometheus scraping of `/metrics` through the gate are not handled.
- S419 ADR-054 install review does not yet show Kuma's admin-only gate.
- S419 Live check on VM 2 pending (see the slice file's "Verify live").
- S419 Apply reports success a few seconds before the embedded outpost has loaded a new provider; the gate answers 404 (fail closed) until then. The apply could wait for /outpost.goauthentik.io/auth/caddy to redirect for the host.
- S357 Vikunja team membership from Authentik groups (the `vikunja_scope` mapping and `vikunja_groups` claim) is not wired; Vikunja has no administrator role to map, so every person signs in as an ordinary user.
- S357 CalDAV against Nextcloud is not wired: Vikunja serves its own CalDAV and the owner adds it in the calendar app. A guided hand-off is not built.
- S357 Sign-in is not exclusive: Vikunja's local login and open registration stay on (`auth.local.enabled`, `service.enableregistration` untouched); an exclusive mode is not offered.
- S357 Vikunja's OpenID provider cannot be a `*_FILE` or environment value, so the client secret lives in a rendered config file in the secret store (backed up with Homestead's secrets); a Vikunja update must re-verify the file shape (pin test).
- S357 Live check on VM 2 pending (see the slice file's "Verify live").
- S357 Vikunja cannot run under a path, so it keeps a name of its own (`vikunja.<zone>`), like Immich; Mailer, reminders by email and migration importers are not configured.
- S358 Actual Budget takes no role from Authentik groups: the first person to sign in becomes the server owner and administrator (permanent in Actual), later people are ordinary users (`ACTUAL_USER_CREATION_MODE=login`). Mapping groups to Actual roles is not possible upstream.
- S358 Actual stores the OpenID client secret in its own `account.sqlite` (auth table, written by Actual at startup from the `_FILE` secret), so it rides in the app's data backups; not avoidable from Homestead.
- S358 Sign-in is not enforced (`ACTUAL_OPENID_ENFORCE` unset): a server password set before sign-in stays as a second login. Turning sign-in off runs a verified script that deletes Actual's OpenID row from `account.sqlite`; an Actual update must re-verify it (pin test). With no prior password, off makes Actual ask for a new server password, and budgets stay owned by the OpenID user.
- S358 Live check on VM 2 pending (see the slice file's "Verify live"). Actual cannot run under a path, so it keeps a name of its own (`actual-budget.<zone>`); bank-sync providers (GoCardless, SimpleFIN, Pluggy) are not configured.
- S359 Memos takes no role from Authentik groups: people created at first Authentik sign-in are always ordinary users, and there is no Authentik-to-administrator mapping upstream. The administrator is the first account created in Memos with a password (a bootstrap user in the configuration file would need an extra generated secret; not wired). Linking an existing Memos account to Authentik is done by the owner in Memos settings.
- S359 First Authentik sign-in creates accounts only while Memos allows user registration; Homestead does not manage Memos's `GENERAL` instance setting (it would take over the whole group and make it read-only), so an owner who disabled registration must re-enable it for new people.
- S359 Turning sign-in off destroys the managed provider through a `removed` entry; if the owner disabled password sign-in in Memos, Memos refuses that plan and will not start (authentication invariant). The plan warns; an off-time guard is not built.
- S359 Memos stores the client secret in its own database (`data/memos_prod.db`, backed up with the app) and in the private secret file. The provider is read-only in Memos while managed.
- S359 Live check on VM 2 pending (see the slice file's "Verify live"). Memos cannot run under a path, so it keeps a name of its own (`memos.<zone>`). Not configured: S3 storage, webhooks, instance access mode, group-based sign-in denial beyond Authentik app groups.
- S360 Image signature and build-attestation verification at install or update is not built; provenance is checked by hand at slice time (ADR-056 §5) and most publishers, LinuxServer.io included, publish none.
- S360 Manifests pin tags, not digests (update discovery compares tags, S343). A rebuilt tag is only noticed at review time; digest pinning with drift detection is deferred.
- S360 Multi-arch, maintenance and source checks are human-verified and recorded in each slice file; no registry automation checks them, and existing packages were not backfilled with digests or platform lists.
- S360 The publisher is not shown to the owner in `apps show`, the plan or the dashboard; only the manifest and the catalog test carry it.
- S360 The LinuxServer `FILE__` secret convention versus the ADR-052 secret entrypoint is confirmed per package in S361-S366, not decided here.
- S421 Other containers sharing a network with an auth-off app can still reach its port (ADR-055 §5 residual); a dedicated app-only network is the real fix and is not built.
- S421 The gate table is applied by the daemon's own reconcile and a five-minute pass, not persisted across reboot by an nftables unit; between boot and the first pass (Docker permitting) an auth-off app is unprotected. S422 must not turn a login off until it accepts that window or a boot-time rule is added.
- S421 With `firewall_mode=external` the rules are not applied and no script for them is shown in the firewall plan; owners get the guidance in Doctor only.
- S421 `nft --check` of the gate rules is skipped in unprivileged test runs, so the nft syntax (`ct status dnat`, `iifname != "br-*"`, `meta skuid`) is first confirmed on the VM.
- S421 The host block drops non-root access to unpublished ports of every Docker container, not only Homestead's (owner-overridable with `firewall_mode=external`); a per-container allow list is not built.
- S422 Only the built-in SQLite database is supported for Kuma's own-login-off; a Kuma on MariaDB (embedded or external) is refused with guidance and keeps its login.
- S422 The Kuma script writes Kuma's own database and must be re-verified on every Kuma update (pin test `kumaSignInVerified`); the `/app/node_modules/@louislam/sqlite3` path is first confirmed on VM 2.
- S422 The Kuma account has a random unknown password; the owner who wants Kuma's own login back sets one with `npm run reset-password`. A Homestead-run password reset is not built.
- S422 The boot window before the daemon's first host-block reconcile (accepted by the owner) and containers sharing a network with Kuma (S421 residual) are not closed.
- S422: the forward-auth sign-in plan opens with "Nothing changes: the Authentik gate is on…" even when the own-login-off step is still pending (seen live on VM 2); the summary line should account for ownloginoff.
- Wave 3 (owner 2026-11-01, after S422): the *arr suite is deferred — S361 Prowlarr, S362 Sonarr+Radarr (bundled, S363 folded in) and S364 SABnzbd. When resumed they follow the S422 pattern (admin-only gate, requireshostblock, own login off, API paths bypassed with API keys). S365 Seerr is deferred with them (owner 2026-11-01); it sends requests to Sonarr/Radarr.
- S366 BookStack sign-in must be re-verified on every BookStack update (roles table, `GroupSyncService` role matching, OIDC env names, callback path); `TestBookStackSignInPinnedToVerifiedVersion` forces it. Revisit if BookStack gains an env or file source for role External Authentication IDs.
- S366 Live check on VM 2 pending: install, default-admin refusal, sign-in apply/off, roles, discovery over the appliance CA, backup dump (commands in the slice file). The sign-in warnings assume the redirect URI list has the same origins pattern as other env-OIDC apps; confirm in the browser.
- S366 The seeded administrator keeps a generated password nobody sees unless revealed; no Homestead-run reset. BookStack mail, LDAP/SAML, per-shelf permissions and a person-to-existing-account link are not built (an existing account with the same email blocks that person's sign-in).
- S366 The `docker exec` one-liner for the database check in the slice file reads the password from the mounted file; a CLI database shell is not built.
- S367: the bridge store is last-write-wins per key with no compare-and-set; Tasks heals concurrent edits by record-level merge, but a stricter revision check could be added later.
- S367: Authentik provider and application provisioning for the bridge client (client id, secret file, redirect URI) is not done by the daemon; the bridge only consumes them.
- S367: the Tasks service worker no longer caches the app root, index or any `_homestead/` path (navigations are network-only with a token-free offline page; hashed static assets stay cache-first), and the shim refreshes a stale CSRF token from `GET _homestead/session`. Consequence: the app shell does not load offline (a reload while offline shows the offline page); only an already-open page keeps its offline queue. A real-browser check exists (`scripts/everything/gate/run_browser_check.sh`, dev identity only) but the OIDC path in a real browser and the Safari/Firefox workers are untested.
- S367: the Tasks logout button is hidden upstream; the shim `auth.signOut` was fixed by S368 (see the S368 line).
- S367: the OIDC bridge gate (the six bridge gate tests against the fake-provider verifier) is not built; the gate still uses the development verifier, and OIDC is covered by Go tests with a fake provider.
- S367: the bridge accepts only RS256 and ES256 ID tokens, requests only the `openid` scope and uses `sub`; Authentik providers without a signing key (HS256) are not supported.
- S367: Tasks (upstream, shared with real Puter) mints a dataset epoch per device on first boot of an empty account. Two devices booting at the same moment each seed their own epoch and the later-minted one replaces the other wholesale (`store.js` `#mergeCloud`, "adopt whichever dataset was minted later"), so the loser device's pre-sync edits are dropped; both converge. The gate boots the second device after the first has synced and has a separate test pinning the simultaneous-boot behaviour. The bridge is not at fault (6/6 then 7/7 gate tests pass).
- S367: `run_compose_check.sh` cannot sign in (no identity provider in the stack); sign-in is covered by Go tests with a fake provider and the browser check with the dev identity. A real-Authentik run over Compose remains.
- S367: quotas count key plus value bytes and compute usage with a SUM per write; fine at appliance scale, add running counters if a person's object count grows large. Exports run as root on a volume owned by uid 65532, so their output is root-owned unless the caller changes it.
- S367: image reproducibility is shown on one machine with one Docker builder (identical layers and, with `SOURCE_DATE_EPOCH`, identical image ID); `npm ci` fetches from the registry and relies on the lockfile integrity hashes, there is no offline mirror.
- S367: finished as a spike (owner decision 2026-11-02): Tasks runs on VM 2 under plain Docker Compose with a hand-made Authentik client and a separate Caddy container on port 8443. Not done and left for a later slice: an ADR and a catalog package for Everything apps, daemon-provisioned Authentik provider and application, an edge route at the appliance name (`/apps/tasks/`), access policy bindings (closed by default), Content-Security-Policy headers, and a cross-engine reproducible image (VM 2's Docker 26 and the local Docker 29 built different image IDs from the same source).
- S368: the Tasks logout button is still hidden upstream (`env` is `app`); the shim's `auth.signOut` now POSTs the bridge logout with CSRF, so a later slice can show a Homestead sign-out control.
- S368: the bridge `ORIGIN` is one `https://{{route_host}}`; a public-name origin (tunnel name) is not handled for Everything apps, so Tasks works at the appliance name only.
- S368: the backup copies `data/` as is; a hook-based consistent SQLite snapshot (online backup before copy) is not wired.
- S368: the Content-Security-Policy and the full sign-in flow are covered by Go and gate tests but have not been checked in a browser; the review plan is the check.
- S368: bridge images are not byte-identical across Docker engines, are not in the update flow or `Prune`, and a new pinned archive or version bump is a manual catalog edit plus `sync-context.sh`.
- S368: the dashboard and CLI craft status do not list bridge apps separately; they are built with the existing craft plan and build calls by app name.
- S369: the upstream Projects tests were not run against the real bridge; the real-browser check and Go/gate tests cover the bridge path only.
- S369: not run on VM 2 or through Authentik; the review plan S369 section is the owner checklist.
- S369: cloud-drive picking, sharing and calendar feeds are not offered (no `ui`, `fs.getReadURL`, sharing); a later slice may add them with an exposure plan.
- S369: the 8 MiB per-attachment limit is the app's own; the bridge enforces only the per-person and per-app quotas.
- S369: `modified` is the bridge write time in Unix ms, strictly increasing per rewrite; restoring a backup does not preserve upstream timestamps.
- S370: the upstream Records tests were not run against the real bridge; the real-browser check and Go/gate tests cover the bridge path only.
- S370: not run on VM 2 or through Authentik; the review plan S370 section is the owner checklist.
- S370: Records has no service worker (upstream has none), so the page cannot be reopened offline; a later slice may add a runtime worker as for Projects.
- S370: the app runs with `env` set to `app`, so it shows no sign-out button; sign-out is the Homestead sign-out route until a small patch adds a menu entry.
- S370: attachments over 8 MiB fail with a generic "Could not upload the file" message; only the per-person and per-app quotas map to the storage-full wording.
- S371: the upstream Notes tests were not run against the real bridge; the real-browser check and Go/gate tests cover the bridge path only.
- S371: not run on VM 2 or through Authentik; the review plan S371 section is the owner checklist.
- S371: Notes has no service worker (upstream has none), so the page cannot be reopened offline; a later slice may add a runtime worker as for Projects.
- S371: the app runs with `env` set to `app`, so it shows no sign-out button; sign-out is the Homestead sign-out route until a small patch adds a menu entry.
- S371: AI assist, read-only public page links and picking from a cloud drive are not offered; AI would need a local model backend and links an exposure plan, both a later slice.
- S371: the unreachable publish handler and `backend.readURL` remain in the built source; they are refused by the bridge if ever called.
- S372: the upstream Contacts tests (313/313) run against the pristine tree with a mock; they were not run against the real bridge, and the image build skips them (patched Smart add tests would fail), so a changed pin must re-run them by hand.
- S372: not run on VM 2 or through Authentik; the review plan S372 section is the owner checklist.
- S372: Smart add (needs an AI backend), "Add birthday to Calendar" and the Calendar suggestions (need the Puter desktop and a Calendar app) are not offered; a local model backend or a Homestead calendar link would be later slices.
- S372: no browser-native replacement was added for the Puter desktop file drop or window title: OS drag and drop, the Import file picker and the export download link already work in a browser and are used.
- S372: key-only `kv.list` still stops at 1000 entries; only the with-values form pages. No Everything app lists more than 1000 keys without values today.
- S372: the store screenshots (about 5 MB) stay in the archive and the image because patches cannot delete files; the manifest no longer lists them. The unused `.well-known/mcp.json`, `robots.txt` sitemap line and social-card meta tags still name contacts.puter.com.
- S372: the sign-in gate and service-mode panel in `main.js` still point at an absolute `/icon.svg`; neither is reachable under the bridge.
- S372: a large address book (a few thousand contacts) loads in one page-by-page read but the app re-reads all of it on each load; no paging in the UI, and save while offline is not queued (the app shows an error).
- S372: the account menu shows no identity because the bridge's user name is the opaque sign-in subject; a friendlier name needs the OIDC email passed through `whoami`.
- S373: the upstream Inventory tests (722/722, 34 files) run against the pristine tree with a mock; they were not run against the real bridge and a changed pin must re-run them by hand.
- S373: not run on VM 2 or through Authentik; the review plan S373 section is the owner checklist.
- S373: barcode scanning with the camera (`getUserMedia`) was not exercised: headless Chrome has no camera. It needs HTTPS, which the appliance serves, and no Permissions-Policy blocks it; a hand check is in the review plan.
- S373: the app keeps its own browser-side mirror (IndexedDB `inv-offline`) of the person's inventory and a write queue, so a signed-in person's data stays in that browser until a different person signs in there (the app then wipes it). Sign-out is hidden (`env` is `app`) so it never runs the app's own wipe; a Homestead sign-out that clears it is a later slice.
- S373: with the Homestead service worker an offline page load shows the offline page, not the app shell, so the app's offline-first start (open the installed app with no connection) does not work; already-open tabs still queue writes.
- S373: the whole inventory is a few large KV values (`inv:products:v2`, activity and so on, 8 MiB each at most) rewritten on change; a very large inventory could hit the value limit.
- S373: the unreachable landing page and some error strings in the built JS still say Puter and link to other sites (promotional links, not scripts); the visible onboarding, settings and product form strings are patched.
- S373: the public handbook and guides (about 50 pages upstream) are not shipped; a local help page would be a later slice.
- S373: key-only `kv.list` still stops at 1000 entries; the app lists by prefix without values only for small prefixes.
- S374: the upstream Time tracking tests (205/205, 10 files) run against the pristine tree with a mock; they were not run against the real bridge and a changed pin must re-run them by hand.
- S374: not run on VM 2 or through Authentik; the review plan S374 section is the owner checklist.
- S374: the app keeps a localStorage mirror (`hourly:cache:v1`) of the person's entries and a write queue, so a signed-in person's data stays in that browser until a different person signs in there (the app then wipes it). Sign-out is hidden (`env` is `app`), so the app's own wipe never runs; a Homestead sign-out that clears it is a later slice.
- S374: with the Homestead service worker an offline page load shows the offline page, not the app shell, so the app's offline-first start does not work; already-open tabs still queue writes.
- S374: the app skips its service worker on localhost and 127.0.0.1 (upstream), so a loopback-only appliance install gets no worker; the normal install serves a real host name.
- S374: the app opens with an automatic backup (a full snapshot, last 8 kept) into `hourly-data/backups/` and re-writes whole month files; each person's quota (256 MiB) bounds both, and a very large backup history is not trimmed by Homestead.
- S374: forgot-to-stop system notifications need the browser's permission and a page that stays open; not exercised.
- S374: the unreachable sign-in view and the sign-out confirmation text and some error strings (for example the stub used when the SDK cannot load) still say Puter; the visible onboarding, Settings and main error strings are patched.
- S375: the upstream Circuits tests (207/207, 20 files, jsdom) run against the pristine and the patched tree with mocks; they were not run against the real bridge and a changed pin must re-run them by hand.
- S375: not run on VM 2 or through Authentik; the review plan S375 section is the owner checklist.
- S375: the browser keeps a "recent projects" list of project names and the last-opened location in localStorage, so a second person in the same browser sees the first person's recent project names (opening one fails; the files are private on the appliance). A Homestead sign-out that clears it is a later slice.
- S375: upstream ships and registers no service worker and the bridge's worker mechanism only replaces an existing one, so Circuits has no offline page and is not installable as a PWA; a lost connection shows the browser's error page and a save while offline fails with a message.
- S375: sharing a project with other people is hidden (it needs `fs.share`, which ADR-057 refuses); the Puter file picker, "Open from the desktop" and the profile picture are absent.
- S375: the board's WebGL2 rendering, the 3D view, the autorouter, KiCad import, Gerber, drill and fabrication export, printing and the component editor were not driven in a browser; only the kernel (design rule check, copper pours) is exercised.
- S375: the page policy now allows `'wasm-unsafe-eval'` for any app whose build ships a `.wasm` file (WebAssembly compilation only; JavaScript eval stays refused); a policy per app in the catalog would be tighter.
- S375: the committed `kernel.wasm` is upstream's prebuilt binary; the image build does not rebuild it from the Rust source in the archive, so the binary is trusted at the pin. A reproducible rebuild check is a later slice.
- S375: the project file is written whole on every save (autosave included) and each save keeps a version snapshot under `Projects/.versions/`; the per-person quota (256 MiB) bounds both and old snapshots are trimmed only by the app's own rule.
- S375: the unreachable sign-in strings, the keyboard-shortcuts and getting-started text and some error strings still mention Puter; the visible start page, account, open-project, About and status bar strings are patched.
- S376: not run on VM 2 or through Authentik; the review plan S376 section is the owner checklist.
- S376: the OpenCascade kernel (`replicad-opencascadejs`, LGPL-2.1-only) comes from npm at image build time and is not rebuilt from source; the licence texts ship in `public/licenses/` and the in-app source page (S423) links the upstream source at the bundled version.
- S376: the 23 MB wasm is served uncompressed and the app has no service worker, so there is no offline page and the first load is heavy.
- S376: sketching, STEP/STL/SolidWorks import and export, drawings and PDF were not driven in a browser; only boot, an extrude, save, reload and reopen were.
- S376: the "recent files" list is kept in localStorage, so a second person in the same browser sees the first person's recent names (not content).
- S376: the upstream tests (104) ran with mocks, not against the real bridge; the built image was not run in a real appliance.
- S376: sharing and publish are hidden; AI is disabled with no backend (a future AI slice would need an approved bridge).
- S376: the app's unused-name TypeScript rules are relaxed so patched code compiles; unreachable strings still mention Puter.
- S377: scenes, libraries, presets, snapshots and the autosave stay in the browser's IndexedDB (and the browser file system), separated by person with a per-person database name but not encrypted, not backed up and not on the appliance; clearing site data or changing browser loses them. Only settings (localStorage) go to the person's bridge storage, capped at 3 MB.
- S377: concurrent tabs of one person overwrite the settings snapshot (last write wins).
- S377: Python (Pyodide from a public CDN) and AI are off. The script console, Run Script and style modules need `unsafe-eval`, which the policy refuses, so they report that they cannot run; the "Allow Online Access" preference text still mentions them.
- S377: no service worker, so there is no offline page; the first load is about 8 MB of script.
- S377: folder access ("Open Folder...") needs the File System Access API and works only in Chromium; other browsers get "Add Files..." and downloads (upstream behaviour, checked only by reading the code and by removing the API in headless Chrome, not in Firefox or Safari).
- S377: exported files still say "Puter 3D" in their metadata and OBJ exports carry a `https://puter.com` comment; they are file-format identifiers (the USD reader depends on one) and were left unchanged.
- S377: the Dockerfile guard was not extended for jsdelivr; the Pyodide reference is removed by patch and checked in the prepared tree and the built output by the tests.
- S377: upstream's 3,018 tests ran against mocks, not the real bridge; the headless Chrome check covers boot, settings and database separation, reload and a second person, not modelling, rendering (no WebGL check), import or export.
- S377: not run on VM 2 or through Authentik; the review plan S377 section is the owner checklist.
- S377: a recovered autosave was read from the person's database on reload but upstream's own startup race (a font-load change before the read returns) can skip applying it; it was seen in headless Chrome and not investigated further.
- S378: not run on VM 2 or through Authentik; the review plan S378 section is the owner checklist.
- S378: the catalog icon is `mdi:image-multiple` because no cards or games icon is bundled in app-icons.ts; owner may bundle a better one (for example a playing-cards icon).
- S378: the browser keeps a copy of the saved game and statistics (separated by person, not encrypted); the appliance copy is the one that is backed up. Anything written to unprefixed localStorage keys by an earlier visit is left in place, ignored.
- S378: the headless check plays one stock move, not a game to a win; touch and drag play, undo and the Vegas bank are covered only by upstream's 209 unit tests, not by the real bridge.
- S378: Solitaire shows no user name; if upstream adds one, it must show "Homestead user", not the subject.
- S379: not run on VM 2 or through Authentik; the review plan S379 section is the owner checklist.
- S379: the catalog icon is `mdi:image-multiple` because no grid or puzzle icon is bundled in app-icons.ts; owner may bundle a better one.
- S379: the browser keeps a copy of records and the puzzle in progress (separated by person, not encrypted); the appliance copy is the one that is backed up. The inline theme script reads one unprefixed settings key (never written), so a person's theme choice applies after the app starts, not before first paint.
- S379: the headless check places one digit, not a full puzzle to a solve; the daily puzzle, hints, pencil marks, touch input and the solved dialog are covered only by upstream's 333 unit tests, not by the real bridge.
- S379: the daily puzzle uses the browser's local date; two people see the same puzzle by design. Nothing online is involved.
- S380: not run on VM 2 or through Authentik; the review plan S380 section is the owner checklist.
- S380: the AI receipt reading ("AI OCR") is not shipped and no local OCR replaces it: every field of an expense is typed by hand. A local or owner-run reading engine would need its own slice and ADR.
- S380: receipts are limited to 8 MB each (the bridge's per-file limit; upstream allowed 10 MB). A PDF receipt cannot be previewed in the app (the page policy has no frame source); it can be downloaded. The headless check stores two image receipts (one just under 8 MB) and refuses one over; PDF receipts, the receipt viewer and download are not driven in a real browser.
- S380: the browser keeps only the chosen theme (separated by person, not encrypted); everything else is on the appliance. The inline theme script reads one unprefixed key that is never written, so a person's theme applies after the app starts, not before first paint.
- S380: the catalog icon is `mdi:piggy-bank-outline` (also Actual Budget's) because no receipt or cash icon is bundled in app-icons.ts; owner may bundle a better one.
- S381 (`api-client`): deferred by the owner on 2026-11-02. It needs the bridge to make network requests for the person (`puter.net`), which no other Everything app has and which could reach the local network unless fenced. **Un-deferred by the owner on 2026-11-03 under ADR-058** (Accepted): any person with app access, public addresses only after DNS resolution with the connection pinned, redirects re-checked, size and time limits, every request logged. Packaged after the `net` slices (S434, S435) as S436.
- S380: the headless check adds expenses and receipts; reports, statement import and reconciliation, the dashboard totals, CSV and JSON export, backup restore and sample data are covered only by upstream's 79 unit tests, not by the real bridge. Delete all data now removes the receipts folder recursively; that path is covered only by the pinned patch test, not driven in a real browser.
- S423: the source offer was proven with Go tests, a Python test and a headless Chrome check against the dev bridge; it was not run on VM 2 or through Authentik, and the image build was run once for one app.
- S423: only the bridge's own `GET` route serves the offer; a person who is not signed in gets a sign-in redirect (page) or 401 (tarball). That is a deliberate reading of AGPL section 13 (the offer is to the people who use the app, and all of them are signed in); the owner may widen it.
- S423: the offer covers the app, Homestead's patches, added files, the shim and the image recipe. The Go bridge (a separate program, MIT) and the npm dependencies fetched at build time are not in the tarball; their sources are public upstream and pinned by `package-lock.json` in the archive.
- S423: the Parametric offer links upstream source at the bundled version (replicad-opencascadejs 1.1.0 and Open CASCADE Technology) and the licence text served with the app; it does not ship the C++/Emscripten sources or a relinking kit, so a person who wants to replace the `.wasm` module builds it from upstream themselves.
- S423: the Source link is a fixed corner link; it was checked for overlap in headless Chrome at one viewport for the three apps, not in every dialog or on small screens.
- S423: the offer text and links are in English only and the tarball is rebuilt with each image, so its checksum changes when the recipe changes (it is reproducible for the same inputs).
- S425: the registry has no remote surface yet; the daemon runs `people purge` on People removal in S427, and grants, read links and event streams join the purge as S426, S430 and S431 add them.
- S425: a person whose token has no `preferred_username` signs in and can use the app but has no name; the one-line explanation in the page is added with the sharing UI (S428), and until then `getUser` answers "Homestead user".
- S425: the 24-hour recheck of session age for shared operations arrives with the first shared operation (S428); nothing in this slice reads the age.
- S425: the Homestead sign-in plan does not pass `-oidc-groups` yet; the bridge accepts it (flag or `HOMESTEAD_EVERYTHING_OIDC_GROUPS`) and the daemon wiring is left to the slice that changes the Everything package environment.
- S425: the whoami and session answers no longer carry the `sub`; the development identity names people by subject for the gate only.
- S426: the share-attempt rate limit (60 per hour per person per app) and the 24 hour session-age recheck for recipient operations are enforced where the first remote share operation lands (S428), since the store has no caller yet.
- S426: kv `listSharedByMe`/`listShared` cursors, fs `readdir`/`stat`/`rename` through a grant, and `{handle}` list resolution are handler concerns left to the SDK slices; the store provides the resolved owner scope and key.
- S426: no per-grant byte cap; a trusted writer can fill the owner's quota (as ADR-058 accepts).
- S427: a pending purge is retried by a two-minute timer only, not at the moment the app starts; a stopped app that starts leaves up to two minutes of shared access.
- S427: disabling a person in People does not purge (ADR-058 purges on removal and on app revoke only); Authentik refuses new sign-ins and shared operations need a session under 24 hours.
- S427: a person who was never recorded in an app's `people` table (never signed in) matches nothing, so the purge reports done; a later purge of a name reused by someone else is guarded only by cancelling pending purges when access is granted again.
- S428: the definition flag `sharing` is realised as the bridge's `-sharing` flag (env `SHARING=1`, default off); no package or catalog manifest sets it yet, so sharing stays disabled in every shipped app until S437 wires it (prepare_app does not read `sdk_surface.sharing` either).
- S428: `puter.appID` is known only after the first `auth.getUser()` or share reply (or `config.appID`), because serving it synchronously would need a new meta tag in the app shell; S437 should check Tasks' use of it before the first `getUser`.
- S428: `kv.unshare` of an unknown, foreign or already revoked handle answers `subject_does_not_exist` (what Tasks reads as "nothing left to stop"), not `kv_handle_not_found`.
- S428: the share-attempt limiter is in memory per bridge process (a restart resets it); `kv.share.received` notifications and `kv:<handle>:*` events wait for S430.
- S428: with the shim's `kv.share` defined, Tasks reports collaboration mode `kv` even while sharing is off; its UI stays hidden until S437 and a `kv_handles_disabled` reply falls back to links there.
- S429: no per-grant byte cap: a trusted folder writer can fill the owner's quota (ADR-058 accepts this).
- S429: `fs.share.received` / `fs.share.revoked` notifications wait for S430; recipients poll `fs.listShared`.
- S429: `fs.getShares` reports only live direct and folder-inherited grants of the caller's own paths; grants made on a path that later changes between file and folder keep the type recorded at share time.
- S429: Circuits and Parametric still hide their sharing UI and "Shared with me" (S439, S440); opening a shared file with those apps (path `~shared/<id>/<name>`) is untested against the real app code.
- S429: no `fs.copy`/`fs.move`/`mkdir` exist in the shim, so nothing else needed grant support.
- S430: events are in memory per bridge process. Changes made by another process (the `people purge` and `grants revoke` tools, import) reach streams only through the 25 s heartbeat re-check (streams end or drop the handle and send `gap`), not as live events.
- S430: fs revocations caused by deleting or renaming a shared file send no `fs.share.revoked` notice; recipients see it on their next `fs.listShared`.
- S430: the edge (Caddy `reverse_proxy` for Everything routes) was not verified live to stream SSE; see the slice doc. Polling in Tasks covers a buffering edge.
- S430: an open stream re-validates the session each beat, which refreshes the session's last-seen time, so an open tab does not idle out (the absolute lifetime still applies).
- S431: a link minted on the public name and the edge route for `read`/`view` are S433 (done).
- S431: there is no per-app definition switch for read links and no `read_links` entry in `sdk_surface` yet; `fs.getReadURL` works on every Everything bridge. The packages' `refused` lists for Tasks and Notes still name it until S437 and S438 re-enable those UIs.
- S431: rate-limit state is in memory per bridge process (a restart resets it). `X-Forwarded-For` is believed from loopback and private peers by default (`-trusted-proxies`); a bridge whose port is published to a LAN must set it to `none`. Not tested against a live Caddy.
- S431: read links are not exported or imported (only a hash is stored, so a restored link could not be re-issued); the `links` tool and People purge are the only revokers besides deleting the file.
- S431: the viewer route (`/_homestead/view/<token>`) is S432 (done).
- S432: the public name and its edge route are S433 (done). The apps still show their own links until S437 and S438.
- S432: only the two named formats have a renderer. Notes tables are flattened to cells in a row, math is shown as the text the snapshot carries, and Notes images, ink and embeds are a marker. Other formats (Circuits, Parametric, Contacts) need their own renderer slice.
- S432: the viewer output is not cached and is rendered per request (bounded to 1 MiB of output and 4 MiB of HTML per outline); the per-client limiter is the only throttle.
- S433: if the dashboard's public name changes or is removed, the app's stored public read origin and its tunnel route flag stay as they were; the owner must plan the removal and a new exposure by hand.
- S433: a link minted on the public name needs the app itself to be reachable there with a session (an OIDC redirect URI and public sign-in for a published app); until an app is published with sign-in, links are minted on the local name and only opened on the public one.
- S433: the viewer link is not yet shown in the Tasks and Notes UIs (S437, S438).
- S433: the generated Caddyfile for the read-link handle was not run through a live Caddy or a public name; only the daemon's rendered text and the bridge were tested.
- S433: the bridge decides that an origin is a public name by shape (https, a host name, no private suffix); a lookalike private name in the public form would be answered `read_links_not_exposed` rather than refused.
- S434: the net operation, request/response header rules, size caps, per-person and per-app limits, `net_log` and the `net-log` tool are S435; the shim `puter.net.fetch` is S435/S436.
- S435: the bridge switch (`-net` / `HOMESTEAD_EVERYTHING_NET=1`) is set by hand or by the `api-client` package (S436); the daemon does not yet turn it on from the app definition's `sdk_surface`.
- S435: `net.fetch` limits are in memory (a bridge restart resets the per-minute, per-day and concurrency counts); the `net_log` keeps 30 days or 10,000 rows for the whole database, so a busy person can push out older rows of others. The daemon does not yet show `net_log` (the `net-log` tool is the owner's view).
- S435: no real public host or TLS handshake was exercised (tests use a fake resolver and dialer onto local servers); the host firewall egress rule remains S421/ADR-008 defence in depth.
- S436: not run on VM 2 or through Authentik; the review plan S436 section is the owner checklist.
- S436: no successful fetch to a real public host was exercised: the real fence refuses every local address, so the browser check proves the refusal and a node test proves the request/response mapping with a fake `net.fetch`.
- S436: user scripts (pre-request and test scripts) run upstream in a sandboxed `srcdoc` iframe and blob workers; the page policy (no frame source, `default-src 'none'`) probably blocks them. Not observed in a browser and not patched to fail visibly; the review plan step 8 records what the owner sees.
- S436: collections, environments and history live in the browser's IndexedDB (per person only through the person-tagged database name, not encrypted) and are mirrored to the bridge's per-person storage by the app's own sync; clearing site data loses the local copy until the next sync pulls it back. The mirror calls `fs.mkdir`, which the shim does not offer; the app copes, the folder is created by the first write.
- S436: redirects are always followed, cookies are not passed on and only allowlisted response headers are returned (the fence's rules); the app's cookie jar and manual-redirect settings have no effect. The response is limited to 8 MB and a request body to 2 MB, and the per-person limit is 60 requests a minute.
- S436: the package turns the net switch on through the `HOMESTEAD_EVERYTHING_NET=1` environment value in the catalog entry; the daemon still does not read `sdk_surface` to decide it.
- S436: the catalog icon is `mdi:cloud`; the owner may choose a better bundled icon.
- S436: `ui/share-dialog.js` still carries upstream's "Verify your Puter account to share." strings; the dialog is unreachable because sharing is hidden, so they are not patched.
- S437: the Tasks browser check uses the development identity with a people registry entry per login; a live two-person run against Authentik, the `profile` claim shape and a real browser through Caddy (event stream not buffered) are unproven.
- S437: Tasks still carries upstream's in-app shared-list page (`#/shared?u=`) and its "Powered by Tasks on Puter" footer; no link points to it since the view-only link is the bridge viewer.
- S437: the calendar feed (`.ics` read link, up to 365 days) is shown again; whether a calendar client can reach it from outside the LAN depends on the S433 public read-link plan and was not tried with a real client.
- S437: `perms.request` always resolves true for Tasks' own prefix (no consent prompt); the owner may want a per-person consent screen later.

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
- Tunnel DNS route actuation: S246 creates the tunnel and runs its
  connector, but the public hostname's DNS record still routes
  through the DNS reconcile path; proxied tunnel CNAME management
  belongs to a later slice.
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
- Theme selection: S248 defines light, dark, and high-contrast
  themes and previews them on the validation board; no user-facing
  switcher or prefers-color-scheme wiring exists yet.
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
- Storage unregister and unassign: the daemon has no route for either
  (the registry and assignments stores can drop rows, but nothing
  exposes it), so the S268 Storage page and the storage CLI
  offer neither. Re-assigning a class is refused with a 409 package list
  until acknowledged; impact is judged by `<old mount>/<package>`
  directories of installed stacks, and S276 declarations are not consulted.
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

- Production craft signing key: the embedded HAI craft key is a
  generated placeholder whose private half was discarded (same status as
  the platform release key). Until the key ceremony, owners add the
  published or their own key through the craft trust-key routes (CLI `craft trust add`).
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

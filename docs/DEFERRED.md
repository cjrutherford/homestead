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
- Internal CA distribution: the S104 authority mints and signs, but
  installing its certificate into OS/client trust stores (and rotation)
  belongs to the security baseline flow (S222).
- Renewal re-issue (S272, ADR-024): S263 schedules the daily renewal
  check and reports ok / renewal-due / plan-required / expired, but
  nothing re-issues expiring certificates yet.
- Certificate issuance path (found in the S263 review): no daemon,
  API, CLI, or dashboard path issues certificates. `DNS01.Issue` and
  `IssueFor` (internal/cert/dns01.go) are called only from tests; the
  TLS wizard (S106/S226, `internal/cert/wizard.go`) saves strategy and
  proves readiness only, the baseline (S222) mints an internal CA
  without recording it, and nothing consumes the ACME account key or
  writes issued chains for Caddy. `IssueFor` records the ADR-024
  fingerprint and chain (migration 27) and is tested, but until an
  owner-approved issuance flow (plan, preview, approve, ACME account
  custody, chain and key placement) calls it, `cert_issuances` stays
  empty. The renewal status therefore reports an explicit "no recorded
  issuances" state in the API (`recorded_issuances`, `summary`), CLI,
  dashboard, and doctor instead of an empty list. S272 depends on this
  flow. http-01/tls-alpn-01 issuance is not fingerprinted.
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
  replaced only as `<binary>.rolledback` (one copy, overwritten by the
  next rollback) and does not make it the new "previous", so there is no
  one-step roll-forward; rolling back to a previous release whose counter
  was never recorded records the baseline with counter 0 (no rollback
  protection until the next verified apply).
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
- Management TLS provisioning: S222 proves issue-and-verify
  mechanics only; persisting the internal authority and serving
  management TLS from the daemon or proxy belong to later slices.
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
  KVM); prebuilt packaging belongs to distribution (S240).
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

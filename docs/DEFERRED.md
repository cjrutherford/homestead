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
- AdGuard Home install-wizard automation (`POST /control/install/configure`):
  needed before friendly names (S086) work end-to-end against a fresh
  instance; kept out of S085 as version-brittle and unverifiable without
  a live server.
- Route 53 STS session-token flow: the S094 adapter takes static IAM keys
  only; short-lived credentials need a refresh flow no slice owns yet.
- Secret re-key/rotation: the S096 key file has no rotation path; losing
  it strands ciphertext, so rotation needs a re-encrypt flow before the
  first long-lived production secret depends on it.
- Overlay-view DNS application: S098 computes overlay effective tables,
  but no overlay DNS provider exists to receive them; overlay names are
  served through local DNS until a provider lands.
- Internal CA distribution: the S104 authority mints and signs, but
  installing its certificate into OS/client trust stores (and rotation)
  belongs to the security baseline flow (S222).
- Renewal actuation loop: S105 reports renewal-due status, but no
  scheduled runner re-issues expiring certificates yet; the loop needs
  an owner-visible schedule before production certificates depend on it.
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
- Secret-backed container variables: S157 writes operational values
  only; injecting secret-store values into container environments
  needs its own slice before apps with API keys install cleanly.
- Container device passthrough: S162 detects GPU stacks, but compose
  generation has no devices/runtime fields, so installs cannot pass
  GPUs through yet; planning the fragment belongs with generation.
- ARM SoC transcoding: S162 covers Intel, AMD, and NVIDIA only;
  Rockchip and other SoC bindings need per-board validation.
- Backup encryption at rest: S181 copies plaintext; encrypting backup
  trees (age recipients, key custody, rotation) belongs with restore
  once runbooks exist.
- Backup job triggering: S183 persists schedules and runs retained
  jobs, but no wall-clock trigger (systemd timers or daemon ticker)
  fires them yet.
- ghcr registry checks: S190 discovers Docker Hub tags only; ghcr
  listings need a token flow before Immich updates discover
  upstream.
- CLI updates coverage: S193 serves review and apply on the API
  and dashboard only; CLI list/plan/apply commands over
  the same routes belong with a later updates CLI slice (mirrors
  the S167/S168 catalog split).
- Platform release acquisition: S194 models desired versions only;
  fetching releases, verifying artifacts, replacing binaries, and
  restarting the daemon belong with distribution work.
- Platform update actuation: S194 refuses downgrades and plans
  backups-first, but no operation executes platform moves, rolls
  back failed replacements, or surfaces platform plans on the API,
  CLI, or dashboard yet.
- Authenticated operator API: S200 hardens credentials and sessions,
  but most operator routes still answer without sessions; requiring
  sessions beyond login/logout belongs to a later slice.
- Rotation and lockout surfaces: S200 policy lives in the auth
  service only; change-password and lockout-status affordances on
  the API, CLI, and dashboard belong to later slices.
- Identity provider adapters: S202 defines the integration contract
  only; driving a provider's API (Authentik first, beyond the
  connection check) belongs to a later slice.
- SSO client actuation: S204 records reviewed integration intent,
  but writing provider credentials into application configuration
  has no owning slice yet.
- Dockge route reconciliation: S211 models and links the local-only
  route, but no flow writes it into the proxy yet; the direct UI
  port serves until then.
- Drift command surface, scheduled scans, live-state comparison:
  S212/S213 detect and resolve generated file drift on demand
  through the API and dashboard; CLI affordances, wall-clock
  scanning, and running-container-versus-file comparison belong
  to later slices.
- Docker Engine bootstrap: S220 installs the Homestead kernel
  only; engine installation on clean machines (S071 owns the
  operation) has no bootstrap wiring yet.
- CLI binary installation: bootstrap and install scripts place
  the daemon only; the command binary has no install path yet.
- Remote bootstrap source: no canonical repository URL exists,
  so bootstrap runs from a checkout; curl-pipe installation
  waits on distribution work.
- mDNS coexistence and probing: S221 answers while the port is
  free and warns otherwise; sharing 5353 with Avahi, name-conflict
  probing, and IPv6 advertisements belong to later slices.
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
  only; API, CLI, and dashboard affordances for reviewing and
  approving exposure belong to later slices.
- Validation surfaces and external vantage: S228 validates from
  the appliance with caller-named management hosts; scheduled
  re-validation, status surfaces, and best-vantage external
  probing belong to later slices.
- Backup configuration surfaces: S229 acceptance stops at a
  backup note because backup destinations and schedules have no
  CLI/API surface; driving them belongs to later slices.
- Corrupt-database repair: S230 snapshots state and refuses a
  missing database as disaster recovery; repairing corrupt
  state belongs to the disaster-recovery flow (S234).

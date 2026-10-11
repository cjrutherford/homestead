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
- Scheduled database backups: S260 fires backup schedules, but
  database passwords are never stored, so scheduled postgres backups
  fail with a recorded error (file-only and sqlite packages run); needs
  secret custody for unattended exports. Also a dashboard surface for
  scheduler state (CLI/API only in S260).
- ghcr registry checks: S190 discovers Docker Hub tags only; ghcr
  listings need a token flow before Immich updates discover
  upstream.
- Platform release acquisition: S194 models desired versions only;
  fetching releases, verifying artifacts, replacing binaries, and
  restarting the daemon belong with distribution work.
- Platform update actuation: S194 refuses downgrades and plans
  backups-first, but no operation executes platform moves, rolls
  back failed replacements, or surfaces platform plans on the API,
  CLI, or dashboard yet.
- Rotation and lockout dashboard surfaces: S239 serves
  rotation and lockout status over the API and CLI; dashboard
  affordances belong to later slices.
- Identity provider adapters: S202 defines the integration contract
  only; driving a provider's API (Authentik first, beyond the
  connection check) belongs to a later slice.
- SSO client actuation: S204 records reviewed integration intent,
  but writing provider credentials into application configuration
  has no owning slice yet.
- Dockge route reconciliation: S211 models and links the local-only
  route, but no flow writes it into the proxy yet; the direct UI
  port serves until then.
- Drift scheduled scans and live-state comparison: S212/S213/S244
  detect and resolve generated file drift on demand through the
  API, dashboard, and CLI; wall-clock scanning and
  running-container-versus-file comparison belong to later
  slices.
- Storage dashboard surface: S235 covers the API and CLI;
  dashboard affordances for mounts, registrations, and
  assignments belong to later slices.
- Theme selection: S248 defines light, dark, and high-contrast
  themes and previews them on the validation board; no user-facing
  switcher or prefers-color-scheme wiring exists yet.
- Visual regression automation: the validation board is checked
  structurally in unit tests; screenshot comparison and an automated
  375px overflow check need a browser harness.
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
- Backup run firing: S238/S267 cover destination and schedule
  records over the API, CLI, and dashboard; scheduled run firing
  belongs to later slices.
- Backup-run restoration: S234 rebuilds from export documents
  plus suite reinstalls; restoring application data from backup
  runs awaits the same backup surfaces.
- Corrupt-database repair: S230 snapshots state and refuses a
  missing database as disaster recovery; repairing corrupt
  state belongs to the disaster-recovery flow (S234).
- Storage dashboard surface: S235 covers mount registration
  and class assignment over the API and CLI; dashboard
  affordances belong to later slices.
- Proxy dashboard surface: S237 covers Caddy deployment and
  route reconciliation over the API and CLI; dashboard
  affordances belong to later slices.
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

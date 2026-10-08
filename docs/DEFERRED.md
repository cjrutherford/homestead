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

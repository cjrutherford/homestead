# S382 — Craft build, serve and DNS advertisement validation

## Goal
Prove, on one fresh appliance, that every craft package builds from the
committed signed craft manifest, serves through the edge with an internal-CA
leaf, and is reachable **by name** from another LAN client; close the local
DNS advertisement gap that leaves the Craft apps start page links
unresolvable, and fix any build/serve defect the run finds.

## Dependencies
S249,S250,S251,S252,S253,S279,S280,S281,S237,S84,S85,S86,S96,S98,S221

## Scope
Implement only the functionality required for this slice. Preserve all
accepted ADRs and customer-ownership guarantees. Do not pull deferred
features into scope without an ADR and slice update.

This is a validation slice with fixes, not a new catalog package. It runs
after S356 and before S357. Governed by ADR-007 (customer domain and split
DNS), ADR-009 (DNS provider abstraction), ADR-018 and ADR-028 (craft
packages), ADR-030 (local TLS through the internal CA), ADR-031 (Craft apps
start page) and ADR-021 (secret custody). No new architecture is expected:
the slice wires the already-accepted S84-S86 local DNS capability into
route reconciliation. If a different DNS design is needed, supersede the
ADR first.

Why this slice exists:

- S249 to S253 proved build and serve with `curl --resolve`, Chromium
  `--host-resolver-rules` or an ssh tunnel. No run proved that a LAN client
  can resolve `<app>.lan`; the KVM transcripts bypassed DNS.
- Route reconciliation (`internal/docker/routes.go`,
  `internal/server/edge.go`) writes Caddy snippets and records
  `edge_routes`; it never writes a local DNS answer.
  `docker.EnsureNameDefinition`, `jellyfin.LocalRewrite`,
  `docker.AdguardDeployDefinition` and `dns.EstablishSplitDNS` have no
  production caller, and the daemon has no local DNS configuration.
- The Craft apps start page (`internal/server/craftapps.go`) renders
  `https://<route host>/`. From any device without a hosts entry, that link
  fails, so the page promises reachability it does not have.
- The craft manifest is now signed (counter 8, commit `3af901f`); the older
  per-batch "unsigned manifest" notes in `docs/DEFERRED.md` are stale and
  are corrected by this slice.

## Acceptance Criteria

### Build and serve matrix (all 13 craft packages)

- On one fresh appliance and one daemon build, verify the committed
  `catalog/craft/craft-manifest.yaml` with the shipped HAI craft public key
  (no throwaway key, no unsigned copy), then for **wordcraft, gridcraft,
  pdfcraft, deckcraft, vectorcraft, photocraft, lightcraft, figcraft,
  filmcraft, effectcraft, soundcraft, designcraft and cadcraft**:
  - `craft plan` verifies, `craft build --follow` produces
    `homestead/craft-<app>:<version>` and records its digest, and a second
    build of the same manifest is byte-identical (same image ID).
  - The image is stateless and non-root: `Config.Volumes` null, user
    `nginx`, `/healthz` healthcheck, `/tmp` the only mount (tmpfs),
    `docker diff` empty, container `healthy`.
  - `apps install`, route `<app>.lan` (local exposure, TLS internal),
    `scripts/craft-header-check.sh` PASS including gzip through the Caddy
    edge, and the Craft apps start page lists the app with its URL.
  - Remove, reinstall and update (a throwaway bumped version) converge;
    the drift scan is clean after install and after update; the craft image
    stays after uninstall by design.
- Every header-table row and `knownImageVolumes` row is re-checked against
  the current manifest versions and the run; stale "pending" or stale
  version notes are corrected.
- Any build/serve defect found is fixed in the shared place with a test.
  A defect that cannot be fixed in this slice is recorded in
  `docs/DEFERRED.md` and the affected app is called out explicitly in the
  KVM transcript; it is not silently counted as a pass.

### Local DNS advertisement

- The route reconcile operation ensures exactly one local DNS A answer per
  **local-exposure** route: the route host resolves to the appliance LAN
  address through the local DNS provider (S85 AdGuard client). The answer
  uses the same LAN-address selection as split DNS (`internal/dns`
  `applianceLAN`/`servableLAN`), not a container bridge address.
- Overlay routes are left to the overlay DNS path (S115) and public routes
  to the public DNS provider path. The local path never creates a public
  record and never widens exposure.
- Re-running reconcile is idempotent: unchanged routes add nothing, a
  changed LAN address updates the answer, and a route removed from the
  table removes the answer Homestead wrote for it. Foreign rewrites (owner
  or another tool) are never deleted. The previously recorded route table
  is the ownership record; no new schema is added unless the implementation
  proves it cannot be done safely without one.
- Reconcile reloads the proxy once for the whole table, not once per name.
  The per-name four-step `names.ensure` definition is not invoked in a loop;
  the DNS step joins the existing reconcile operation so a partial failure
  names the host and leaves no served-but-unresolvable route reported as
  success.
- After reconcile, resolution is verified through local DNS
  (`VerifyName`/`LookupVia`, dialling the configured resolver, not the host
  resolver configuration) and the result is visible on the existing doctor
  and edge surfaces. If a new API route, CLI command or dashboard surface
  is added, `docs/PARITY.md` is updated and the parity test is green.
- There is exactly one local-name implementation: `jellyfin.LocalRewrite`
  and the unused `EnsureNameDefinition` path are folded into it or removed,
  with tests covering the shared path.
- Local DNS is owner-configurable and never hijacked: a mode selects a
  Homestead-managed AdGuard, an owner-provided local DNS endpoint with
  credentials in the secret store, or advertisement off. With
  advertisement off, routes still serve but the start page and doctor say
  the name is not advertised instead of presenting a broken link.

### Fresh-appliance local DNS bootstrap

- The daemon can deploy AdGuard (S84) as a planned operation and complete
  first-run setup deterministically: either the version-checked install API
  or a pre-seeded minimal configuration. The administrator credential is
  generated and sealed through the S96/ADR-021 secret store, never an
  environment variable or a committed file.
- Owner-supplied upstream resolvers are configuration with a documented
  default taken from the appliance's current resolvers; no hard-coded
  public resolver is shipped.
- A port 53 conflict (for example the systemd-resolved stub) is detected
  and either resolved by binding to the appliance LAN address or reported
  as a doctor blocker with the exact remedy. It is never a silent partial
  setup.
- Doctor reports local DNS state: provider reachable, DNS answering, the
  managed answer count, and any route whose host is not advertised.
- On a fresh appliance, a LAN client configured to use the appliance's
  local DNS resolves `<app>.lan` with no `/etc/hosts` entry and no
  `--resolve`, and the craft page loads over the internal-CA HTTPS leaf.
  The client-side DNS requirement (point the client or the router's DHCP
  DNS at the appliance) is written down.

### Start page and surfaces

- `GET /api/v1/craft/apps`, `homesteadctl craft apps` and the Craft apps
  dashboard page distinguish "route exists and the name is advertised" from
  "route exists but the name is not advertised". The URL is not presented
  as working when local DNS does not answer it; the reason and the fix are
  shown. An app with no route keeps the existing reason.
- If the API shape changes (for example an `advertised`/`url_reason`
  field), the CLI, dashboard and `docs/PARITY.md` change in the same
  commit and their tests cover both states.

### mDNS scope

- The mDNS advertisement is validated and documented: S221 advertises
  `homestead.local` and `_homestead._tcp` only; app hostnames are not mDNS
  names and are not added here. Adding them would require a `.local` naming
  decision and an ADR; it is recorded in `docs/DEFERRED.md` with the
  reason. The slice must not imply that mDNS advertises app names.

### Tests

- Unit tests with a fake local DNS client: rewrite added for a local route;
  no rewrite for overlay or public routes; changed answer updates; removed
  route deletes only the managed rewrite; foreign rewrite preserved;
  idempotent re-run; DNS failure fails the reconcile with the host named;
  one proxy reload for the table.
- Bootstrap tests: managed mode config and sealed credential; external mode
  endpoint and credential; off mode; port 53 conflict detection; doctor
  states; upstream resolver default.
- Start page tests: advertised, not advertised, no route, advertisement
  off; CLI output and dashboard component for each state.
- `go test ./...`, `python3 -m unittest discover -s tests`, dashboard
  `ng test`/`ng build`, `gofmt`, `go vet` and `make lint` green.

### KVM acceptance

- Fresh VM, no manual hosts entry and no `--resolve` anywhere. Deploy
  AdGuard through the daemon, complete setup, install and route the craft
  apps, then from a second LAN client (or the KVM host through a tunnel
  that uses the appliance as its resolver) show:
  - `dig`/`getent hosts <app>.lan` returns the appliance LAN address before
    and after a daemon restart;
  - the app loads at `https://<app>.lan/` with the S280 root and no `-k`;
  - clearing the route stops the name resolving, and an unrelated rewrite
    added by hand survives the reconcile;
  - the Craft apps start page link opens from the second client;
  - `doctor` and the edge/start-page surfaces agree with the observed
    state.
- Record the commands and observed output in `docs/DEVELOPMENT.md`,
  including the 13-app matrix result and any deferred defect.

## Required Documentation
- `docs/architecture/LOCAL_NAME_ADVERTISEMENT.md`: route to snippet to local
  DNS answer to verification; ownership and removal; overlay/public
  boundaries; managed/external/off modes; AdGuard bootstrap and port 53;
  client-side DNS requirement; failure modes; mDNS scope.
- `docs/architecture/CRAFT_HEADER_TABLE.md`: re-check every row against the
  current versions and the run; remove stale pending notes.
- `docs/DEVELOPMENT.md`: an S382 section with the 13-app build/serve matrix
  and the KVM transcript summary, including the second-client DNS proof.
- `docs/PARITY.md`: every added or changed route, CLI command and dashboard
  surface.
- `docs/DEFERRED.md`: remaining gaps (IPv6/AAAA local answers, multi-homed
  answer selection, mDNS app names, DHCP/DNS advertisement to clients,
  AdGuard API version drift, external-DNS mode limits) and correction of
  the stale per-batch craft-signature notes.
- `docs/THIRD_PARTY.md` only if a vendored version changes; the slice
  should not need to change one.
- An ADR only if the implementation needs a DNS design different from
  ADR-007/ADR-009.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to
begin without hidden manual state. One slice, one commit on main.

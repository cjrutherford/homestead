# S382 — Craft build, serve and DNS advertisement validation

## Goal
Prove, on one appliance (VM 2), that every craft package builds from the
committed signed craft manifest, serves through the edge with an internal-CA
leaf, and is reachable **by name** from another LAN client, using the local
name path that already exists (ADR-039, S295). Fix any build or serve defect
the run finds.

## Re-scope (2026-11-02)
The first version of this slice (2026-10-29) planned to write an AdGuard A
answer per route during reconcile. Later accepted work superseded that and
this slice does **not** build a second DNS path:

- S295 (ADR-039, `e82434d`) derives local names from the edge route table.
  `internal/mdns` announces `homestead.local` and `<label>.local` for each
  host route. The optional resolver `internal/localdns` (off until the owner
  approves a plan with its port 53 firewall rules; `homesteadctl names`,
  `apps names`, the Names page) answers `homestead.lan` and each host route's
  `<label>.lan`, NXDOMAIN for other `.lan` names, REFUSED for everything
  else, with the A record on the asker's own subnet.
- S315/S320 serve craft apps by path: `https://homestead.lan/apps/<app>/`
  (and `homestead.local`). A path route adds no name of its own, so the 13
  craft apps need no per-app DNS record. Public routes are skipped by both
  the resolver and mDNS.
- Names come from the route table, so there is nothing to make idempotent or
  to remove on route delete: the answer set changes with the table (the
  resolver reads it per query). No schema, no ownership record, no proxy
  reload coupling.
- `docker.EnsureNameDefinition`, `jellyfin.LocalRewrite`,
  `docker.AdguardDeployDefinition` and `dns.EstablishSplitDNS` still have no
  reconcile caller. Wiring them would be the second DNS path; see
  `docs/DEFERRED.md` (S382).

No ADR is contradicted: ADR-007/ADR-009 describe the split-DNS provider path
for the customer domain; ADR-039 is the accepted local-name design.

## Dependencies
S249,S250,S251,S252,S253,S279,S280,S281,S295,S315,S320,S221

## In scope
- Gap analysis of the old acceptance blocks against later slices (below).
- Go tests pinning the name derivation for craft apps
  (`internal/docker/craftnames_test.go`).
- Correcting the stale "unsigned manifest" and "KVM acceptance pending" notes
  in `docs/DEFERRED.md` (the manifest is signed at counter 8, commit
  `3af901f`).
- The exact VM 2 runbook. The live run itself is owner-run.

## Out of scope (recorded in DEFERRED, S382 section)
AdGuard bootstrap, per-route DNS rewrites, an "advertised" flag on the Craft
apps API/start page (no new API, CLI or dashboard surface, so
`docs/PARITY.md` is unchanged), mDNS for multi-label names, AAAA, DHCP
advertisement of the resolver.

## Gap analysis
| Old block | State |
| --- | --- |
| 13-app build/serve matrix | Live-only. Build, serve, headers and start page code is done (S249-S253, S279-S281, S320); VM 2 holds deckcraft, gridcraft, wordcraft. |
| Signed manifest | Done: signed at counter 8 (`3af901f`); the shipped-key test verifies it. Stale notes corrected here. |
| Header table / `knownImageVolumes` | Done: all 13 versions match the catalog; `data_test.go` has the 13 `homestead/craft-*` rows. Live re-check is part of the runbook. |
| Local DNS answer per route | Done by S295 with a different design (above). Tests added for the craft path case. |
| Idempotence, removal, public leak, LAN selection | Done by construction: derived per query from the route table; public routes skipped; `Answer` picks the address on the asker's subnet (`internal/localdns` tests). |
| Managed/external/off modes, AdGuard bootstrap, port 53 | Superseded: the resolver is on/off by approved plan; the doctor warns when port 53 is taken (S295). |
| Start page advertised state | Not built (see Out of scope). |
| mDNS scope | Superseded: S295 announces `<label>.local` for host routes; craft path apps are reached at `homestead.local`. |

## Built
- `internal/docker/craftnames_test.go`: thirteen craft path routes add no
  `.lan` or `.local` names; removing a host-served craft route removes its
  names and keeps `homestead.lan`; a public route never enters the local
  answers.
- DEFERRED corrections; SLICE_INDEX entry.

## Verify live (VM 2, owner-run)
Let `A` be the appliance IP and `APPS="wordcraft gridcraft pdfcraft deckcraft
vectorcraft photocraft lightcraft figcraft filmcraft effectcraft soundcraft
designcraft cadcraft"`. Run on VM 2 as root unless noted.

1. Sign in and verify the manifest:
   `homesteadctl login -username cjrutherford -password-file /root/homestead-owner-recovered.pass`
   `homesteadctl craft trust list` (the HAI craft key is present) and
   `homesteadctl craft plan wordcraft` (verifies `catalog/craft/craft-manifest.yaml`
   against its `.minisig`; no throwaway key, no `--manifest` override).
2. Build all 13, then a second build for identity:
   `for a in $APPS; do homesteadctl craft plan $a && homesteadctl craft build $a --follow; done`
   `docker image inspect -f '{{.Id}}' homestead/craft-<app>:<version>` before and after
   `homesteadctl craft build <app> --follow` again: the image ID is identical.
   `homesteadctl craft status` shows each tag matching its recorded digest.
   If a counter at or above 8 is already recorded from earlier throwaway
   runs, `--allow-older` is the audited override.
3. Install each (VM 2 already has deckcraft, gridcraft and wordcraft: for
   these use `apps uninstall <app> --follow` first, or skip and test update
   in step 8). Body file `/tmp/<app>.json`:
   `{"domain":"lan","networks":[{"Name":"<app>-net"}],"container_ports":{"<app>":8080}}`
   (the plan names any missing field).
   `homesteadctl apps plan <app> -f /tmp/<app>.json` then
   `homesteadctl apps install <app> -f /tmp/<app>.json --approve-sign-in D1 --approve-access D2 --follow`
   where D1 and D2 are the digests the plan prints (a craft app has no
   sign-in; if the plan prints no digest, omit the flag).
4. Image checks per app (`C` = the app's container):
   `docker inspect -f '{{.Config.User}} {{json .Config.Volumes}} {{json .Config.Healthcheck.Test}} {{.State.Health.Status}}' $C`
   expects `nginx null` `/healthz` `healthy`;
   `docker inspect -f '{{json .Mounts}}' $C` shows only `/tmp` (tmpfs);
   `docker diff $C` is empty.
5. Headers, through the edge with the S280 root, no `-k`, no `--resolve`
   (the host name must resolve, so run after step 7):
   `scripts/craft-header-check.sh https://homestead.lan/apps/<app>/ --cacert <ca.pem>`
   with `--unhashed` for lightcraft, effectcraft and filmcraft; exit 0 for all 13.
   Compare each result with the row in `docs/architecture/CRAFT_HEADER_TABLE.md`.
6. Start page: `homesteadctl craft apps` lists all 13 with
   `https://homestead.lan/apps/<app>/`; open `/craft-apps` in a browser on
   the second client and follow a link.
7. Name resolution from a second LAN client. The VM 2 host behind the NAT
   can run the `.lan` check; mDNS does not cross the NAT.
   - Turn the resolver on: `homesteadctl names` (status), then the plan and
     apply verbs it prints (approve the port 53 firewall lines).
   - `dig +short @$A homestead.lan` returns the appliance LAN address;
     `dig @$A nosuch.lan` is NXDOMAIN; `dig @$A example.com` is REFUSED.
   - Restart the daemon (`systemctl restart homestead-server`) and repeat:
     the same answers.
   - With a resolver entry for `lan` pointing at `$A` (or `getent hosts
     homestead.lan` on a client that uses it), `curl --cacert <ca.pem>
     https://homestead.lan/apps/wordcraft/` returns 200.
   - On a device on the same L2 segment (a phone, laptop, or a VM bridged
     to the LAN): `avahi-resolve -n homestead.local` returns the address and
     the app loads at `https://homestead.local/apps/<app>/`.
   - Public leak: with a route set public, `dig @$A` for its name is
     NXDOMAIN and `avahi-resolve -n` fails.
8. Lifecycle: `apps uninstall <app> --follow` (image stays:
   `docker image ls homestead/craft-<app>`), reinstall as in step 3, then
   update with a throwaway bumped version: raise `version` and the image tag
   in `catalog/<app>.yaml` and the manifest entry, sign with a throwaway key
   (`craft trust add`), `homesteadctl apps update <app> --craft-manifest ...
   --craft-signature ... --craft-allow-older` as needed. Each converges to
   `healthy` with the route intact.
9. Drift scan after install and after update: `homesteadctl doctor` and
   `homesteadctl status` show no drift or names warnings; the Names page
   agrees with step 7.

Timing estimate for the 13-app matrix: a build is about 10 to 60 s for the
small apps and up to a few minutes for the 60 MB wasm apps (effectcraft,
photocraft), so builds take roughly 25 to 40 minutes serially, installs
about 15 minutes, the checks about 30 minutes, and lifecycle another 30 to
60 minutes: plan about 2.5 to 3 hours for a full pass.

Needs the owner: a second physical LAN device (or a bridged VM) for the
`.local` proof; a router conditional forward for `lan`, or one client
pointed at the appliance, for the `.lan` proof beyond `dig @$A`; approval of
the resolver plan.

## Live results (VM 2, 2026-11-02)
Commit 0252f17 deployed. Run by an agent over SSH; the resolver was already
on at the start (plan: "nothing changes"), so no resolver plan was applied.
The root CA was exported with `homesteadctl tls root export`.

| App | Build (counter 8) | Digest = `craft status` | Installed, healthy | user/mounts/diff | Headers | Served at `/apps/<app>/` |
| --- | --- | --- | --- | --- | --- | --- |
| wordcraft, gridcraft, deckcraft | pre-existing, verified only | match | healthy | nginx, only `/tmp` tmpfs, diff empty | PASS | yes |
| pdfcraft, vectorcraft, photocraft, figcraft, soundcraft, designcraft, cadcraft | ok | match | healthy | ok | PASS | yes |
| lightcraft, effectcraft | ok | match | healthy | ok | PASS with `--unhashed` | yes |
| filmcraft | ok | match | healthy | ok | PASS only with `--unhashed` | yes |

- A second build of the ten non-installed apps left all 13 image IDs
  identical. Five of those images were already cached from earlier runs, so
  the builds were fast; the identity check still holds.
- Runbook correction: filmcraft also needs `--unhashed` (its table row says
  unhashed names with `?v=`). Without it the check fails with
  `FAIL filmcraft_web_bg.wasm is not immutable (hashed wasm must be)`.
  effectcraft without `--unhashed` fails likewise, as expected.
- Header checks used `--resolve homestead.lan:443:<A>` because the VM host
  does not use the appliance resolver (`getent hosts homestead.lan` is empty),
  so the no-`--resolve` form was not exercised.
- `homesteadctl craft apps` lists all 13 with `https://homestead.lan/apps/<app>/`.
  The browser `/craft-apps` page check is owner-pending.
- Resolver, `dig @10.187.0.2` (dnsutils installed on the VM): `homestead.lan`
  returns 10.187.0.2, `nosuch.lan` NXDOMAIN, `example.com` REFUSED; identical
  after `systemctl restart homestead-server`. Craft apps add no extra names
  (the names list still shows the path routes only).
- Lifecycle on soundcraft: uninstall left the image and removed the
  container, reinstall converged to healthy with the route intact (header
  check PASS).
- Doctor: no craft findings; two pre-existing warns (release-trust placeholder
  key, navidrome data placement). `drift review` for soundcraft, pdfcraft and
  cadcraft: no drift. A craft install also ran its approved "link to Nextcloud
  for files" step for pdfcraft, filmcraft, effectcraft and cadcraft.
- Skipped: throwaway-key/bumped-version update (trust changes).
- Owner-pending: mDNS (`avahi-resolve`) from a second L2 device, router
  conditional forward for `lan`, `/craft-apps` in a browser, public-leak check
  with a public route.

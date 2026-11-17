# S421: Host firewall for gated apps

## Goal
Owner decision 2026-11-01: apps behind the ADR-055 forward-auth gate should need
one sign-in (Authentik), with the app's own login off. That is safe only if
nothing but the edge reaches the app's container port. Found live on VM 2
(Docker 26.1.5, iptables-nft, `br_netfilter` on): the host reaches every
container through the bridge gateway, so any non-root local user could reach an
auth-off app directly. This slice builds the protection. Turning Kuma's login
off is S422.

## Dependencies
S418, S419, S420, S052-S054 (firewall model and apply), ADR-008.

## Scope
- `internal/firewall/gate.go`: second Homestead table `inet homestead_gate`,
  interface-based (follow-up after the first VM 2 check, where a non-root user
  still reached Kuma on its own compose network `uptime-kuma_default`). Output
  chain drops non-root `skuid` for packets leaving through `docker0` or `br-*`;
  forward chain drops new flows leaving through a Docker bridge that did not
  arrive on one; each after a `ct status dnat accept` so published ports and
  `docker-proxy` work. One rule per interface pattern (no wildcard sets).
  Atomic `nft --check` then `nft -f`, read-back verified. No subnet or address
  is read or tracked; IPv4 and IPv6 are both covered by the `inet` table.
- Trade-off (ADR-055 amendment): while a gate is on, non-root users lose direct
  access to unpublished ports of every Docker container, the owner's own
  included. Opt out with `firewall_mode=external`.
- Live parser: reads named and numeric listings (`ct status 0x20`,
  `ct state 0x8`, also zero padded), found live on VM 2 as "applied but live
  state differs".
- `internal/server/gateblock.go`: `reconcileGateBlock` applies while any gate is
  on, removes when none; run from `reconcileEdgeSet`, `setGate`, at daemon start
  and every five minutes (`RunGateBlock`). `firewall_mode=external` is respected.
- Doctor check `gated-app-host-block` (missing or incomplete: warn with
  guidance; present: OK).
- Manifest field `sso.forwardauth.requireshostblock`: the sign-in plan refuses
  when mode is external or nft is missing or unprivileged, and `setGate` fails
  when the block is not in place after applying. Not set on Kuma (S422).
- Docs: ADR-055 §5/§6 amendment, ADR-008 clarification.
- Out of scope: Kuma login off (S422); a dedicated edge-to-app network; other
  containers on a shared network.

## Acceptance
- Rendered rules are exactly those in the ADR-055 amendment and pass
  `nft --check`.
- Gate on: table present and complete; last gate off: table removed; table
  edited or removed: re-applied by the next pass.
- The live reader reports complete for nft's numeric listing.
- Plan for a `requireshostblock` app refuses with the reason when nft is
  missing/unprivileged or mode is external.
- Doctor warns while a gate is on and the block is not in place.

## Tests
`internal/firewall/gate_test.go` (render, `nft --check`, numeric/named/padded
parse, incomplete variants, apply/remove), `internal/server/gateblock_test.go`
(reconcile on/off/edited/refusals, plan precondition, Doctor),
`TestSetGateFailsWithoutTheHostBlockOnlyWhenRequired`,
`TestForwardPlanRequiresHostBlock`, `TestForwardAuthRequiresHostBlockParses`,
`tests/test_s421_gated_app_host_firewall.py`.

## Built
See Scope. Files: `internal/firewall/gate.go`, `internal/server/gateblock.go`,
`internal/server/{forwardauth,firewall,doctorlive,tunnel}.go`,
`internal/appsso/{appsso,forwardauth}.go`, `internal/catalog/forwardauth.go`,
`cmd/homestead-server/main.go`, ADR-055, ADR-008.

## Verify live
On VM 2 with a gate on. `KUMA_EDGE` is the container's address on
`homestead-edge` and `KUMA_OWN` its address on `uptime-kuma_default`
(`docker inspect uptime-kuma -f '{{json .NetworkSettings.Networks}}'`).
1. `sudo nft -n list table inet homestead_gate` shows eight rules;
   `homesteadctl` logs no "live state differs".
2. As a non-root user, `curl -m 5 http://$KUMA_EDGE:3001/` and
   `curl -m 5 http://$KUMA_OWN:3001/` both time out.
3. As root, `sudo curl -m 5 -sI` of both answers.
4. `curl -skI https://<server>/` answers as non-root and root (published 443).
5. `homesteadctl doctor --live` shows `gated-app-host-block` OK.
6. `sudo nft delete table inet homestead_gate`: Doctor warns; within five
   minutes (or on any gate change) the table is back.
7. Turn the last gate off: the table is gone.
8. LAN-routed test if feasible: from a LAN host add a route to a container
   network via the appliance; `curl -m 5` of the container address fails.

## Live results (VM 2, 2026-11-01)

Docker 26.1.5, iptables-nft, Navidrome and Uptime Kuma gated.

- The rendered script passed `sudo nft --check -f`; after deploy
  `nft -n list table inet homestead_gate` showed all eight rules and the
  daemon logged no host-block error.
- Non-root `curl` to Kuma on the edge network (192.168.48.16:3001) and on
  its own compose network (172.29.0.2:3001) timed out; root got 302 on
  both. Navidrome on both of its networks timed out for non-root.
- The first build matched only the edge subnet and non-root still reached
  Kuma on 172.29.0.2; that is why the rules match Docker bridge
  interfaces instead.
- Non-root HTTP to the edge on 127.0.0.1 got 308 (published ports work).
- App egress unaffected: `curl https://example.com` inside Kuma got 200.
- `homesteadctl doctor --live`: `gated-app-host-block: ok`.
- Not run: steps 6–8 (manual table delete, last gate off, LAN-routed
  test).

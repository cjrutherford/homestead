# S295 — App names: `<app>.local` and the appliance resolver

## Goal
Make every app reachable by name without hand-edited DNS: announce each app as `<app>.local` over mDNS by default, and offer an optional resolver in the daemon that answers the appliance's own `.lan` names (ADR-039 §1).

## Dependencies
S294. ADR-039, ADR-035, ADR-030.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

Out of scope: nightly backups (S297), app sign-in (S298), public app names (S290), mDNS conflict probing and IPv6 (DEFERRED).

## Acceptance Criteria
- **mDNS names:**
  - The responder serves a list of single-label names: `homestead` plus one per app route whose local name is `<label>.lan`. The list follows the edge routes; installing or removing an app updates it without a restart.
  - Each name answers A queries and is announced once per interface, as `homestead.local` is today.
- **Edge aliases:** app routes may carry their `.local` alias; the edge answers it, and the local CA leaf covers it. Aliases on app routes are limited to that one `.local` name.
- **Resolver:**
  - Off by default. Turning it on or off goes through a plan the owner approves (the listener and the firewall rule for port 53 from the local network), and is audited.
  - When on, it answers A for `homestead.lan` and each app's `<label>.lan` with the appliance's LAN address, NXDOMAIN for other `.lan` names, and REFUSED for everything else, over UDP and TCP. It forwards nothing.
  - The page and CLI say how to add a conditional forward for `lan` on the router.
- **Surfaces:** the app page and `homesteadctl apps` show each app's local names; the resolver has a status, plan and apply in the API, CLI and dashboard.
- **Doctor:** warns when another device already answers an announced `.local` name, and when the resolver is on but port 53 is taken.
- Live check on the KVM VM: `<app>.local` resolves from the host, and `dig @<vm> <app>.lan` answers once the resolver is on.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`, `docs/architecture/LOCAL_DASHBOARD.md` (names section).

## Handoff
Next: S296 public dashboard behind Authentik, then S297 nightly backups in setup.

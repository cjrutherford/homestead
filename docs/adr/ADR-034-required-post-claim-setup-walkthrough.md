# Required Post-claim Setup Walkthrough

## Status
Accepted

Proposed and accepted by the owner on 2026-10-18, including UI-level gating
and not forcing appliances claimed earlier through the walkthrough. Related: ADR-004 (parity), ADR-006
(exposure), ADR-007 and ADR-009 (domain and DNS), ADR-010 (TLS), ADR-015
(secure first claim), ADR-017 (tunnel), ADR-020 (session boundary), ADR-021
(secret store), ADR-033 (Authentik sign-in).

## Decision
After claim and first-administrator creation the dashboard keeps the owner in
a **setup walkthrough** until every step is completed or explicitly skipped.
The walkthrough is guidance and orchestration over existing capabilities:
nothing is applied except through the existing plan, review and approve flows,
and every step's controls are the same API calls the CLI already has
(ADR-004).

### Steps
1. **Secure the owner account.** Change the password if it was generated or
   temporary (for example after the recovery path), otherwise confirm it is the
   owner's own; export and acknowledge the backup recovery identity
   (existing `backups/identity` routes); explain in plain words that the
   local `owner` account is the break-glass path and must stay usable
   (ADR-033). Completed when the recovery identity is acknowledged.
2. **Sign-in.** Install Authentik through the catalog install wizard and link
   the owner's Authentik account (ADR-033). Skippable, and skipped by default
   for owners who do not want SSO.
3. **Domain and access.**
   - Choose a DNS provider: Cloudflare, DigitalOcean, Route 53 or manual, and
     enter its credential. The page spells out the exact permissions each
     needs, taken from what the adapters actually call: Cloudflare, a scoped
     API token with Zone read and DNS edit for the chosen zone (plus
     Account Cloudflare Tunnel edit if a tunnel is chosen); DigitalOcean, a
     personal access token with read and write scope (the DNS adapter uses
     the domains API); Route 53, an IAM key pair allowed
     `route53:ListHostedZones`, `ListResourceRecordSets` and
     `ChangeResourceRecordSets` on the hosted zone; manual, no credential
     and the owner creates records by hand from the displayed table.
   - Choose how the appliance is reached: **home network only** (default,
     local-first, ADR-006), **Cloudflare Tunnel** (offered only when the
     provider is Cloudflare, ADR-017) or **router port forward** (router
     discovery and validated adapters). The chosen path produces the usual
     exposure and tunnel plans, which the owner reviews and approves here.
   - If a domain or tunnel is already configured (existing domain onboarding,
     S223, or tunnel, S246) the step opens as a **review of the existing
     setup**, with edit links, never a redo, and completes on acknowledge.
   - DigitalOcean, Route 53 and manual provide DNS and certificates only;
     outside access for them is a router port forward. Droplet or relay
     tunnels are not offered (deferred).
4. **Initial app selection.** A short curated list with plain descriptions
   from the catalog. Selected apps are installed through the S285 install
   wizard's plan and queue as operations; they appear in the S283 work-orders
   rail and do not block finishing the walkthrough.

State per step is one of `pending`, `done`, `skipped`. Skipping is a recorded
owner choice and reversible from the Setup page, which stays in the navigation
after completion for revisiting.

### Resumable state
Progress is stored with the existing resumable wizard store
(`internal/wizard`, wizard id `setup`; non-sensitive payload only: step
states, chosen provider name, chosen access mode). Credentials entered in a
step go straight to the secret store (ADR-021) and never into wizard
payloads. A read-only derived status (`GET /api/v1/setup`) reports each step
as `pending`, `done` or `skipped`, combining the stored choice with observed
facts (for instance step 3 is `done` when a domain is onboarded), so a step
completed from the CLI or the Domain page counts. CLI parity is
`homesteadctl setup status`, `homesteadctl setup step <name>` (show or
act on a step) and `homesteadctl setup skip <name>`.

### Boundary and gating
The walkthrough needs no new public routes. By the time it starts the owner
is logged in (first-admin setup issues a session, ADR-020), and every call the
steps make already exists as a session-class route (backups identity, config,
tls credentials, exposure, tunnel, catalog plan and install, operations,
wizards). The new routes (`GET /api/v1/setup`, `PUT /api/v1/setup/steps/{name}`
for skip and unskip) are **session-class** and carry parity rows. After claim
completes the wizard routes require a session already, so resumable state
needs no allowlist change.

**Gating is at the UI, not the server.** While setup is incomplete, an
Angular router guard redirects dashboard navigation to the walkthrough;
direct API calls, the CLI and `scripts/acceptance.sh` are not gated.
Reasons:
- Setup is guidance, not a security control. The security boundary is ADR-015
  and ADR-020 (claim, session, CSRF); a server-side "setup incomplete"
  rejection adds a second state machine that every route test must model.
- A server gate would break CLI parity and automation (ADR-004): `homesteadctl`
  and the acceptance scripts would fail until a human clicked through a
  dashboard, and a half-finished walkthrough could lock the owner out of the
  very routes needed to fix the problem.
- The owner may override recommendations (project principles), so the guard
  must be skippable, and it is: every step has an explicit skip.
- Recovery stays simple: if the guard misbehaves the CLI and the Unix-socket
  recovery path still work.
The guard also exempts the login page, claim page, Setup page and the
operations views, and never traps a not-logged-in user. Appliances claimed
before this record ships are stamped `legacy` (not gated); they see a
non-blocking "Finish setup" entry instead.

## Context
A fresh owner currently lands on an empty dashboard with Status warnings and
no order of operations: secure the account, back up the recovery key, choose a
domain, expose services deliberately. The pieces all exist (S223 domain
onboarding, S246 tunnel, S271 recovery identity export, S285 install wizard,
S286 sign-in), but nothing sequences them.

## Alternatives considered
- Server-side gating of all non-setup routes: rejected above.
- A one-time modal checklist with no persistence: loses progress on reload
  and gives the CLI nothing to read.
- Auto-applying recommended defaults: violates plan, review, approve (ADR-006).
- Offering relay tunnels for non-Cloudflare providers: deferred.

## Consequences
- Until accepted, S287 stays blocked; S285 and S286 do not depend on it.
- Two new session-class routes and one CLI command family, with parity rows.
- Step completion is derived from real state, so out-of-band changes count.
- No setup state gates the API; the guard is a UI affordance only.
- Provider permission text must track the adapters; a test pins the
  per-provider permission copy to the calls each adapter makes.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

## Amendment 2026-10-19 (S287 review)
Status stays Accepted; this records implementation details, not a change of
decision.
- The guard's exemptions also include the operations list (`operations`) as
  well as `operations/:id`, and the pages the walkthrough links to (`account`,
  `sign-in`, `tunnel`, `exposure`, `tls`, `catalog/:name/install`). The full
  list and the reason for each is in `docs/architecture/SETUP_WALKTHROUGH.md`.
- The guard fails open: when the status cannot be read (or the owner is not
  logged in) the navigation is allowed, so a daemon fault can never lock the
  owner out of the dashboard.
- "Reviewed and approved in the walkthrough" is met by reading back approval:
  the plan is reviewed and approved on the linked Tunnel or Exposure page, and
  the walkthrough only confirms once that approved state exists.

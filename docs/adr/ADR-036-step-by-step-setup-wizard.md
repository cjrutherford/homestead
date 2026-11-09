# Step-by-step Setup Wizard

## Status
Accepted

Owner decisions 2026-10-20. Supersedes ADR-034 (Required Post-claim Setup
Walkthrough). Everything ADR-034 decided stays in force unless this record
changes it: derived step status, resumable state in the `setup` wizard
store, credentials only in the secret store (ADR-021), UI-level gating that
fails open, legacy appliances not gated, CLI parity (ADR-004), and nothing
applied except through the existing plan, review and approve flows (ADR-006).
Related: ADR-020 (session boundary), ADR-030 (internal CA), ADR-033
(Authentik sign-in), ADR-035 (dashboard on the LAN).

## Decision
The post-claim setup becomes a **wizard**: one step on screen at a time, in a
fixed order, with Back and Next. Each step does its work **inside the
wizard** (owner decision) instead of linking out to other pages, and each
step explains in plain words what it does, why, and every task the owner must
do **outside Homestead** to finish it.

### Steps, in order
1. **Secure the owner account.** As ADR-034 step 1. Outside Homestead: keep
   the password in a password manager; store the exported recovery identity
   file offline (for example on a USB stick), not on the appliance.
2. **Storage** (new, owner decision). Register the mounted disks Homestead
   discovered and assign the app, bulk and backup classes, using the existing
   storage registration and assignment routes. Done when the app class is
   assigned. Comes before sign-in and apps because installs need the app
   class. Outside Homestead: Homestead never formats or partitions (project
   principle); a disk must already be mounted (the step shows an `fstab`
   example and links the storage doc) and the page refreshes discovery.
3. **Sign-in with Authentik** (skippable). Install Authentik from the
   catalog with live operation progress in the step, then the existing
   Sign-in plan, approve, apply and link (ADR-033). Outside Homestead, each
   spelled out in order with the exact address to open: reach the Authentik
   name from this device (router DNS or hosts entry, ADR-035), trust the
   appliance root (ADR-030), finish Authentik's initial setup (create
   `akadmin`), create an API token in Authentik's admin interface, paste it
   back. The break-glass local password is restated.
4. **Domain and access** (skippable). As ADR-034 step 3: provider choice with
   the exact permissions each needs, then home-only, Cloudflare Tunnel or
   router port forward, with the plans reviewed and approved in the step.
   Outside Homestead: for Cloudflare, adding the domain (zone) and creating a
   scoped API token, click by click; for the others, the equivalent
   credential; for manual DNS, the record table to enter at the registrar.
5. **Apps** (skippable). As ADR-034 step 4.

A final **summary** screen lists each step's outcome (done or skipped) with
the way back to any skipped step. It is not a step and has no state.

### Order and navigation
- Linear (owner decision): Next is enabled only when the current step is
  `done` or the owner explicitly skips it. Back always works. A step that is
  already `done` from the CLI or another page shows as done, and Next is
  enabled.
- The wizard opens at the first step that is `pending`. After finishing, the
  Setup entry stays in the navigation and reopens the wizard at the step the
  owner picks; skipped steps can be completed later.
- The step's controls call the same API routes as the standalone pages; the
  standalone pages stay, and the step components reuse their logic rather than
  copying it.

### API and CLI
- `GET /api/v1/setup` gains the `storage` step, between `account` and
  `sign-in`, derived from the storage assignments (`done` when the app class
  is assigned). `PUT /api/v1/setup/steps/{name}` accepts it for skip and
  unskip. `homesteadctl setup` accepts the new step name. No new routes.
- Appliances already past setup under ADR-034 (or stamped `legacy`) are not
  sent back into the wizard because of the new step: the new step only gates
  appliances whose setup was still incomplete.

## Context
ADR-034's walkthrough is a checklist whose steps link to other pages. Testing
it end to end on the LAN, the owner found it gave too little guidance: it was
unclear what to do next, and the tasks outside Homestead (Authentik's own
first-run and token, DNS for local names, the Cloudflare token) were not
explained where they were needed. Installing an app before storage is
assigned also fails late, in the install plan.

## Alternatives considered
- Keep link-out steps with better explanations: smaller, but the owner leaves
  the wizard and loses the thread (owner chose inline).
- Free-order stepper: the owner may jump past a prerequisite (storage before
  apps) and the guidance becomes a menu again (owner chose linear).
- Separate steps for trusting the root certificate and for each outside task:
  the owner chose to keep the step list short; those tasks are explained
  inside the step that needs them.
- Server-side gating: rejected in ADR-034 for the same reasons.

## Consequences
- The Setup page becomes a wizard component; step components embed the logic
  of the account, storage, sign-in, domain and tunnel, and install pages.
- One more derived step and its parity rows; the CLI and status test cover it.
- Explanatory copy for outside tasks must track the real UIs it describes
  (Authentik admin, Cloudflare dashboard); it names menu paths, and a doc
  records when it was last checked.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

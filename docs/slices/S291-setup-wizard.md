# S291 — Step-by-step setup wizard

## Goal
Replace the setup checklist with a wizard that walks the owner through setup one step at a time. Each step's work happens inside the wizard, and each step explains every task outside Homestead needed to finish it (ADR-036).

## Dependencies
S289. Runs before S290 (owner direction 2026-10-20).

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Owner direction 2026-10-20: more hand-holding in the configuration, a wizard with one step after the next, and explanations of each step including the tasks done outside Homestead. Owner decisions are in ADR-036: steps work inline, the order is linear with skip allowed, and Storage is a new step.

Today `/setup` (S287, `dashboard/src/app/setup/`) shows the four ADR-034 steps as a list whose actions link to other pages.

## Acceptance Criteria
- ADR-036 (Accepted) supersedes ADR-034; `docs/adr/README.md` updated.
- The Setup page is a wizard. It shows a progress indicator with the five steps, one step at a time, and Back and Next.
  - Next is enabled only when the step is `done` or skipped.
  - It opens at the first `pending` step.
  - A final summary screen lists each step's outcome.
  - The router guard behaviour is unchanged: it fails open, has the same exemptions, and `legacy` appliances are not gated.
- Steps, in this order, each doing its work inline through the existing API calls, reusing the existing page logic (services or shared components, not copies):
  1. **Account:** password change and recovery identity export and acknowledgement.
  2. **Storage:** discovered mounts, register, and assign the app, bulk and backup classes.
  3. **Sign-in:** install Authentik with live operation progress, then plan, approve, apply and link.
  4. **Domain and access:** provider and credential, then home-only, tunnel or port forward, with the plans reviewed and approved in the step.
  5. **Apps:** a curated selection queued through the install wizard plan.
- Each step has a plain-words "What this does" and an ordered "Outside Homestead" checklist where relevant:
  - **Account:** password manager; recovery file stored offline.
  - **Storage:** the disk must already be mounted; an `fstab` example.
  - **Sign-in:**
    1. reach `authentik.<name>` from this device (router DNS or hosts entry);
    2. trust the root certificate (link to Trust);
    3. open `https://authentik.<name>/if/flow/initial-setup/` and create `akadmin`;
    4. in Authentik Admin, create an API token (exact menu path) and paste it here.
  - **Domain:**
    1. add the domain to Cloudflare;
    2. create the scoped API token (exact permissions, matching the existing permission copy test);
    3. the other providers' equivalents;
    4. for manual DNS, the record table.
- API and CLI:
  - `GET /api/v1/setup` reports the `storage` step (done when the app class is assigned).
  - `PUT /api/v1/setup/steps/storage` skips and unskips it.
  - `homesteadctl setup status|step|skip` accept `storage`.
  - An appliance whose setup was complete before this slice is not re-gated by the new step.
- `docs/architecture/SETUP_WALKTHROUGH.md` rewritten for the wizard, including the date the outside-task menu paths were checked. Update `docs/PARITY.md` if anything changes, and `docs/DEFERRED.md`.
- Tests:
  - Go: setup status with storage, and the not-re-gated rule.
  - CLI: the storage step name.
  - Dashboard specs: wizard navigation (Next disabled until done or skipped, Back, opens at the first pending step, summary), and each step's inline actions calling the right API.
  - The parity and route classification tests stay green.
- KVM on VM 2. Fresh install on the reset VM, which is on the LAN with a single NIC. From the workstation browser: claim through `https://homestead.local`, then run the whole wizard. Install and link Authentik, and record the tunnel/domain choice (the tunnel can be skipped until S290). Sign out, sign in with Authentik, and check the break-glass password. Record the transcript in `docs/DEVELOPMENT.md`; this also closes the pending S289 KVM item.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- ADR-036.
- `docs/architecture/SETUP_WALKTHROUGH.md`.
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main (the ADR may land first as its own planning commit).

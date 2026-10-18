# S287 — Post-claim setup walkthrough

## Goal
After claim and first-admin creation, keep the owner in a resumable setup walkthrough (secure the account, sign-in, domain and access, first apps) until each step is completed or skipped.

## Dependencies
S286 (sign-in step), S285 (app selection installs through the wizard). **Blocked until ADR-034 is Accepted.** ADR-034 is Proposed (2026-10-18); do not start this slice, and do not rely on its decisions, until the owner accepts it, as S281 was blocked before ADR-031. If acceptance changes the decisions, update this slice first.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-034 (once Accepted). Orchestration only: nothing is applied except through the existing plan, review and approve flows. Out of scope and in `docs/DEFERRED.md`: droplet or relay tunnels for non-Cloudflare providers (DigitalOcean, Route 53 and manual cover DNS and certificates only; outside access for them is a router port forward), new DNS providers, new router adapters.

## Acceptance Criteria
- Post-claim setup walkthrough implemented and documented
- Step 1, secure the owner account: password change when generated or temporary, recovery identity export and acknowledge through the existing `backups/identity` routes, plain-language break-glass explanation; done on acknowledge
- Step 2, sign-in: install Authentik through the S285 wizard, then link through the S286 flow; skippable
- Step 3, domain and access: choose Cloudflare, DigitalOcean, Route 53 or manual and enter the credential (sealed per ADR-021); per-provider required permissions spelled out exactly (Cloudflare scoped token: zone read and DNS edit, plus tunnel edit when a tunnel is chosen; DigitalOcean token with read/write scope; Route 53 IAM key with `ListHostedZones`, `ListResourceRecordSets`, `ChangeResourceRecordSets`; manual: none, records shown for the owner to create); a test pins the copy to what each adapter calls
- Step 3 access choice: home network only (default), Cloudflare Tunnel (offered for Cloudflare only), router port forward; each produces the usual plan, reviewed and approved in the walkthrough; Cloudflare Tunnel is hidden for other providers with the reason shown
- Step 3 review path: when domain onboarding or a tunnel already exists, the step opens as a review of the existing setup with edit links and completes on acknowledge; it never re-runs onboarding or re-creates a tunnel
- Step 4, initial app selection: short curated list with plain descriptions; selections go through the S285 plan and install, queue as operations and show in the S283 rail; finishing the walkthrough does not wait for installs
- Step states `pending`, `done`, `skipped`; skip is explicit and reversible; completion derived from real state (a step done from the CLI or another page counts)
- Resumable: state in `internal/wizard` under id `setup`, non-sensitive payload only; reload and re-login resume the correct step; credentials never in wizard payloads
- API and CLI: `GET /api/v1/setup` (derived status), `PUT /api/v1/setup/steps/{name}` (skip, unskip), session-class, no new public routes; `homesteadctl setup status`, `setup step <name>`, `setup skip <name>`; `docs/PARITY.md` rows added and the parity test is green; routes classified in `internal/server/boundary.go` `routeClasses` and the classification test is green
- Gating: UI-level router guard only, per ADR-034: incomplete setup redirects dashboard navigation to the walkthrough; exempt login, claim, Setup and operations; the API, CLI and acceptance script are never gated; appliances claimed before this slice are stamped `legacy` (not gated) and get a "Finish setup" nav entry; Setup stays in navigation after completion
- Tests: step derivation (fresh, partial, all done, skipped, out-of-band done), resume, skip and unskip, review path with existing domain and tunnel, tunnel option visibility by provider, provider permission copy pinned to adapters, guard redirect and exemptions, legacy stamp, CLI output, route classification, secrets absent from wizard payloads
- KVM on VM 2: factory-reset the appliance, claim, create the first admin, and walk the whole walkthrough end to end (password step, recovery export and ack, Authentik install and link or skip, DNS provider credential and access choice, app selection with rail tracking); then repeat on an appliance that already has domain onboarding and a tunnel and confirm step 3 opens as a review and changes nothing; confirm CLI `setup status` agrees at each point; leave VM 2 as found; transcript and screenshots in `docs/DEVELOPMENT.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Operations doc: the walkthrough, skipping, and resuming from the CLI.
- `docs/DEFERRED.md`: record droplet/relay tunnels (already added) and anything discovered.
- `docs/PARITY.md`: rows as above.
- `docs/DEVELOPMENT.md`: S287 section with the KVM transcript summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

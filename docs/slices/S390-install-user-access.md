# S390 — Choose user access during app installation

## Status
Implementation and automated tests complete, including the resume-admin
step-up added from review. Live acceptance on VM 2 (real Authentik and
tunnel installation) is pending; do not deploy or call this release ready
until it passes.

## Goal
Install reviews explicit access for every existing managed person, with
owner access fixed full, while protecting app-admin grants with step-up.
See ADR-054. Both dashboard and CLI use the same daemon-enforced plan.

## Requirements
- A verified accountless app is described as open; an account-based app
  without an enforceable integration cannot be presented as secured.
- Include the managed-person roster, per-person decisions and relevant app
  and authentication readiness in the approval; reject stale decisions.
- Enforce app-administrator step-up on the server for local and public
  dashboard sessions, without accepting public local passwords. Preserve
  legacy pending setup records without granting new privileges.
- Apply access through reviewed per-app integrations. Validate selections
  before changes and recheck before grants, and report failures or partial
  results accurately.
- Test dashboard and CLI parity, roles, every-person coverage, disabled
  users, stale approvals, denied and stepped-up admin requests, backward
  compatibility, and real Authentik sign-in before claiming completion.

## Verification still required
Managed public installs now review the anticipated post-sign-in publication
plan and retain its original digest through setup. Installation rebuilds
install, sign-in, access and public approvals before it starts. Publication
waits for successful sign-in and person grants, then rebuilds actual state
against that original digest. A live Authentik and tunnel installation E2E
is still required before declaring this slice release ready or deploying it.

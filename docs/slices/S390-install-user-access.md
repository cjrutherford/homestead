# S390 — Choose user access during app installation

## Status
Planned. The S389 decision contract is not wired to installation.

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

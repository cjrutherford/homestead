# S398 — Install apps that keep their own accounts

## Status
Complete (automated tests). Live check on VM 2 pending.

## Regression
S390 (4ec3efc) classified every catalog app with neither an OIDC adapter nor
Craft handling as `unavailable`, so installing it answered 422 "app access
unavailable: Homestead cannot yet configure this app's accounts and
permissions". That blocked audiobookshelf, gitea, komga, linkding, mealie,
navidrome, paperless-ngx, freshrss, uptime-kuma, vaultwarden,
calibre-web-automated and every other app without an adapter. They installed
fine before S390.

## Decision
ADR-054 refuses an install only when per-person selections would be claimed
to protect an app. A new install access mode, `self-managed`, covers apps
with no OIDC declaration (the app keeps its own accounts). The existing
`open` mode stays for the account-less Craft apps.

- Plan and access review carry `mode: "self-managed"` and a `notice`:
  "<App> keeps its own accounts. Homestead does not control who can sign in
  to it; create accounts in the app itself."
- The install proceeds with no per-person choices.
- Any per-person choice or role is refused with 422 before an operation is
  created: `access-plan` with `choices`, top-level `choices`, `setup.people`,
  and `setup.access` carrying choices.
- The mode is bound into the access digest (`self-managed-v1:` plus the
  install plan digest). `access-plan` returns it; an install that sends it
  is verified (409 if stale). Sending it is optional because no claim is
  made; the CLI and dashboard do not need it.
- `unavailable` remains for real blockers only: an OIDC adapter Homestead
  does not know (catalog fault), Authentik not ready, people verification
  failing, or previous grants/groups for a managed app.

## Catalog by mode after this slice
- managed (OIDC adapter): by the catalog's `sso.oidc.adapter` declarations
  (immich, jellyfin, nextcloud, ...).
- open: the thirteen Craft apps.
- bootstrap: authentik.
- self-managed: every other app, including audiobookshelf, gitea, komga,
  linkding, mealie, navidrome, paperless-ngx, freshrss, uptime-kuma,
  vaultwarden, calibre-web-automated.

## Clients
CLI: `apps plan` and `apps install` print the notice and refuse a
`choices` key in the input file. Dashboard wizard: shows the notice, renders
no selectors, install enabled. No routes, commands or surfaces were added,
so PARITY.md is unchanged.

## Tests
Handler matrix (mealie fixture): plans self-managed, installs to the
operation boundary with and without the digest, refuses every per-person
form with no operation, stale digest 409, immich still managed. Mode
classification, CLI install/plan/refusal, wizard spec.

# S325 — Tunnel setup issues the public certificate and moves app sign-in

## Goal
ADR-048 §5–§6. Approving the tunnel plan also issues the Let's Encrypt
certificate for the public dashboard name, so the appliance answers
Authentik's public address itself (ADR-047). Apps whose sign-in waited
for that address move to it in the same approval. A certificate issued
from TLS moves them too.

## Scope
- Tunnel plan (`POST /api/v1/tunnel/plan`), when it publishes the
  dashboard and Authentik's path:
  - `certificate`: the public dashboard name, and either the public
    certificate already placed for it (no step), or the issuance the
    operation runs: Let's Encrypt production, DNS-01 through the
    Cloudflare token stored in setup, zone, automatic renewal, the
    terms URL, and the issuance plan's digest as it will stand once the
    edge carries the public name. A TLS strategy entry covering the
    name is reused when it is ACME DNS-01; otherwise a
    `public-dashboard` entry is added to TLS at step time. A strategy
    entry tunnel setup cannot run, or a blocked issuance plan, is
    reported with no step.
  - `issuer_moves`: every applied app whose sign-in plan, with the
    certificate served, names a different issuer. Each lists the issuer
    change, the new host entry, the one it replaces and the app's plan
    digest. Without the kept Authentik token the note says nothing
    changes in Authentik and each app is re-planned from its card.
  - Both are bound into the tunnel digest.
- Tunnel setup:
  - `agree_terms` is required when the plan issues the certificate,
    and `confirm_production` for a production CA (422 otherwise).
  - `certificate` step after `record-route`, before `sign-in`: adds the
    strategy entry if planned, re-plans the issuance and compares its
    digest, records the approval under the owner (audited
    `cert.issuance.approve`, "approved with tunnel plan <digest>"), and
    runs the existing issuance steps (issue and place, refresh routes,
    reload the edge). On failure the tunnel stays up, the operation
    fails saying the certificate was not issued and how to retry it
    from TLS, and later steps are skipped.
  - `app-sign-in` step last, when apps move and the token is kept: each
    app's sign-in is re-planned, checked against its approved digest
    and applied with the kept token (audited `app.sign-in`). One app
    failing does not stop the others; the failed apps are named.
- TLS issuance: a plan whose names cover the public dashboard name
  carries `issuer_moves` in its digest, and its operation ends with the
  same `app-sign-in` step.
- CLI: `tunnel setup --agree-terms --confirm-production`; `tunnel plan`
  prints the certificate and the move list and adds the flags to its
  approve line; `tls issuance plan` prints the move list.
- Dashboard: the tunnel review shows the certificate section with the
  terms (and production) checkboxes that gate Approve, and the move
  list; the Certificates page shows the move list. Apps without the
  kept token link to their catalog card.

## Rules
AGENTS.md slice rules apply. No new routes; PARITY descriptions gain
the new fields and flags. No migration.

## Acceptance criteria
- Go tests cover: the certificate section and its digest binding; terms
  and production confirmation; step order; an already placed
  certificate adding no step; a blocked strategy entry; a failed
  certificate keeping the tunnel and skipping sign-in; the move list
  and its no-token note; only listed apps re-applied, one failure not
  stopping the others; the TLS path's moves in the digest and step;
  cert-level move binding, Preview/Plan digest equality, IssueDefinition
  and AddToStrategy. No test contacts a real CA.
- CLI tests cover the printouts and flags. Dashboard specs cover the
  certificate section, the gated approval, the move lists and links.
- The parity test, and all Go, Python and dashboard checks, pass.

## Live check
On VM 2 with a published app applied before the certificate: plan the
tunnel, agree to the terms, approve. The operation issues the
certificate, updates sign-in, and moves the app; the audit shows
`cert.issuance.approve` through the tunnel plan and `app.sign-in`.

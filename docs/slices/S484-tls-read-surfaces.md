# S484 - TLS read surfaces

## Goal and dependencies
List the recorded issuance approvals (G3) and the local CA's leaf certificates (G4) on the Certificates page (`/admin/tls`), so every read the CLI has (`tls issuance approvals`, `tls local leaves`) has a dashboard page. Depends on S278 and S280 (the routes) and S483.

## What changed
- `Tls` calls the existing `tlsIssuanceApprovals()` and `localCaLeaves()` wrappers on load. No daemon, CLI or route change.
- "Recorded approvals" table: certificate, who approved, when (UTC), plan digest, certificate authority and class. Empty state uses the CLI wording: "No issuance approvals recorded."
- "Local certificates" table: host, status and detail, expiry, and a link to the Trust page where the existing re-issue action lives. Empty state uses the CLI wording: "No local-only HTTPS routes use the internal CA."
- A failed load of either list shows its own error and leaves the rest of the page working.

## Verification
Dashboard specs in `tls.spec.ts` (tables, empty states), `tests/test_s484_tls_read_surfaces.py`, checklist row `END-PARITY-4`, and a live check on VM 2.

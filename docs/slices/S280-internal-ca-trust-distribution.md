# S280 — Internal CA trust distribution and local TLS for craft hosts

## Goal
Local-only hosts are served over HTTPS from Homestead's persisted internal CA, and the owner can download, fingerprint-check and install the root certificate on their devices, so secure-context browser features (WebGPU, clipboard) work for craft apps without a public certificate.

## Dependencies
S104,S222,S101,S105,S263,S278,S270,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-030 (Accepted 2026-10-15; amends ADR-010), ADR-006, ADR-021, ADR-020 and ADR-024. Lands before S249; S249 to S254 depend on it. Closes or narrows the DEFERRED entries "Internal CA distribution" (S104/S222), the internal-CA note in the S278 gaps entry, and "Management TLS provisioning" (S222). Serving management TLS from the daemon itself stays deferred unless it falls out of the edge work; say which in the slice record.
S101 (Caddy automatic HTTPS), S105 (certificate monitoring), S263 (renewal check), S278 (first issuance) and S270 (dashboard edge surfaces) supply the edge and certificate code this extends.

## Acceptance Criteria
- Internal CA trust distribution and local TLS implemented and documented
- Persist the internal CA: the S104 authority and S222 baseline mint it once; private key custody-sealed under ADR-021 (never in YAML, logs, audit, drift, API or export), public certificate and metadata in SQLite and inspectable YAML; migration plus idempotent creation; backup and configuration export carry the public certificate only; restore path documented and tested
- Local leaf issuance and rotation: leaf certificates for local-only hosts issued by the CA and presented by the edge (Caddy) through the existing route reconciler, planned and audited like other certificate changes; unattended renewal on the daemon scheduler (ADR-023, ADR-024, ADR-026); new local-only routes get a leaf automatically; hosts with a public-provider certificate are never switched to the internal CA; name constraints applied to the owner's local suffix where the authority supports it
- Root download: `GET` route serving the PUBLIC root certificate (PEM, and DER) on the local/overlay management listener; classified in `internal/server/boundary.go` as the one read-only unauthenticated exception per ADR-030, with a test that no private key material can be served and that the route is absent from public-exposure listeners
- Fingerprint display: SHA-256 fingerprint, serial and validity shown with the certificate on the authenticated API, CLI and dashboard so the owner can verify a downloaded file
- Surfaces with parity (ADR-004): API routes for status, root download, leaf list, re-issue and root rotation plan/approve; `homesteadctl` commands; a dashboard "Trust this appliance" page (S248 tokens, `StateMessage`) showing fingerprint, download buttons and the per-OS steps; component tests; `docs/PARITY.md` rows added and the parity test green
- Per-OS install runbook in `docs/architecture/` or operations docs: Windows, macOS, iOS and iPadOS (profile plus full-trust toggle), Android (CA certificate install, user-store caveats, per-app trust), Linux system store, Firefox's own store, Chrome/Chromium on Linux (NSS); how to verify the fingerprint, how to remove the root, what trusting it permits (ADR-030)
- Doctor check (S044 pattern): reports root present, root and leaf expiry windows, a leaf not matching its served host, and the edge not presenting the internal CA for a local-only HTTPS host; surfaced by CLI and dashboard
- Rotation: explicit root rotation plan listing which devices must re-trust; leaf rotation automatic; tests with an injected clock for expiry and renewal windows
- Tests: CA persistence across restart and restore, leaf chain validates to the root and covers the host and SANs, edge config renders the internal-CA certificate for local-only hosts only, key never in any response or audit, route classification, rotation
- KVM acceptance: fresh appliance, create the CA, open the Trust page, download the root and compare fingerprints across CLI, dashboard and `openssl x509 -fingerprint -sha256`; add the root to the KVM host's trust store and the guest's, then `curl https://<local host>` verifies without `-k`; renew a leaf with a shortened validity and see the edge present the new one without a restart; rotate the root; confirm the private key is absent from the database dump, YAML, backups and logs; clean removal leaves no stray certificates; record results in `docs/DEVELOPMENT.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- `docs/DEFERRED.md`: close or narrow the "Internal CA distribution" and "Management TLS provisioning" entries and the internal-CA clause of the S278 gaps entry; record newly discovered out-of-scope work (for example MDM or automatic device enrolment, ACME server for local names, DANE or CT, native-app trust stores).
- `docs/PARITY.md`: new routes, commands and dashboard surface.
- `docs/DEVELOPMENT.md`: S280 section with the KVM transcript summary.
- Record any architectural change as an ADR before implementation (ADR-030 holds the current decision).

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

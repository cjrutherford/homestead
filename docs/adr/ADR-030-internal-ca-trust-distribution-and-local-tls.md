# Internal CA Trust Distribution and Local TLS

## Status
Accepted

Owner decision 2026-10-15. Amends ADR-010 (TLS Provider Abstraction) by
using its existing internal CA path for local-only hosts; consistent with
ADR-006 (Local-first Exposure), ADR-021 (Secret Key Custody) and ADR-020
(Operator Session Boundary).

## Decision
### Why
Craft apps need a secure context: WebGPU and the clipboard require HTTPS
(or `localhost`), and a local-only `.lan` host has no public certificate.
Homestead therefore serves HTTPS for local-only hosts from its **own
internal CA**, and lets the owner trust that CA on their own devices.

### Persisted authority
The internal CA authority proven by S104 and exercised by S222 is
persisted on the appliance. Its private key is custody-sealed under
ADR-021 (never in YAML, logs, audit, exports or API responses); its
public certificate and parameters are recorded in SQLite and inspectable
as YAML. Backups and configuration export include the public certificate
only; restoring the key follows the ADR-021 and ADR-022 paths. The CA is
constrained to the owner's local names where the authority can express it.

### Local leaf certificates
For local-only hosts the edge (Caddy) is configured to present a leaf
certificate issued by the internal CA. Leaves are issued and rotated by
the daemon through the same plan, apply and audit machinery as other
certificates (ADR-024 renewal semantics); renewal is unattended. Hosts
exposed publicly keep their ADR-010 provider (ACME or imported); this ADR
never issues internal-CA certificates for public names.

### Root certificate distribution
The **public** root certificate is offered for download so the owner can
install it into their devices' trust stores: an API route, a dashboard
page and CLI commands (ADR-004 parity), each showing the SHA-256
fingerprint, validity and serial. The download route serves only the
public certificate on the local/overlay management listener and is
classified in `internal/server/boundary.go`; because a device fetching
the root cannot yet have an operator session, the route is the one
deliberate read-only unauthenticated exception, and the fingerprint
shown in the authenticated dashboard and CLI is the owner's check against
tampering in transit. Per-OS and per-browser install steps live in an
operations runbook. Homestead never installs the root into any client
trust store, never pushes it, and never serves the private key.

### Rotation
The root has a long, recorded validity. Rotation and re-issue are
explicit owner actions that show which devices must re-trust the new
root; leaf rotation is automatic. A doctor check reports an expiring root,
an expiring leaf, or a leaf not matching the served host.

## Consequences
- Local-only craft hosts (and any other local-only host that opts in)
  are HTTPS with a certificate the owner can trust deliberately; no
  public CA and no outbound dependency is involved.
- Trusting the root on a device lets that root vouch for any name its
  constraints permit; docs say so plainly, and the private key is
  custody-sealed.
- The unauthenticated root-download route is a deliberate, narrow
  exception to ADR-020 and must stay public-certificate-only.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

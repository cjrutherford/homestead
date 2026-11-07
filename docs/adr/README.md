# Architectural Decision Records

ADRs are the binding record of Homestead's architecture. Code must follow accepted ADRs; an ADR change comes first, implementation second.

## Location and naming

- Records live in `docs/adr/`, one file per decision.
- Filename: `ADR-<NNN>-<kebab-case-slug>.md`, numbers zero-padded and sequential from `001`.
- Never reuse a number. Never rename an accepted record's file.

## Required format

Every record has exactly this shape:

```markdown
# <Decision title>

## Status
Accepted

## Decision
<What was decided, in one or two paragraphs.>

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
```

Additional sections (e.g. `## Context`, `## Alternatives`) may be inserted between `Decision` and `Consequences` when they aid review, but `Status`, `Decision`, and `Consequences` are mandatory and must appear in that order.

## Status lifecycle

- `Proposed` — under review, not yet binding. Implementation must not rely on it.
- `Accepted` — binding on all current and future slices.
- `Superseded by ADR-<NNN>` — no longer binding; the named record governs. The file itself is kept immutable as history.

Transitions allowed: `Proposed` → `Accepted`, `Accepted` → `Superseded by ADR-<NNN>`. No other transitions. Accepted records are never edited in place for substance — a correction is a new superseding record. Typo fixes that do not change meaning are the only in-place edits permitted.

## Amendments

An accepted record may carry a short `## Amended by ADR-<NNN>` section,
placed between `Decision` and `Consequences`, that only points to the
accepted record that narrows or extends it. The note never changes the
original `Decision` text or `Status`; the amending ADR holds the substance
and must say it amends the earlier record.

## Rules

1. Preserve all accepted ADRs and customer-ownership guarantees.
2. If architecture must change, create or supersede an ADR first — before implementation.
3. Do not pull deferred features (see `docs/DEFERRED.md`) into scope without an ADR and slice update.
4. Every slice that touches architecture cites the ADRs it preserves.

## Adopted records

Conformance is checked automatically by `tests/test_s001_adr_framework.py`.

| ID | Record | Status |
|----|--------|--------|
| ADR-001 | Native Go Control Daemon | Accepted |
| ADR-002 | Angular Dashboard | Accepted |
| ADR-003 | SQLite and YAML State | Accepted |
| ADR-004 | CLI / Dashboard Parity | Accepted |
| ADR-005 | Docker Compose Runtime | Accepted |
| ADR-006 | Local-first Exposure | Accepted |
| ADR-007 | Customer Domain and Split DNS | Accepted |
| ADR-008 | Homestead Firewall Boundary | Accepted |
| ADR-009 | DNS Provider Abstraction | Accepted |
| ADR-010 | TLS Provider Abstraction | Accepted |
| ADR-011 | Remote Access Providers | Accepted |
| ADR-012 | Single Appliance Scope | Accepted |
| ADR-013 | Mounted Storage Only | Accepted |
| ADR-014 | First-party Catalog | Accepted |
| ADR-015 | Secure First Claim | Accepted |
| ADR-016 | Bootstrap Trust Ladder | Accepted |
| ADR-017 | Tunnel Remote Access | Accepted |
| ADR-018 | Static Container Packages | Accepted |
| ADR-019 | Clean-room UI Reuse | Accepted |
| ADR-020 | Operator Session Boundary | Accepted |
| ADR-021 | Secret Key Custody and Re-key | Accepted |
| ADR-022 | Backup Encryption Format | Accepted |
| ADR-023 | Daemon Scheduler | Accepted |
| ADR-024 | Unattended Certificate Renewal | Accepted |
| ADR-025 | Release Artifact Trust Root | Accepted |
| ADR-026 | Scheduler Unavailable Retry | Accepted |
| ADR-027 | Privileged Apply Unit for Platform Updates | Accepted |
| ADR-028 | Craft Suite Packaging and Image Trust | Accepted |
| ADR-029 | Nextcloud WebDAV Proxy for Craft Apps | Accepted |
| ADR-030 | Internal CA Trust Distribution and Local TLS | Accepted |
| ADR-031 | Craft Apps Start Page | Accepted |
| ADR-032 | Craft File Proxy Reaches the Daemon over a Dedicated Unix Socket | Accepted |
| ADR-033 | Dashboard SSO via Authentik with a Break-glass Owner | Accepted |
| ADR-034 | Required Post-claim Setup Walkthrough | Superseded by ADR-036 |
| ADR-035 | Dashboard on the Local Network through the Edge | Accepted |
| ADR-036 | Step-by-step Setup Wizard | Accepted |
| ADR-037 | Public Domain, Planned Tunnel, and the Public Dashboard behind Authentik | Accepted |
| ADR-038 | Tunnel Teardown Restores the Records Its Setup Replaced | Accepted |
| ADR-039 | App Names, App Sign-in, and Nightly Backups | Accepted |
| ADR-040 | App Sign-in: Back Channel, Trust, and Adapters | Accepted |
| ADR-041 | Public App Names on the Tunnel, and Tunnel Adoption | Accepted |
| ADR-042 | Bootstrap Installs Docker and cloudflared | Accepted |
| ADR-043 | People and App Access Through Authentik Groups | Accepted |
| ADR-044 | Apps Under One Host, by Path | Accepted |
| ADR-045 | Craft Apps Under One Host, by Path | Accepted |
| ADR-046 | Tunnel Setup Updates the Dashboard's Sign-in | Accepted |
| ADR-047 | Published Apps Sign In at Authentik's Public Address | Accepted |
| ADR-048 | Approved App Setup Runs on the Appliance; Tunnel Setup Issues the Public Certificate | Accepted |
| ADR-049 | People Administration on the Public Dashboard; Authentik Recovery Links | Accepted |
| ADR-050 | An Apps Page for Everyone Who Signs In; Administration Moves to /admin | Accepted |
| ADR-051 | Catalog Integration Metadata Is Advisory | Accepted |
| ADR-052 | Secret Entrypoint Export and OIDC Client Actuation | Accepted |
| ADR-053 | Everything Application Bridge | Accepted |
| ADR-054 | Explicit app access decisions during installation | Accepted |
| ADR-055 | Authentik forward-auth for Navidrome and Uptime Kuma | Accepted |
| ADR-056 | Community container image trust policy | Accepted |
| ADR-057 | Everything apps as catalog packages | Accepted |
| ADR-058 | Everything SDK: sharing, read links, net and events | Accepted |
| ADR-059 | No manual first run for any catalog app | Accepted |

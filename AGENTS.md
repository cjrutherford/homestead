# Agent Handoff

## Mission
Implement HAI Homestead one slice at a time. Do not redesign architecture implicitly.

## Non-negotiable principles
- Customer ownership; no runtime lock-in.
- Native Go daemon (`homestead-server`) managed by systemd.
- CLI and Angular dashboard must have configuration parity through the same API.
- SQLite for operational state; YAML for inspectable desired/generated configuration.
- Docker Compose remains usable without Homestead.
- Local-first networking; explicit exposure; owner may override recommendations.
- Single-appliance scope only. No clustering/fleet management.
- Mounted storage only for v1; no formatting/partitioning.
- First-party curated catalog only for v1.
- Public exposure must be planned, previewed, and auditable.
- Management remains local/overlay by default.

## Slice execution rule
Before coding a slice:
1. Read `docs/PROJECT.md`, `docs/ARCHITECTURE.md`, relevant ADRs, and the slice file.
2. Confirm dependencies are complete.
3. Keep the slice narrowly scoped.
4. Add tests and docs.
5. Update `docs/DEFERRED.md` for discovered out-of-scope work.
6. Leave main buildable and runnable.
7. Land the slice as exactly one commit on main: never combine slices in a
   commit, never leave a completed slice uncommitted.
8. If the slice adds, removes, or renames API routes, CLI commands, or
   dashboard surfaces, update `docs/PARITY.md` — the parity test enforces it.

If architecture must change, create or supersede an ADR first.

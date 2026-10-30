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
- First-party curated catalog only for v1; the one exception is the pinned Jellyfin SSO plugin inside the Jellyfin package (ADR-039).
- Public exposure must be planned, previewed, and auditable.
- Management remains local/overlay by default; the only public management path is the dashboard through the tunnel, behind Authentik sign-in, planned and approved (ADR-037).

## Model and delegation policy

The parent thread chooses every model explicitly. Cheap by default; pay for judgment only.

| Work | Model | Effort |
|---|---|---|
| Parent thread, when the slice file is already unambiguous | `gpt-6-luna` | medium |
| Parent thread, when a slice needs ADR-level judgment, ambiguous failures, or final review | `gpt-6.1-terra` | medium |
| Default worker: one named slice plus its tests and docs | `gpt-6-luna` | medium |
| Mechanical edit: rename, extract, fixture or doc touch-up | `gpt-6-luna` | low |
| Verification of a finished diff | `gpt-6-luna` | medium |
| Planning or review judgment that does not justify Terra | `gpt-6.1-sol` | medium |
| `gpt-6-astra` | never — Sol beats Astra in every case | — |

- Always pass `target` with an explicit model on `delegate_task`. Omitting it inherits the parent's model, which silently turns every worker into Terra at ten times Luna's token price.
- Never select `gpt-6-astra`. Sol beats Astra in every case, so `gpt-6.1-sol` fills any role Astra would have held. Sol is not an execution default: slices, tests, and verification stay on Luna, then Terra.
- Escalate a worker to Terra only when (a) the task cannot be specified without resolving an ambiguity first, or (b) a Luna attempt already failed on correctness. Name the criterion in the delegation prompt.
- At most 2 concurrent workers. Queue the rest; never fan out a swarm.
- A worker that needs more than 80k input per request is scoped wrong. Re-scope it instead of escalating the model.
- A thread averaging more than 150k input per request, older than 24 hours, or past 300 requests in a day gets a handoff and a fresh thread.
- None of this relaxes the slice rule: one slice, one commit.

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

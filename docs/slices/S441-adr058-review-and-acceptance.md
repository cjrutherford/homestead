# S441 - ADR-058 review plan and acceptance docs

## Goal and dependencies
Close ADR-058 (slice row "Review plan and acceptance docs ... ADR-057 table refresh"). Depends on S425-S440. Docs only: no code, package, catalog or test change.

## What changed
- `docs/adr/ADR-057-...md`: every row of the SDK surface table states the current truth and which packages enable it, checked against `catalog/*.yaml` (sharing switch `HOMESTEAD_EVERYTHING_SHARING=1`: Tasks, Circuits, Parametric; net switch `HOMESTEAD_EVERYTHING_NET=1`: `api-client`) and `scripts/everything/apps/*.json` `sdk_surface` (read links: Tasks, Notes; events: Tasks). "Hidden until their slice" wording is gone.
- `docs/reviews/EVERYTHING_REVIEW_PLAN.md`: one section "ADR-058 owner acceptance (S441)" for VM 2 with Authentik, cross-referencing the S436-S440 sections instead of repeating them.
- `docs/DEFERRED.md`: the live-unproven ADR-058 items are listed once, each pointing to its review-plan step.
- ADR-058 and `docs/SLICE_PLAN.md` mark the implementation complete; `docs/SLICE_INDEX.md` line added.

## Stale wording noticed, not changed (data, outside a docs-only slice)
- `scripts/everything/apps/*.json` `sdk_surface.disabled.net` strings say "no network bridge (S381 only)" for apps that do not use net; still true (net is off for them), only the S381 reference is old.
- Tasks' `sdk_surface.refused` is empty and its `supported` list names `events.onLocal`, not `events.on`.

## Tests
No test change. `go build ./...`, `go test ./internal/... ./cmd/...` and `python3 -m pytest -q tests` are run unchanged.

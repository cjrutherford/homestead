# S488 - No route select for publishing

## Goal and dependencies
Gap G8: should the public-name form offer `--route`? Depends on S487.

## Finding
`--route` chooses among an app's edge routes (`appsso.AppRoutes`: routes whose target is the manifest's `exposes` service and whose host carries the app name). All 46 catalog manifests declare exactly one service with `exposes`, so an installed app has one route; the `.local` name is an alias of that route (`Route.AllNames`). The server already defaults to the first route.

## What changed
Docs and tests only. No UI, route or CLI change. `docs/PARITY.md` notes on the public plan and apply rows, `docs/DEFERRED.md` (when to revisit), `docs/SLICE_PLAN.md`.

## Verification
`tests/test_s488_public_route_not_needed.py` fails if a catalog manifest gains a second exposed service, which is the signal to build the select.

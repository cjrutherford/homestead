# S395 — Moving Authentik keeps public sign-in reachable

## Goal
Stop `app.move` taking public dashboard sign-in offline. Live failure
2026-10-31: with the dashboard on the tunnel and sign-in linked, moving
Authentik from `authentik.lan` to `/apps/auth/` (ADR-044) set the route's
`Published = false`, so `https://<dashboard>/apps/auth/` answered 404 through
the tunnel and public sign-in and recovery links broke. The only repair,
tunnel teardown plus a fresh setup, also dropped every app public name.

## Dependencies
S322 (tunnel sign-in), S337 (path publishing), ADR-044.

## Design
- `authentikPathPublished` (tunnel.go) is the one predicate: the dashboard
  route and a path Authentik exist and sign-in is linked. `buildTunnelPlan`
  uses it to add the Authentik path to `plan.Paths`; the move uses it too.
- `keepsAuthentikPublic` (appmove.go) projects the move and also requires the
  dashboard route to already carry tunnel names. The move never creates a
  tunnel. The result is kept in the plan (digest-bound) and the edge step
  sets `Published` to it instead of `false`, so the rendered tunnel block has
  the `/apps/auth/*` handle.
- The plan's sign-in notes say whether Authentik's path stays published, or
  that it is not published because sign-in is not linked. The existing
  refusal of a public host name (path back to host is not a move; an
  Authentik with a public name must be torn down first) is unchanged.
- `preserveTunnelNames` (PUT edge table) also keeps a stored path route's
  `Published`, as only tunnel setup and teardown change publication.
- Other writers of `Published = false` are craft app routes only
  (`pathCraftRoutes`, `MigrateCraftRoutes`); `setTunnelNames` is teardown/setup.
  Re-render and sign-in re-apply do not touch routes.

## Acceptance Criteria
- Move with tunnel and linked sign-in: stored route Published, rendered
  tunnel entry contains `handle /apps/auth/*`, plan says it stays published.
- No tunnel or sign-in unlinked: stays unpublished; the plan says why.

## Verification
`internal/server/appmove_test.go`.

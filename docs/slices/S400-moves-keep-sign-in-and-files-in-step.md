# S400 — Moves keep sign-in and the craft files link in step

## Status
Complete (automated tests). Live check on VM 2 pending.

## Bugs
- A. `apps move apply wordcraft` (and jellyfin) failed with `422 catalog:
  configuration needs at least one change`: the move always built a
  configuration change, but apps whose path setting is a seed file (Jellyfin's
  network.xml) or nothing (Wordcraft) have no environment change.
- B. After a move, the app's sign-in still named the old address until the
  owner re-planned it.
- C. After Nextcloud moved to /apps/nextcloud, the craft files link still held
  the old name, which no longer serves Nextcloud.
- D. Jellyfin's shown sign-in URL had no app path on a host-served Jellyfin
  with BaseUrl /apps/jellyfin.

## Decision
No architecture change (ADR-044, ADR-047, ADR-048 unchanged).

- A. The configure step runs only when the move has environment changes
  (`configure.go` is unchanged and still refuses an empty change). When the
  move wrote seed files, the stack was stopped, so a plain `up` step
  (`catalog.StartStack`) brings it back. With neither, nothing was stopped and
  no restart is needed.
- B. Like S399, the move plan projects the edge table after the move (path set,
  tunnel names cleared, Published as the move leaves it), builds the app's
  sign-in plan over it, shows the change in `sign_in` and binds its digest as
  `sign_in_digest` (part of the move digest). A final `sign-in` step applies it
  through `reapplyAppSignIn`; a failure reads "<app> moved, but its sign-in
  still uses the old address: ...; re-plan it from its card ...". Without a
  record, a kept Authentik token or a previewable plan, the earlier "re-plan
  after the move" line stays. Authentik's own move is unchanged.
- C. When Nextcloud moves and the craft files link holds its old address, the
  plan lists "Craft files link moves to <new base>" under `links` (the new and
  old addresses are in the digest) and a `files-link` step runs
  `craftfiles.Manager.MoveBaseURL`, which changes only the base URL: the account,
  folder and sealed credential stay, so no password is needed. A link that
  points elsewhere is left alone. The step is audited as
  `craftfiles.link.move`.
- D. The sign-in URL of the Jellyfin adapter gets the manifest's app path
  unless the origin already ends with it (`appsso.signInURL`), so host-served
  and path-served Jellyfin both show `<origin>/apps/jellyfin/sso/OID/start/authentik`.
- E. The public plan no longer tells the owner to re-plan when sign-in has
  nothing to change ("needs no change") and keeps BuildPlan's warnings (the
  public issuer is not served yet) in either case.

## Tests
internal/server/appmove_signin_test.go: a move with neither env nor seeds and
one with seeds only both succeed with the real configure (stack stopped and
brought back up); the move shows and applies the sign-in change and the craft
files link move, the digest covers both, the credential is untouched; a
sign-in failure is reported after the move; no kept token leaves sign-in to a
re-plan and a link elsewhere is left alone. internal/server/apppublic_signin_test.go:
a path publish (Published toggle) previews and applies sign-in; no change does
not ask for a re-plan; token not kept. internal/appsso: Jellyfin sign-in URL.

## Not in this slice
See DEFERRED. No routes, commands or surfaces were added or renamed (new JSON
fields `sign_in_digest` and `links` on the move plan), so PARITY.md is
unchanged.

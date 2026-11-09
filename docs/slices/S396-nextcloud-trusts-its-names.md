# S396 — Nextcloud trusts every name Homestead serves it on

## Goal
Live failure 2026-10-31 (VM 2): Nextcloud's edge route is `nextcloud.lan`, but
its `trusted_domains` held only localhost, homestead.lan, homestead.local and
the public dashboard name. `https://nextcloud.lan` answered 400 (untrusted
domain), the app-password endpoint answered 400, and the GridCraft setup step
`nextcloud-link` stopped.

## Root cause
`NEXTCLOUD_TRUSTED_DOMAINS={{route_names}}` is read only on the image's first
install. After that, Homestead changed the list on exactly two paths: the
`app.move` `app-hosts` step and the public-name add/remove steps (S313). The
other writers of an app's served names never touched it: the edge route table
(`POST /api/v1/edge/routes`), the appliance-run address step (ADR-048), tunnel
setup and teardown, adoption, craft migration. A Nextcloud installed while
served under the appliance names (path), then given or returned to a name of
its own, kept the old list. Which operation did it on VM 2 cannot be told from
the install record; the fix covers all of them.

## Design
- `reconcileAppHosts` (internal/server/apphosts.go) is the one hook: for each
  installed catalog app that keeps an allow-list (today Nextcloud), it adds all
  names its edge routes answer on, local names and aliases (path routes: the
  dashboard's names) plus public names, through the existing `trustAppHosts`
  (`appsso.TrustHosts`). Existing entries are never removed.
- `startEdgeReconcile` (edge table POST and the address step) appends an
  `app-hosts` step, running only for apps whose served names changed
  (snapshot before vs. after), so unrelated edge changes never need Nextcloud
  running. A failure fails the operation with the app's name and reason.
- The `nextcloud-link` step runs the reconcile first (when it targets this
  appliance's Nextcloud). `apps setup resume` re-runs that step, so resume is
  the repair path: it trusts `nextcloud.lan`, then links. A failed reconcile
  stops the step with "Nextcloud does not trust the names it is served on".
- Move and public-name steps keep their own calls (they already pass local
  names, plus the public one).

## Tests
`internal/server/apphosts_test.go`: missing local and public names added;
unchanged names skip the app; changed names reconcile; errors surface;
craft-link succeeds only after the reconcile and stops with the reason when it
fails. Existing move and public tests cover the move and public-name calls.
The craft-link rig stubs `trustAppHosts`.

## Out of scope
See DEFERRED: removing Homestead-added stale names; tunnel setup/teardown and
adoption not running the hook outside the edge reconcile.

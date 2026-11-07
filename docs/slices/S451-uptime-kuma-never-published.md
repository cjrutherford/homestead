# S451 - Uptime Kuma is never published, enforced

## Goal and dependencies
Uptime Kuma's manifest said it is never published, but only in prose; S450's live install gave it a public name. Depends on S450, ADR-055.

## Cause
The rule existed as `allowwider: false` plus text. `allowwider` only gates exposure widening (local to overlay or public); the public-name path (`apps public`, wizard "Also publish at", setup.public) never read it, and craft apps also carry `allowwider: false` yet are published by path (ADR-045), so it cannot mean "never publish".

## What changed
- Manifest field `never_publish: true` (`catalog.Manifest.NeverPublish`, `PublicRefusal()`), set on `uptime-kuma`.
- `buildPlanState` refuses any add (plan, apply, before-install, anticipated sign-in, setup.public at install, `apps install --public`) with 422 "is never published"; removal of a name the app somehow holds stays planned and approved as before. The install handler refuses `public_name` and `setup.public` up front.
- `GET /api/v1/catalog/{name}/public` returns `never_publish`; not available (no proposed name) unless a name exists, in which case only Remove is offered.
- Dashboard: the install wizard's "On the internet" shows the refusal instead of the publish toggle; the app page's Public address shows the refusal and Remove only.
- Doctor (tunnel group) check `never-publish`: fail when a never-publish app holds a tunnel name, with the removal command as the fix. Local state only.
- Audit: no other manifest claims "never published", "refused by design" or similar (craft apps advise "keep it local", but are deliberately publishable by path); `TestNeverPublishFieldMatchesProse` guards the pairing.
- Checklist SRV-UPTIME-KUMA-1 says the review offers no public address.

## Live (VM 2)
The existing public name uptime-kuma.hopefulaspirationsindustries.com was removed through `apps public plan/apply --off` (DNS record deleted, connector rewritten). Navidrome's open `/rest` returns Subsonic errors 10 and 40 without valid credentials from the public name.

## Tests
`internal/server/apppublic_neverpublish_test.go`, `dashboard/src/app/catalog/app-public.spec.ts`, `install-wizard.spec.ts`, `tests/test_s451_uptime_kuma_never_published.py`.

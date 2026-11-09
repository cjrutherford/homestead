# S408: path apps sign in at Authentik's public address

## Problem
On VM 2, Nextcloud at `https://homestead.lan/apps/nextcloud/` still signed in
at `https://homestead.lan/apps/auth/...`, because ADR-047 §1 gave the public
issuer only to published apps. The owner wants every app on the `/apps/` path
to use Authentik's public address.

## Change
- `appsso.BuildPlan` uses the public Authentik address for an app served by
  path whenever the public address is answered, published or not. Host apps
  keep the ADR-047 §1 rule.
- The "visitors through the tunnel cannot sign in yet" warning stays limited
  to published apps.
- ADR-047 amended (S408).

Applied apps see a re-plan in sign-in status; the tunnel setup's issuer move
picks them up too, since it builds the same plan.

## Tests
- `TestPublishedAppsSignInAtPublicAddress`: an unpublished path app gets the
  public issuer, and keeps the local one without a warning while no public
  address is answered.
- `TestApplyOnThenOff`: an unpublished host app (Immich) keeps the
  local issuer with the public address answered.

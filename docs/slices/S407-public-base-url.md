# S407: single-base-URL apps use their public address once published

## Problem
Mealie is published at `https://mealie.<domain>` and signs in through
Authentik's public issuer (ADR-047), but it builds its OIDC redirect from
`BASE_URL`, which the manifest set to `https://{{route_host}}`: always the local
edge name. After signing in remotely the browser was sent to
`https://mealie.lan/login`, unreachable off the LAN. Vaultwarden's `DOMAIN` had
the same defect.

## Change
- New catalog env token `{{app_url}}` (`catalog.AppURLToken`): `https://<public
  hostname>` when the app has a public name of its own, else
  `https://<route host><route path>` (the `{{route_url}}` value). Validated like
  the other route tokens (the service must expose an endpoint).
- `catalog/mealie.yaml` `BASE_URL` and `catalog/vaultwarden.yaml` `DOMAIN` use it.
- `Generation.PublicHosts` / `InstallRequest.PublicHostname`: an install that is
  public at install time (`public_name` or the approved setup's public name)
  renders the public address.
- Public add and remove plans of an installed app compare the stack's effective
  value (generated compose, then override file) with the token's new value. A
  difference becomes a plan line (`mealie: BASE_URL: <old> -> <new> (the app
  restarts)`), is covered by the digest, and runs as the app move's configure
  steps (`write-config`, `up`) after the edge steps and before `sign-in`.
- Already public with a stale value (VM 2's Mealie): planning `public add` for
  the same hostname, which used to be refused as "already on the tunnel", now
  plans a `refresh` when the base URL is stale. It changes only the base URL: no
  DNS, edge or connector step. With nothing stale it is still refused.

Operator command:

    homesteadctl apps public plan  mealie --hostname mealie.<domain> --route mealie.lan
    homesteadctl apps public apply mealie --hostname mealie.<domain> --route mealie.lan --digest <digest> --follow

## Tests
Catalog: token renders public and local, unrouted drops it, validation,
`AppURLChanges` (compose, override, local). Server: add and remove plans carry
the change and the digest differs from a current stack; the configure steps run;
refresh of a stale public app; refresh with nothing to do is refused.

## Out of scope
Path-served apps keep the local base URL; the first public name wins when a route
has several. See DEFERRED.

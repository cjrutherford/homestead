# S327 — A "Sign in with Authentik" button on Jellyfin's login page

## Goal
ADR-048 item 4. The pinned Jellyfin SSO plugin (4.0.0.4) adds no button to
Jellyfin's login page, so people had to know the sign-in link. The owner
wants a button on the page.

## Scope
- The Jellyfin adapter (`internal/appsso/jellyfin_branding.go`) edits
  Jellyfin's branding settings, `/config/config/branding.xml`
  (`BrandingOptions`), when sign-in is applied or turned off:
  - `LoginDisclaimer` gains a link, "Sign in with Authentik", to the
    plugin's start URL under the app's path base: with the app on
    `/apps/jellyfin/` it is `/apps/jellyfin/sso/OID/start/authentik`
    (provider `authentik`, the one the adapter registers; the redirect
    stays `/sso/OID/redirect/authentik`). The base comes from the app's
    route, so one link serves the `.lan` and `.local` names and the
    public name.
  - `CustomCss` gains a rule that styles the link as a button.
- Both additions sit between markers
  (`<!-- homestead:sso -->` ... `<!-- /homestead:sso -->` and
  `/* homestead:sso */` ... `/* /homestead:sso */`).
  - Applying again replaces only the marked block.
  - Turning sign-in off removes only the marked block.
  - The owner's own disclaimer and CSS, and any other branding element
    (for example `SplashscreenEnabled`), are kept. Removal never creates
    the file.
- Plan text: the on lines say the login page gets the button and that a
  disclaimer or CSS the owner wrote is kept; the off lines say it loses
  it. Jellyfin plan digests change because their text did.

## Design note
The adapter has no Jellyfin admin session, only the container, so it writes
the branding file the way it writes the plugin's settings, instead of
calling `/System/Configuration/branding`. Jellyfin is restarted afterwards,
as it already was, so it reads the file.

## Tests
`internal/appsso/jellyfin_branding_test.go`: start URL under the path base,
insert into empty and alongside owner text, idempotent re-apply, replace on a
new base, removal keeps owner text and other elements, no file written when
nothing is to remove, adapter calls through the container.

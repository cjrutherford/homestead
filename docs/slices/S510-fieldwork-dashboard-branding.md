# S510 — Fieldwork supporting identity in the dashboard

## Goal

Integrate the supplied Fieldwork navigation and catalog illustrations in the
existing dashboard, retain Homestead's primary mark and theme tokens, and
prepare the next version of the local Debian package artifacts.

## Dependencies

- S309 dashboard design language and current Lamplight tokens.
- S340 manifest-keyed app icon fallback.
- S509 Debian package builder.

## Scope

- Keep `assets/homestead-logo.png` as the primary mark and pair it with the
  Homestead wordmark in the admin shell and apps header.
- Use the supplied monochrome supporting glyphs beside existing navigation
  labels. They inherit foreground color, remain decorative to assistive
  technology, and do not replace text.
- Add custom monochrome art for the five supplied IDs that exist in current
  catalog manifests: `nextcloud`, `immich`, `jellyfin`, `paperless-ngx`, and
  `vaultwarden`. The catalog label remains visible; all other packages retain
  their existing local MDI icon and monogram fallback.
- Retain the full supplied source, light/dark and monochrome SVGs, PNG exports,
  proof sheets, and manifest under `assets/brand/fieldwork/source/`.
- Bump `VERSION` and the rollback release counter for a new release build; the
  package artifacts include the updated embedded dashboard.

## Not built

- No primary vector or favicon is present in the bundle. The current corn mark
  and favicon remain in use.
- The `collabora` and `home-assistant` illustrations are not mapped because
  those are not standalone IDs in this checkout's catalog.
- The proposed Fieldwork palette is not applied over current theme tokens.
  The marketing landing-page concept is not deployed.
- No route, API, CLI, access policy, network, or app lifecycle behavior is
  changed. `docs/PARITY.md` remains unchanged.

## Verification

- `tests/test_s510_fieldwork_branding.py` checks navigation asset references,
  package ID mappings, and preservation of the existing logo.
- Dashboard tests cover the supplied icon and fallback behavior; the
  production dashboard build embeds the new public assets in both release
  architectures.
- The new `.deb` release is metadata/content checked for amd64 and arm64.
  Arm64 is cross-built and statically inspected; runtime on ARM hardware is not
  claimed.

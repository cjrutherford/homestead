# Fieldwork supporting identity assets

The supplied `source/` bundle is retained for reference, including its proof
art, editable SVGs, application icon PNG sizes, manifest, and handoff notes.
S510 implements the supporting glyphs in the dashboard; it does not replace
the primary brand mark or change the dashboard's established color tokens.

## Integration

- `source/icons/*.svg` are copied unchanged to
  `dashboard/public/icons/fieldwork/`. The shell uses them as 24px
  current-color masks beside persistent navigation labels. Their artwork is
  decorative and hidden from assistive technology; the labels remain the
  accessible names.
- `assets/homestead-logo.png` remains the primary raster mark and is copied to
  `dashboard/public/brand/homestead-logo.png`. It appears beside the
  Homestead wordmark in the admin shell and apps header. The UI clips the
  lower wordmark area in its fixed symbol window without changing the source
  image; the separate text stays readable. The bundle supplied no primary
  vector or favicon, so the existing favicon remains unchanged.
- The catalog inventory was compared with the 46 current package manifests.
  Matching IDs are `nextcloud`, `immich`, `jellyfin`, `paperless-ngx`, and
  `vaultwarden`. Their provided monochrome SVGs are copied unchanged to
  `dashboard/public/catalog-icons/` and displayed in catalog and user-app
  tiles. Monochrome variants inherit the current text color, including the
  high-contrast theme; names stay visible next to icons.
- Proposed `collabora` and `home-assistant` entries do not match current
  standalone catalog IDs. Those illustrations remain in the supplied source
  bundle and are not wired to another package. Other packages retain their
  existing manifest-keyed Material Design Icons and fallback behavior.
- Dark/light art, monochrome source, and 32/64/128/256 PNG exports stay in
  `source/catalog-icons/` as supplied. The application uses the monochrome
  vector because current theme tokens and high-contrast mode control its
  foreground. No third-party application logo is presented as Homestead
  artwork.

## Boundaries

The Fieldwork palette is a proposal and is not mapped over the existing
Lamplight tokens. Typography remains the bundled Atkinson Hyperlegible and
Barlow families. The landing-page concept in the source handoff is a future
static-site reference, not a deployed product page. This integration changes
dashboard presentation only; it does not add routes, API/CLI behavior, or
application lifecycle behavior.

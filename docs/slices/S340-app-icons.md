# S340 — App icons

## Goal
Render each app's manifest `icon: mdi:<name>` on the apps page tiles and the
catalog page, offline.

## Scope
- `@mdi/js` (Apache-2.0, path strings) is a dashboard dependency. Only the
  icons the catalog names are imported, in `dashboard/src/app/ui/app-icons.ts`,
  so only their paths are bundled. Nothing is fetched at runtime.
- `<hs-app-icon [icon] [title]>` draws an inline, `currentColor`, aria-hidden
  SVG path; an unknown or empty name falls back to the title's first letter.
- `catalog/jellyfin.yaml` icon changed from `mdi:plex` (a competitor's logo) to
  `mdi:television-play`. Catalog manifests are not pinned by digest or
  signature (only the craft manifest is signed, and it was not touched).
- Third-party notice: `@mdi/js` is Apache-2.0; the repo has no notice file, so
  none was added.

## Acceptance
Component specs (known icon renders a path; unknown falls back); a Python test
fails when a manifest names an icon missing from the map.

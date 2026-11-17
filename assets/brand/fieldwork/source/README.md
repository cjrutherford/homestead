# Homestead — Fieldwork direction (concept handoff)

Status: proposed supporting identity only. No application changes authorized. Existing 1254 × 1254 corn/network PNG is retained; its source was not supplied and is not included. Primary vector, favicon, and app-icon exports are pending inspection of that source. Do not substitute navigation glyphs for the brand mark.

## Visual language
Grounded, technical, warm. Use field-like parallel lines, modular plots, rounded junctions and clear boundaries sparingly. Avoid literal barns, rustic textures, locks as generic security claims, and decorative dashboard clutter. The supporting glyphs use a 32-unit grid, 2-unit round strokes and currentColor. Use at 24–32 CSS px; test at 16 px before adopting. Keep 4 units of external breathing room. Use text labels for navigation. Existing catalog app marks remain unchanged.

## Proposed palette (not verified against current Lamplight tokens)
| Role | Dark | Light |
|---|---|---|
| Canvas | #162822 | #FAF6EC |
| Surface | #21382E | #FFFFFF |
| Text | #F4EBD8 | #162822 |
| Muted text | #B8C9BD | #476052 |
| Accent | #E9C77B | #79551A |
| Brand green | #91C4A3 | #245D40 |

Map semantically to existing tokens after review. Preserve existing high contrast/system behavior and status colors. Check WCAG contrast for actual pairs: normal text 4.5:1, large text and meaningful non-text indicators 3:1. Do not encode status through hue alone.

Type: retain bundled Atkinson Hyperlegible Next for UI/body, Atkinson Hyperlegible Mono for commands, Barlow Semi Condensed for headings. No new fonts distributed. Verify bundled font notices in repository before redistribution.

## Mark usage (provisional)
Retain corn logo as primary. Once source is obtained, create true vector symbol, stacked and horizontal wordmarks, one-color variants and 16/32/48 favicon, 192/512 app PNGs, ICO and SVG. Simplify fine detail for tiny sizes. Start clear space at one quarter of symbol width; verify after mark inspection. Keep HAI endorsement secondary; do not fuse the two marks. Avoid stretching or recoloring the original raster.

## Landing page concept (not deployment)
Hero: “Your cloud. Your ground.” Supporting line: “A private cloud, built on hardware you own.” Primary action: installation guide; secondary: source repository. Use actual links only when verified. Show real dashboard photography/screenshots once supplied, not mock operational evidence. Follow with Install applications / Manage people and access / Plan public routes / Keep your Compose files. Explain one Debian or Ubuntu Server host, already-mounted storage, curated catalog, and prerequisites for public access. Close with documentation and ownership model. No invented testimonials, compatibility promises or security guarantees. GitHub Pages may host the static marketing site in a later slice; it is not the management plane. No implementation code included.

## Manifest
- icons/*.svg: 12 original editable supporting glyphs, viewBox 0 0 32 32, monochrome currentColor. No third-party assets embedded. Proposed for inclusion under the project's existing asset licensing terms, subject to owner approval.
- icon-proof.svg: editable 960 × 690 presentation sheet; generic system sans-serif, no bundled fonts.
- README.md: usage and landing-page direction.
- Existing logo, primary mark variants, app icons and favicon: not included, pending source review.


## Catalog illustration addendum
See `catalog-icons/README.md`, `catalog-icons/manifest.json` and `catalog-icons/proof.png`. Seven provisional application illustrations have been added; actual repository coverage remains unverified.

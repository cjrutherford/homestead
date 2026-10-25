# S310 — Lamplight: depth, a named language, and dark by default

## Goal
S309 gave the dashboard a hierarchy but no depth: in the dark theme the
sidebar, canvas and panels are near the same value, every panel sits at
the same height, and the language (permit, field) is too quiet to read as
a language. The owner also wants dark to be the default: light only when
someone asks for it.

S310 builds on S309 (keep the masthead, fields, furrow, permit and stamp)
and gives it one organising idea: **a homestead at night, seen by
lamplight.**

## Dark by default
- With no stored choice the dashboard is **dark**, whatever the system
  preference. Light is applied only when the owner picks it. "Match my
  system" stays as an explicit option. High contrast unchanged.
- First paint must be dark with no light flash: make dark the CSS default
  (`:root` with no `data-theme`), `[data-theme='light']` selects light,
  and `[data-theme='system']` uses `prefers-color-scheme`. Keep the
  existing spec that enforces identical duplicated theme blocks, adjusted
  to the new arrangement.
- Picker order: Dark, Light, Match my system, High contrast.
- ThemeService and its spec updated; the validation board still shows all
  themes side by side.

## The language: five rules
These are the language. DASHBOARD_DESIGN.md opens with them, and every
component follows them.

1. **One light source.** Light falls from the top of the page, where the
   masthead is. A surface nearer the viewer is lit more: a lighter fill, a
   1px top highlight (inner, light at low alpha), and a longer, softer
   shadow below it. Every shadow falls downward from the same light.
   Nothing invents its own shadow.

2. **Four depths, named and tokenised.**
   - **Hedgerow**: the sidebar, the deepest plane. It is darker than the
     ground, and the canvas casts a shadow onto it along its edge, so the
     page visibly sits above the band. The current page is a lit tab:
     raised fill, top highlight and a lamp bar on its inline-start edge.
   - **Ground**: the canvas.
   - **Plate**: fields. They are raised from the ground with
     `--elevation-plate`.
   - **Lifted**: the permit, menus, dialogs, the drawer, and the Needs you
     field. They use `--elevation-lifted`.
   - **Well**: the sunken depth. Inputs, code, ledgers, table bodies and
     logs are cut into their plate with an inset shadow (`--elevation-well`).
     Detail always reads as recessed and actions as raised: buttons are
     small plates with a top highlight that press down (shadow collapses,
     1px translate) on `:active`.
   Tokens: `--elevation-well`, `--elevation-plate`, `--elevation-lifted`,
   `--highlight-top`, `--edge-shadow`.

3. **Light means attention.** Only things that need the owner glow.
   - The Needs you field is a lit window. It has a warm lantern glow: an
     amber outer glow (`--glow-attention`), the warm tint, and the furrow
     brighter than the other furrows.
   - State dots and the state glyphs in tables are small lamps. An ok
     state is a steady Meadow lamp with no halo. A warn or failure lamp
     has a soft halo of its colour.
   - Whole fields never glow for ok. The focus ring is a lit ring: the
     existing outline plus a soft halo.
   - No animation of light. The S309 stamp press stays the only
     non-user-triggered motion.

4. **The land.** The masthead sits on a band carrying faint topographic
   contour lines: an inline SVG data URI in a token (`--contour`), at very
   low opacity. The lines are lit by the lamp, brightest top-left and
   fading out across and down into the ground. This is the only texture in
   the product. The masthead rule becomes the **horizon**, a 2px line lit
   at its inline-start end that fades to transparent by the far end. The
   h1 grows to ~3.25rem, and the sentence under it becomes a lead at
   ~1.25rem in `--muted-strong`, a new token if needed, AA on the ground.

5. **The permit is paper under the lamp.**
   - It is the highest surface, with the deepest shadow.
   - The Manila-night fill has a light falloff: a radial gradient that is
     lighter near the top-left and warms down.
   - The perforated edge stays. The seal is embossed with an inset and a
     highlight, and the stamp reads as ink pressed into paper.
   - In dark the permit is warm and lit, but never bright: no white paper
     in the dark theme.

## Colour anchors (dark, the default)
These are starting values to tune; every text and border pair must pass AA
in `tokens-contrast.spec.ts`.
- Hedgerow `#0A100D`
- Ground `#111915`
- Plate `#18221D`
- Lifted `#1F2A24`
- Well `#0D1310`
- Ink `#E6ECE7`
- Muted `#9DAAA2`
- Pine accent `#7FD1A8`
- Lantern `#F2B544`: the glow source; glow alphas stay low
- Meadow lamp `#6FCF7F`
- Brick lamp `#F07A62`
- Manila-night `#2A2619` → `#332D1E` falloff
Shadows in dark are near-black at real alpha (0.45–0.6), because on dark
grounds depth comes from the highlight and value steps more than from the
shadow.

## Light theme ("daylight")
The same five rules with gentler values. Shadows are softer and cool. The
lamp becomes a faint warm wash at the masthead and the contours sit at
very low opacity. Hedgerow stays the deep pine band from S309, and wells
are a step darker than the plate.

## High contrast
Depth comes from borders only: no shadows, no glows, no contours, no
gradients. Wells get a 1px inner border. Keep black, white and yellow.
`forced-colors: active` drops all of the above as well.

## Fix from the S309 review
- Status "Appliance" (and any other inline fact strip) becomes the ledger:
  a two-column definition list in a well.
- The permit's warning callout spans the permit's full content width.

## Rules
Same as S309. In addition:
- Tokens only. Components never write raw shadows or colours.
- All accessibility guarantees are kept: AA, focus, 44px targets, reduced
  motion, one h1, glyph plus word, sentence case.
- Mobile: depth is kept, and the hedgerow drawer is lifted.

## Scope
Tokens, base, shell, primitives, pages, permit and catalog CSS; the
ThemeService default; DASHBOARD_DESIGN.md (the five rules first); the
validation board samples (show the four depths and the lamps).

Out of scope: new data, routes, API or page restructuring.

## Acceptance Criteria
- `pnpm exec ng test` passes, with the new contrast pairs and the
  ThemeService dark default.
- `make dashboard-build` and `make dashboard-embed` succeed.
- With an empty localStorage, the first screenshot is dark.
- Screenshots of Status, Setup, the Tunnel plan review (permit), Catalog
  and one form page (Storage or Account), at desktop and 375px, in Dark
  (default), Light and High contrast. Each is reviewed against the five
  rules.

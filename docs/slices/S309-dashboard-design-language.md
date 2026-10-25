# S309 — Dashboard design language: the permit and the field

## Goal
Give the dashboard a design language of its own, with a clear visual
hierarchy: the owner should see first what needs them, then what the
appliance is doing, then the detail. The current system (S248, S282–S285) is
accessible and tidy but flat: every panel weighs the same, headings barely
outrank body text, and the product's defining act, reviewing and approving
a plan, looks like any other form.

## Subject
Homestead is a customer-owned home server. The owner is a homeowner or
prosumer, not an operator. The dashboard's job is to show the appliance's
state at a glance and to make every change a plan the owner reviews and
approves. The vernacular comes from the homestead itself: land in parcels,
fields worked in rows, signage on a feed store or barn, and the paper permit
that says a change was reviewed and approved.

## Direction
Three tiers, each with a distinct treatment, and one memorable moment.

### Tier 0: the shell recedes
- The sidebar is a deep pine band, **Hedgerow** `#12291F` (dark theme: a
  step darker than the page). The brand wordmark sits in the display face.
  Nav text is light. The current page is a Limewash notch that joins the
  sidebar to the canvas, so the page reads as cut from the band rather than
  merely highlighted.
- The canvas is Limewash and panels are Board, as now.

### Tier 1: the page's one sentence
- Every page opens with a **masthead**: the `<h1>` in the display face at
  about 2.75rem, tight leading, and directly under it one plain sentence of
  the page's most important fact. "Homestead is running. Nothing needs you."
  "2 apps are installed. Jellyfin is updating." A sentence, not a stat card.
  Pages without live data keep a one-line purpose instead.
- Below the masthead sits a single full-width hairline in Pine at 2px. It is
  the only heavy rule on the page.

### Tier 2: fields
- Sections are **fields**: Board panels with `--radius-panel`. A field whose
  content has a state carries a 4px **furrow** on its inline-start edge in
  that state's colour (Signal amber for needs review, Brick for failure,
  Meadow for running/ok). A neutral field has no furrow. The furrow encodes
  state only, never decoration.
- A **Needs you** field, when there is anything, always comes first on
  Status and Setup. It is the one place amber fills a background tint
  (`--warn-surface`). Its rows are actions with a button each.
- Field headings (`h2`) are in the display face at about 1.375rem. Body
  stays Atkinson Hyperlegible Next.

### Tier 3: detail
- Tables, definition lists and logs drop to `--font-size-sm`, with muted
  labels and ink values. Mono stays for hostnames, fingerprints, commands
  and IDs only.
- Definition lists render as a two-column ledger, with labels muted and
  right-aligned against a hairline on wide screens, stacked on narrow.

### The memorable thing: the permit
Every plan review (tunnel, sign-in, exposure, firewall, app install, public
name, backup policy, names resolver, uninstall, platform update) renders as
a **permit**: a document on **Manila** `#F5EEDB` paper (dark theme: a warm
deep `#2A2619`), with a perforated top edge (CSS radial-gradient), the plan
title in the display face, and "Will change", "Leaves alone" and "Refused"
as ruled sections. The plan digest sits in the corner as a **seal**: a
circular mono badge with the first 8 characters, the full digest in its
accessible name.

On approval, a Pine **Approved** stamp (display face, double rule border,
rotated −4°) presses onto the permit: it scales from 1.25 to 1 with opacity
0 to 1 over `--duration-slow`. This is the page's only non-user-triggered
motion. With `prefers-reduced-motion` it simply appears. The stamp carries
the approver and time in its text. A refused plan gets a Brick "Not
approved" stamp with the reason beside it, never rotated, so it reads as a
fact rather than a flourish.

The work-orders rail stays, quieter: it is now Tier 2, and the permit is the
one bold element (update DASHBOARD_DESIGN.md "The one bold element").

## Type
- **Display:** Barlow Semi Condensed SemiBold (600), SIL OFL, self-hosted
  woff2 in `public/fonts`. Barlow is drawn from California highway and rail
  signage, so it suits a utilitarian, signage voice. Use it for `h1`, `h2`,
  the wordmark, the permit title and the stamp only. Sentence case, tracking
  −0.01em, never all caps.
- **Text:** Atkinson Hyperlegible Next (unchanged). **Mono:** Atkinson
  Hyperlegible Mono (unchanged, same restrictions).
- **Scale** (ratio ≈ 1.25 above base): 0.8125 / 0.9375 / 1 / 1.125 / 1.375
  / 1.75 / 2.75rem. Body line-height 1.55; display line-height 1.05–1.15.
  Text measure ≤ 72ch in prose blocks.

## Colour
Existing tokens stay: Limewash `#F2F4F0`, Board `#FFFFFF`, Pine `#1F4D3A`,
Ink `#1C2621`, Signal `#B4690E` (text `#8A4F06`), Brick `#9E2F1E`, Meadow
`#2D7A1F`. New tokens:
- `--hedgerow` `#12291F` and `--on-hedgerow` text;
- `--manila` `#F5EEDB` and `--manila-rule`;
- `--stamp` (Pine) and `--stamp-refused` (Brick);
- `--warn-surface`.

Each new token needs a dark and a high-contrast value. Every new text or
border pair must be added to `theme/tokens-contrast.spec.ts` and pass AA.
High contrast keeps its black, white and yellow: the permit becomes a white
document with a black border, and the stamp is black.

## Rules
- Tokens only (`tokens.css`); no hardcoded values in components.
- Clean-room (ADR-019): no UI framework, no vendored UI code. The font is
  OFL and is recorded in `docs/THIRD_PARTY.md`.
- Keep every existing accessibility guarantee: focus rings, 44px targets,
  reduced motion, one `<h1>` per page, colour never alone (glyph plus word),
  sentence case, no all-caps labels, no middle-dot meta strings, no arrows
  on buttons.
- Mobile: the sidebar drawer, the masthead and fields reflow to one column.
  The permit stays readable at 320px, with the seal moving under the title.

## Scope
- Tokens, fonts, base, shell, primitives and pages CSS.
- A reusable masthead (h1 + sentence) applied to every page.
- A reusable permit component (or class set) used by every plan review that
  exists today.
- The Needs you field on Status and Setup, from data the pages already have.
- `docs/architecture/DASHBOARD_DESIGN.md` rewritten to this language.

Out of scope: new data, routes or API changes; page restructuring beyond
the masthead, field and permit treatments.

## Acceptance Criteria
- `pnpm exec ng test` passes, including the contrast spec with the new
  pairs.
- `make dashboard-build` succeeds.
- Screenshots in light, dark and high contrast, at desktop and 375px, of
  Status, Setup, the Tunnel plan review (the permit) and Catalog, reviewed
  against this brief.

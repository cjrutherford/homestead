# S282 — Dashboard visual foundation

## Goal
Give the dashboard a deliberate visual identity and a usable shell: bundled typeface, palette and themes, styled primitives (buttons, forms, tables, badges, dialogs) and a grouped sidebar navigation that replaces the 26-link top bar.

## Dependencies
S248, S281

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Extends the S248 design system (ADR-019: clean-room, no vendored third-party UI code). Owner approved the "well-kept workshop" direction on 2026-10-18. No UI framework is added; plain Angular and CSS custom properties. No route, API or CLI change.
Baseline found in the 2026-10-18 survey: unstyled browser-default buttons, 26 nav links overflowing to 1868px at 1440px wide, most S248 tokens unused, dark theme defined but never applied, no focus styles, undefined classes (`.button`, `.warning`, `.hint`, `.field`, …), inline `role="alertdialog"` spans without modality.

Design direction:
- Palette (light): Limewash `#F2F4F0` page, Board `#FFFFFF` panels, Pine `#1F4D3A` brand and primary actions, Ink `#1C2621` text, Signal amber `#B4690E` warnings and "needs review", Brick `#9E2F1E` destructive and failure. A matched dark palette and the existing high-contrast theme. All text and control pairs meet WCAG AA (verify with a contrast check in tests or a script).
- Type: Atkinson Hyperlegible Next for all text and Atkinson Hyperlegible Mono only for code-like values (hostnames, fingerprints, commands). Bundled with the build under the SIL OFL (licence recorded in `docs/THIRD_PARTY.md`); no runtime font or asset fetch from any external host. Type scale per S248 tokens, revised if needed.
- Avoid the generic tells: no all-caps eyebrow labels, no middle-dot meta strings, no identical-card grids, no single radius and shadow on everything.

## Acceptance Criteria
- Dashboard visual foundation implemented and documented
- Tokens: palette, type, spacing, radius, elevation and motion tokens in `dashboard/src/tokens.css` for light, dark and high-contrast; hard-coded colours and sizes in component styles replaced by tokens (`44px` → `--touch-target`, `monospace` → `--font-mono`, etc.); undefined classes either defined or removed
- Themes: dark and light follow `prefers-color-scheme` by default with a persisted manual choice (light, dark, system) on the Account page; `color-scheme` set; high-contrast still selectable; link and focus colours readable in every theme (fixes the validation-board link defect)
- Primitives: button variants (primary, secondary, danger, quiet) with hover, active, focus-visible, disabled and busy states; text inputs, selects, checkboxes, textareas and field errors; tables with sticky header and responsive overflow; status badges (ok, warn, fail, pending, running) that pair colour with a glyph and text; a modal dialog primitive on the native `<dialog>` element with `aria-modal`, focus trap and return, Escape, and a danger-confirm variant; every existing inline `alertdialog` migrated to it
- Shell: grouped sidebar navigation (Overview, Apps, Network, Storage and backups, System) with collapsible groups and the current page marked; logged-out visitors see only Status, Login and (when unclaimed) Claim; mobile drawer below the md breakpoint; a real `<h1>` per page, the brand as the site name, and a skip link
- Focus: visible `:focus-visible` styles on every interactive element in every theme
- No horizontal overflow at 375, 390, 768, 1024 and 1440 px on every route (test or scripted check)
- `/validation-board` updated to show every primitive in all three themes
- Component specs for the new primitives and the shell (nav groups, logged-out filtering, drawer toggle, theme persistence, dialog focus behaviour)
- KVM visual review: deploy to VM 2, screenshot every route at 1440 and 390 px in light and dark, review them, fix what reads poorly, and record the screenshot set location and findings in `docs/DEVELOPMENT.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update `docs/slices/S248-dashboard-design-system.md` references or a short `docs/architecture/DASHBOARD_DESIGN.md` describing tokens, themes, primitives and the voice and copy rules.
- `docs/THIRD_PARTY.md`: the bundled font licence.
- `docs/DEFERRED.md`: close the theme-switching entry; record discovered out-of-scope work.
- `docs/PARITY.md`: confirm no row changes are needed (no routes or commands change).
- `docs/DEVELOPMENT.md`: S282 section with the visual review summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

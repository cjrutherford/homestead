# Dashboard design (S248, S282)

Clean-room (ADR-019): plain Angular and CSS custom properties, no UI
framework, no vendored third-party UI code.

## Tokens
`dashboard/src/tokens.css` is the only place values live; components use
`var(--...)`. Groups: spacing, type scale, radius, elevation, motion, layers,
touch target (`--touch-target`, 44px) and colour. Breakpoints are literals
documented in that file (md, 48rem, is where the sidebar becomes a column).

Radius and elevation follow hierarchy: controls `--radius-control`, panels
`--radius-panel` with `--shadow-sm`, dialogs `--radius-dialog` with
`--shadow-lg`, badges are pills. Motion tokens (`--duration-*`, `--ease-*`)
exist for S283; today only control state changes transition, within
`--duration-fast`, and `prefers-reduced-motion` shortens all of it.

## Themes
Light (default): Limewash `#F2F4F0` page, Board `#FFFFFF` panels, Pine
`#1F4D3A` brand and primary, Ink `#1C2621` text, Signal amber `#B4690E`
(fills, glyphs, borders; text uses the darker `--warn-text` `#8A4F06`
because `#B4690E` is 4.2:1 on white), Brick `#9E2F1E` danger, Meadow
`#2D7A1F` for ok. Dark uses deep pine-charcoal surfaces (`#131A16` page,
`#1B2420` board) with a lighter pine primary `#7FCBA3`. High contrast is
black, white and yellow.

Selection: no `data-theme` follows `prefers-color-scheme`; the Account page
stores system, light, dark or high contrast in `localStorage`
(`homestead.theme`) and `ThemeService` sets `data-theme` on `<html>`.
`data-theme` also works on any element (the validation board nests all
three). The dark block appears twice (system and manual); a spec keeps them
identical. `theme/tokens-contrast.spec.ts` asserts WCAG AA (4.5:1 text, 3:1
borders, focus rings and glyph colours) for every token pair in every theme.
Add a token pair there when you add a token.

## Type
Atkinson Hyperlegible Next for all text; Atkinson Hyperlegible Mono
(`--font-mono`, `.mono`, `code`, `pre`) only for hostnames, fingerprints,
commands and IDs. Regular and Bold only. Files live in `public/fonts/`
(licence in `docs/THIRD_PARTY.md`); `font-display: swap`, no external
requests. Sentence case everywhere; no all-caps labels.

## Primitives (`styles` in `src/*.css`)
- Buttons: a plain `<button>` is secondary; `button[type=submit]` and
  `.primary` are primary; `.danger`; `.quiet`; `a.button` for links.
  States: hover, active, `:focus-visible`, `:disabled`, `aria-busy="true"`.
- Fields: `input`, `select`, `textarea`, checkbox and radio inside `label`;
  `aria-invalid="true"` plus a `.field-error` message with a glyph. A form
  in `.shell-main` renders as a board.
- Tables: wrap in `.table-scroll` (overflow wrapper, sticky header).
- Badges: `.badge` with `.badge-ok`, `-warn`, `-fail`, `-pending`,
  `-running`; plain `.badge` is neutral. The glyph is a CSS mask, so colour
  never stands alone; always include the word.
- Messages: `app-state-message` (empty, loading, error, status) and
  `.warning` callouts.
- Dialog: `app-dialog` (`heading`, `variant="confirm|danger"`, `(dismissed)`)
  on a native `<dialog>` opened with `showModal()`, `role="alertdialog"`,
  `aria-modal`, Tab trap, Escape, focus returns to the opener. Put actions in
  an element with class `dialog-actions`. The page owns the pending state
  and renders the component while it is pending.
- Focus: one `:focus-visible` ring per theme; the sidebar has its own colour.

## Shell
Skip link, grouped sidebar (`core/nav.ts`: Overview, Apps, Network, Storage
and backups, System), collapsible groups persisted in `localStorage`
(`homestead.nav.collapsed`), current page with `aria-current="page"`, a
drawer below md with a menu button, focus management and Escape. Logged-out
visitors see Status, Log in and, while unclaimed, Claim. Log in, Log out and
the user name live in the sidebar foot. The slot `data-slot="work-orders"`
is reserved for S283. Each page has one `<h1>`; the brand is not a heading.

## Voice and copy
- Plain verbs, sentence case, the owner's vocabulary (not internal names).
- An error says what happened and how to fix it ("Can't reach the
  appliance. Check that homestead-server is running.").
- Empty states invite the next action.
- An action keeps one name through the whole flow: the button, the
  confirmation heading and the confirm button use the same verb (Deploy
  Caddy, "Deploy Caddy?", Confirm deploy).
- No middle-dot meta strings, no arrows appended to buttons.

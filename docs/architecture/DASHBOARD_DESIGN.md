# Dashboard design (S248, S282, S283, S285)

Clean-room (ADR-019): plain Angular and CSS custom properties, no UI
framework, no vendored third-party UI code.

## Tokens
`dashboard/src/tokens.css` is the only place values live; components use
`var(--...)`. Groups: spacing, type scale, radius, elevation, motion, layers,
touch target (`--touch-target`, 44px; sidebar rows drop to 40px on fine pointers) and colour. Breakpoints are literals
documented in that file (md, 48rem, is where the sidebar becomes a column).

Radius and elevation follow hierarchy: controls `--radius-control`, panels
`--radius-panel` with `--shadow-sm`, dialogs `--radius-dialog` with
`--shadow-lg`, badges are pills. Motion tokens (`--duration-*`, `--ease-*`)
are used by S283 (see States, motion and feedback); `prefers-reduced-motion`
removes every animation and transition.

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
  `.primary` are primary; `.danger` (filled; the confirm button of a danger
  dialog); `.danger.quiet` (outlined; a destructive action that is not the point
  of the page, such as Uninstall on a list row or the done page); `.quiet`;
  `a.button` for links.
  States: hover, active, `:focus-visible`, `:disabled`, `aria-busy="true"`.
- Fields: `input`, `select`, `textarea`, checkbox and radio inside `label`;
  `aria-invalid="true"` plus a `.field-error` message with a glyph. A form
  in `.shell-main` renders as a board.
- Tables: wrap in `.table-scroll` (overflow wrapper, sticky header).
- Badges: `.badge` with `.badge-ok`, `-warn`, `-fail`, `-pending`,
  `-running`; plain `.badge` is neutral. The glyph is a CSS mask, so colour
  never stands alone; always include the word.
- Messages: `app-state-message` (empty, error, status, warning, notice) and
  `.warning` callouts. Text stays ink; the kind shows in the start bar and
  glyph. Use `warning` for cautions, `status` only for success.
- Dialog: `app-dialog` (`heading`, `variant="confirm|danger"`, `(dismissed)`)
  on a native `<dialog>` opened with `showModal()`, `role="alertdialog"`,
  `aria-modal`, Tab trap, Escape, focus returns to the opener. Put actions in
  an element with class `dialog-actions`. The page owns the pending state
  and renders the component while it is pending.
- Focus: one `:focus-visible` ring per theme; the sidebar has its own colour.
  Below md the sticky top bar would cover what takes focus, so `html` carries
  `scroll-padding-top`.

## Shell
Skip link, grouped sidebar (`core/nav.ts`: Overview, Apps, Network, Storage
and backups, System), collapsible groups: only the group holding the current
page is open by default, and the owner's explicit open or close choices are
persisted in `localStorage` (`homestead.nav.groups`); current page with `aria-current="page"`, a
drawer below md with a menu button, focus management and Escape. Logged-out
visitors see Status, Log in and, while unclaimed, Claim. Log in, Log out and
the user name live in the sidebar foot. The slot `data-slot="work-orders"`
holds the work-orders rail (S283); below md the rail is a bottom sheet. Each page has one `<h1>`; the brand is not a heading.

## Voice and copy
- Plain verbs, sentence case, the owner's vocabulary (not internal names).
- An error says what happened and how to fix it ("Can't reach the
  appliance. Check that homestead-server is running.").
- Empty states invite the next action.
- An action keeps one name through the whole flow: the button, the
  confirmation heading and the confirm button use the same verb (Deploy
  Caddy, "Deploy Caddy?", Confirm deploy).
- No middle-dot meta strings, no arrows appended to buttons.

## First paint
`public/theme-init.js` (same-origin, loaded synchronously in `<head>`) applies
a stored theme before the first paint. The production build sets
`inlineCritical: false` so the full stylesheet, including the `data-theme`
blocks, is render-blocking; inlined critical CSS omitted those blocks and
caused a light flash on reload.

## States, motion and feedback (S283)
The one bold element is the work-orders rail. Everything else stays quiet.

### Loading
- Wrap content that waits for data in `<app-loading [loading]="..."
  shape="lines|list|fields|panel">`. The skeleton appears only after 150 ms
  and, once shown, stays at least 300 ms, so a fast response never flashes
  and a slow one never flickers. The content stays hidden until both are
  done. The wrapper reserves the skeleton's height from the start, so
  nothing jumps. Choose the shape that matches the content.
- Skeletons are static: no shimmer, no pulse. Never write "Loading…" or a
  bare `…`; assistive technology hears "Loading" once the skeleton shows.
- A button that is busy keeps its label and sets `aria-busy="true"`.

### States
- One component, `app-state-message`: `empty`, `error`, `status` (success),
  `warning`, `notice` (neutral). `headline`, optional `detail`, and one next
  action (`actionLabel` plus `actionLink` or the `(action)` output).
- Empty states invite the action ("No apps installed yet" with a Browse the
  catalog action). Errors say what happened and how to fix it; use
  `httpErrorMessage`.
- A read that answers 404 because something is not set up yet (no TLS
  strategy, no reviewed exposure plan) is a state, not an error: use
  `isNotSetUp(err)` and show an empty state with the next step. Angular
  writes nothing to the console for a handled `HttpClient` error. The browser
  still lists the failed request in its network log; avoiding that needs the
  API to answer 200 (see DEFERRED.md).

### Motion
- Motion answers a user action, except the rail's progress. No fade-and-slide
  on sections, no hover animation on cards.
- Route changes use View Transitions (`withViewTransitions`): only
  `<main>` crossfades (120 ms out, 200 ms in); the sidebar stays still. The
  first load never animates. Unsupported browsers just swap the page.
- Dialogs fade and rise in (200 ms) and out (120 ms). The page removes the
  component at once, so `app-dialog` leaves an inert copy and scrim behind
  for the exit.
- Toasts rise in (200 ms). The rail's bar widens over 320 ms; a running
  order's glyph pulses slowly.
- `prefers-reduced-motion: reduce` removes all of it: view transitions are
  skipped in script (`onViewTransitionCreated`), dialogs close at once,
  keyframes and transitions are off.

### Feedback
- A completed action shows a toast (`ToastService.show`), six seconds, errors
  twelve, always dismissible, never the only record: the result also lives in
  Operations or on the page. The action keeps one name through the flow:
  "Reconcile routes" (button), "Reconcile routes?" (dialog), the rail title,
  then "Routes reconciled" (toast). The names live in
  `core/operation-labels.ts`.
- `ToastRegion` (mounted once in the shell) holds a polite `aria-live`
  region. Step changes and results are announced there
  (`ToastService.announce`), not on every element.

### Work orders
- Every operation the dashboard starts joins the rail: a successful POST or
  DELETE to an operation-starting route is recognised by
  `workOrderInterceptor` (pages need no code). Operations found running on
  load, and on navigation (at most every ten seconds), join too, read from
  the existing operations list, so the rail survives a reload and shows
  operations started from the CLI.
- Each order shows title, current step, step count, elapsed time and result,
  and links to the operation detail. Finished orders leave after 8 seconds
  (20 on failure); the record stays in Operations. The rail lists three and
  links to Operations for the rest.
- Progress is the same everywhere: `app-step-list` (rail detail page and
  in-page) and `app-operation-progress` for a page that started an
  operation. No "Operation N: running" text.

## Wizard pattern (S285)
The catalog install wizard (`catalog/install-wizard.ts`, route
`/catalog/:name/install`, loaded on demand) is the model for any multi-step
flow.

### Steps
One sequence for every package; a step that does not apply is left out, never
shown empty. `catalog/wizard-model.ts` (`stepsFor`) decides:

| Step | Shown when | Content |
| --- | --- | --- |
| What it is | always | blurb, description, curated note, browser advisories |
| Requirements | always | what the package needs; Docker, edge network, Caddy and (craft) build state; the daemon's plan check |
| Storage | the manifest declares storage or persistent data | one mount per storage class from registered mounts, free space, owner-visible paths, folders the owner already has |
| Address | always | domain, hostname per endpoint (the address it will be given, local only, internal TLS), who can reach it, advanced (container port, extra networks, zone and public address) |
| Secrets and sign-in | the manifest declares secrets or sso is not `unsupported` | generated secrets by name and purpose (never values); how people sign in |
| Review | always | the plan in plain words (create, change, leave alone), data placement, advisories, notes, raw plan |
| Install | always | the install operation, or for a craft app Verify and build first |
| Done | always | the working address as a link (and a trust note when this device cannot open it), first-login hints, where data lives, Uninstall |

Package shapes: a stateless craft app is About, Requirements, Address, Review,
Install, Done. Storage adds Storage. `sso: optional` or declared secrets add
Secrets and sign-in. Authentik has both and no special case beyond its curated
sign-in paragraph.

### Rules
- The step list is an ordered list with `aria-current="step"`; each item has a
  visually hidden "Step N of M". On a step change focus moves to the step
  heading (`tabindex="-1"`) and the polite live region announces it.
- Each step has one sentence of helper text, from manifest fields where they
  exist and from `catalog/curated-copy.ts` where they do not (first-login
  hints, per-app notes). A spec fails when a package in `catalog/` has no
  curated entry.
- Hard problems (Docker down, a refused plan) keep the owner on the step with
  what happened and how to fix it. Soft ones (Caddy not running) offer "Set up
  the edge" (a confirm dialog, then the existing Caddy deploy operation, shown in
  the rail) or let the owner acknowledge and carry on. Field errors are tied to their field
  (`aria-invalid`, `aria-describedby`) once Continue was pressed.
- Back and Next keep every choice (`InstallDrafts`, in memory). A reload starts
  clean, and the plan is asked for again every time Requirements or Review is
  entered, so the plan reviewed is the plan applied.
- The action keeps its name: "Install GridCraft" (title and button), "Install
  GridCraft" in the rail, "GridCraft installed" (heading, toast). Craft:
  "Verify and build GridCraft" first. Package names in the rail come from the
  catalog titles (`registerPackageTitles`).
- Leaving while an operation runs does not stop it: returning to the wizard
  picks the same operation back up. After a failure nothing is left installed
  (the daemon rolls back) and Try again goes through a fresh Review.
- An installed app gets its address from the wizard, through the existing edge
  routes API only: after the install succeeds, Done reads the route table,
  merges the reviewed plan's routes into it (a route on the same host is
  replaced, every other route is sent back unchanged) and reconciles. Public
  routes are never added (ADR-006). With no running proxy nothing is added: Done
  says plainly the app is not reachable until the edge is set up and offers the
  same dialog, then adds the address. Done then asks this device to open the
  address; if it cannot (name unresolved, or the Homestead root not trusted),
  it links to Trust. Uninstall takes the app's routes out of the table the same
  way (`catalog/app-routes.ts`).
- Uninstall (`catalog/uninstall-app.ts`) is a danger dialog: what is removed,
  what is kept, an "Also delete its data" box that lists every root in full,
  and an acknowledgement for data still in Docker volumes only after the
  daemon refuses.

### Copy
Curated hints come from the manifests, this folder and `docs/THIRD_PARTY.md`.
Where a fact is upstream's own first-run behaviour (the first Immich account is
the administrator; Authentik's first-run, started from its own address), it is written once
and kept generic. Nothing promises a feature the package lacks.

## Setup walkthrough (S287)
`/setup` (lazy) is the post-claim walkthrough. It uses the shared primitives
(`app-loading`, `app-state-message`, the `step-list` glyph styles) and one h1.
It links to existing pages rather than embedding them. `setupGuard` is the only
gate; the exempt routes are listed in `setup/setup.guard.ts` and
`docs/architecture/SETUP_WALKTHROUGH.md`. The navigation entry reads "Setup", or
"Finish setup" on an appliance claimed before the walkthrough existed.

## Page patterns (S284)

Four patterns cover most pages. Each uses only the S282 tokens and primitives.

- **Triage list** (Status). Things that need action come first, worst first,
  as `.triage` items: a badge, what happened, a "What to do:" line and, when a
  command helps, an `app-copy-code` block. Healthy items collapse into a
  `<details>` with a count. Identity facts (service, claim, version) sit in a
  compact `.facts` list, not a card grid.
- **App list** (Catalog, Craft apps). One row per app: `.app-icon`, name, a
  one-line purpose, a state badge and the install or open action. Rows, not a
  uniform card grid. Craft advisories use the S252 component in `.advisory-list`.
- **Data table** (Operations, Audit, Storage, Backups, Edge, Trust, Craft
  files, Tunnel routes). A real `<table>` inside `.table-scroll`, with a
  `<caption>` or heading, status in `.badge`, numbers in `.num`, and sortable
  headers where order matters. Use `core/sort.ts` (`nextSort`, `ariaSort`,
  `sortRows`) and the `th button.sort` style; set `aria-sort` on the header.
- **Risky action** (Deploy Caddy, Reconcile routes, Apply firewall, rotation,
  re-key, volume removal, tunnel teardown). A `.risky` block with a "What
  changes" preview next to the button, a `danger` or `danger quiet` button, and
  an `app-dialog` (danger variant) that states the consequence before the
  action runs. The dialog only gates the existing call; it adds no API.

Forms use the `.field` layout with an inline `.field-error` message tied to the
input by `aria-describedby`. Errors say what happened and how to fix it.

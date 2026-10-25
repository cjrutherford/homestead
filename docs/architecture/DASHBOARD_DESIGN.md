# Dashboard design (S248, S282, S283, S285, S309, S310)

Clean-room (ADR-019): plain Angular and CSS custom properties, no UI
framework, no vendored third-party UI code.

## The language: lamplight (S310)
Five rules. Every component follows them; components use tokens only and
never write a raw shadow or colour.

1. **One light source.** Light falls from the top of the page, at the
   masthead. A nearer surface is lit more: a lighter fill, a 1px top
   highlight (`--highlight-top`) and a longer, softer shadow below it. Every
   shadow falls downward.
2. **Four depths, plus the well.**
   - **Hedgerow**: the sidebar, the deepest plane, darker than the ground.
     The canvas casts `--edge-shadow` onto it. The current page is a lit
     tab: `--sidebar-active-bg` fill, control elevation and a
     `--sidebar-lamp` bar on its inline-start edge.
   - **Ground**: the canvas (`--background`).
   - **Plate**: fields, panels, cards, forms and catalog rows
     (`--elevation-plate`).
   - **Lifted**: the permit, menus, dialogs, the drawer, toasts and the
     Needs you field (`--elevation-lifted`).
   - **Well**: inputs, code, ledgers (`dl`), table bodies and logs, cut into
     their plate (`--surface-sunken`, `--elevation-well`).
   Detail reads as recessed and actions as raised: buttons are small plates
   (`--elevation-control`) that press down on `:active`
   (`--elevation-pressed` and a `--press-offset` translate). Quiet and
   disabled buttons stay flat.
3. **Light means attention.** Only what needs the owner glows. Needs you is
   a lit window (`--glow-attention`, `--glow-furrow`). Warn and fail badges
   are lamps with a halo of their colour (`--halo-warn`, `--halo-fail`); ok
   has no halo, and whole fields never glow for ok. Focus is a lit ring: the
   outline plus `--glow-focus`. Light never animates.
4. **The land.** The masthead sits on a band (`--band-height`) of faint
   contour lines: the `--contour` SVG data URI coloured with `--contour-ink`
   and masked by `--lamp-falloff`, brightest top-left, behind a soft
   `--lamp-wash`. This is the only texture. The masthead rule is the
   **horizon** (`--horizon`), lit at its inline-start end and fading out.
   The h1 is `--font-size-display` (3.25rem) and the sentence under it a lead
   at `--font-size-lead` in `--muted-strong`.
5. **The permit is paper under the lamp.** It is the highest surface: its
   perforation is a mask, so its shadow is `filter: var(--elevation-paper)`
   on the `app-permit` host. The Manila fill carries `--paper-falloff`
   (lighter top-left, `--manila-lit`). The seal is embossed (`--emboss`).
   In dark the permit is warm and lit, never white. Its warning spans its
   full width.

**Every page (S311).** The language is not per page. Every routed page
opens with the masthead, and every section below it is a field
(`section.field-panel`, one h2 first): a plate, with a furrow when the
section has a state and the lit Needs you treatment when it needs the
owner. Only the masthead, fields, permits, dialogs, operation progress,
page-level messages and a line of prose may sit on the ground. A field
inside a field (one package on Updates, a page embedded in a Setup step) is
a sub-plate: a raised step lit along its top edge, with no second shadow,
and a sub-field that needs the owner keeps the lit Needs you window (Drift).
Forms, risky blocks and cards inside a field are groups on it, not second
plates; a list of permits is a stack of papers with no bullets. Empty states are wells with their action beside them. Claim
and login are the **gate**: one centred, lifted plate (`.gate`,
`--gate-width`) on the contour ground. `theme/lamplight.spec.ts` walks every
routed template and fails on a missing masthead, a section on the ground, an
h2 that does not open its own field, an empty state outside a field, or a
raw colour or shadow in any page stylesheet or component style; a new route
must be added to its page list.

High contrast keeps depth in borders only: every elevation, glow, halo,
wash and falloff token is `none`, contours are transparent, wells get a 1px
inner border and the horizon is solid. `forced-colors: active` drops them
too. The validation board shows the four depths and the lamps.

## Hierarchy (S309)
The owner sees first what needs them, then what the appliance is doing,
then the detail. The vernacular is the homestead's own: land in fields
worked in rows, signage on a feed store, and the paper permit that says a
change was reviewed and approved. Three tiers, each treated differently, and
one bold element.

- **Tier 0, the shell recedes.** The sidebar is a deep pine band,
  **Hedgerow** (`--hedgerow`, `--on-hedgerow`). The wordmark is in the
  display face. The current page is a lit tab (S310, replacing the S309 notch):
  a raised fill with a 4px lamp bar on its inline-start edge, so the state
  never rests on colour alone. High contrast keeps its yellow
  current page and drops the furrow.
- **Tier 1, the masthead.** Every page opens with
  `<header class="masthead"><h1>…</h1><p class="masthead-fact">…</p></header>`:
  the `h1` in the display face (up to `--font-size-display`, 2.75rem) and one
  plain sentence. Where the page already has live data the sentence states
  its most important fact, computed from data the page already loads
  (Status: "Homestead is running. Nothing needs you."; Setup: "2 of 6 steps
  are done. Storage is next."; Catalog: the installed count; Tunnel: the
  tunnel, its routes and its connector). Other pages keep a one-line
  purpose. A sentence, never a stat card. The masthead closes with the only
  heavy rule on the page (`--rule-heavy`, `--masthead-rule`).
- **Tier 2, fields.** A section with a state is a **field**:
  `.field-panel` is a Board panel; `data-state="warn|fail|ok"` adds a
  `--furrow` (4px) on its inline-start edge in that state's colour. A neutral
  field has no furrow. The furrow encodes state only, never decoration, and
  the heading or a badge always says the state in words. Status and Setup
  use fields; other pages keep hairline `h2` sections (see DEFERRED.md).
- **Needs you.** On Status and Setup, when anything needs the owner, a
  `.needs-you` field comes first: "Needs you (N)", the only place amber
  fills a background (`--warn-surface`). Each row is one sentence and one
  action (a button, a link or a command to copy). Status builds it from the
  unclaimed state and the failing or warning checks; Setup from the steps
  still to do before the one shown. When nothing needs the owner it is not
  rendered and the masthead says so.
- **Tier 3, detail.** Tables, definition lists and logs drop to
  `--font-size-sm` with muted headers. A `dl` is a **ledger**: on wide
  screens `dl > div > dt` labels are muted, right-aligned against a hairline,
  values beside them; below 40rem they stack. Mono stays for hostnames,
  fingerprints, commands and IDs.

### The permit: the one bold element
Every plan the owner reviews and approves renders as a **permit**
(`ui/permit.ts`, `<app-permit>`, styles in `src/permit.css`): a document on
**Manila** paper (`--manila`, `--on-manila`, `--manila-rule`) with a
perforated top edge (a CSS mask of radial-gradient holes), the plan title in
the display face, a "Plan for your approval" overline, and the page's own
review content projected into the body. "Will change", "Leaves alone" and
"Refused" are ruled `h4` sections (`.permit-section`). The page's buttons
stay its own; `.permit-actions` (or `.risky-actions`) puts them on a dashed
signature line. Inputs: `heading`, `digest`, `level` (2 or 3), `label` (an
accessible name when the page already named its review region), `approval`
and `refusal`.

- **Seal.** When the plan has a digest it sits in the corner as a circular
  mono seal with the first eight characters (`sealCode`, without an
  algorithm prefix); its accessible name is "Plan digest" and the full
  digest. Below 30rem the seal becomes a pill under the title.
- **Approved stamp.** After approval a `--stamp` "Approved" stamp (display
  face, double rule border, rotated `--stamp-angle`, -4deg) presses on: it
  scales from 1.25 to 1 and fades in over `--duration-slow`. This is the
  only motion not started by the owner's hand in the moment, it runs once,
  and with `prefers-reduced-motion` it simply appears. The stamp says who
  approved and when. A page keeps the approved permit as a compact record
  (`approvedPlan(heading, digest, summary, by)`) beside the operation it
  started, so the stamp is seen; a plan the appliance reports approved
  without saying by whom still gets the stamp, without the meta line.
- **Not approved.** When the appliance refuses an approval and the plan
  stays on screen, the permit gets a `--stamp-refused` "Not approved" stamp,
  never rotated, with the appliance's reason beside it. A stale plan (409)
  is cleared as before and the page error says why.
- **Where.** Tunnel, sign-in with Authentik, exposure, firewall, catalog
  install review, app public name, app sign-in, nightly backup policy, names
  resolver, uninstall (inside its danger dialog), platform updates, TLS
  issuance plans, root rotation, Nextcloud link and data migration. Plans
  without a digest (firewall, exposure, install, uninstall, Nextcloud,
  platform updates) have no seal.
- **High contrast.** The permit becomes a white document with a black
  border; tokens are re-scoped under `[data-theme='high-contrast'] .permit`
  so its contents are legible on white, and the stamp is black. Forced
  colours keep the borders and drop the mask.

The validation board shows a pending, an approved and a refused permit.

## Tokens
`dashboard/src/tokens.css` is the only place values live; components use
`var(--...)`. Groups: spacing, type scale, radius, elevation, motion, layers,
touch target (`--touch-target`, 44px; sidebar rows drop to 40px on fine pointers),
the S309 scalars (`--font-display`, `--font-size-display`,
`--line-height-display`, `--tracking-display`, `--measure` 72ch,
`--furrow`, `--rule-heavy`, `--stamp-angle`, `--seal-size`) and colour. Breakpoints are literals
documented in that file (md, 48rem, is where the sidebar becomes a column).

Radius and elevation follow hierarchy: controls `--radius-control`, panels
`--radius-panel` with `--elevation-plate`, dialogs `--radius-dialog` with
`--elevation-lifted` (S310 replaced `--shadow-*`), badges are pills. Motion tokens (`--duration-*`, `--ease-*`)
are used by S283 (see States, motion and feedback); `prefers-reduced-motion`
removes every animation and transition.

## Themes
Dark is the default (S310). Dark: Hedgerow `#0A100D`, ground `#111915`,
plate `#18221D`, lifted `#1F2A24`, well `#0D1310`, ink `#E6ECE7`, muted
`#9DAAA2`, Pine accent `#7FD1A8`, Lantern `#F2B544` (warn and focus), Meadow
`#6FCF7F`, Brick `#F07A62`, Manila-night `#2A2619` to `#332D1E`. Dark
shadows are near-black at real alpha; depth comes from highlights and value
steps. Light ("daylight"): Limewash `#F2F4F0` page, Board `#FFFFFF` panels,
Pine `#1F4D3A` primary, Ink `#1C2621` text, Signal amber `#B4690E` (text
uses `--warn-text` `#8A4F06`), Brick `#9E2F1E`, Meadow `#2D7A1F`, with
softer cool shadows and fainter contours. High contrast is black, white and
yellow.

S309 adds, in every theme: `--hedgerow` and `--on-hedgerow` (the sidebar
band; `--sidebar-*` follow it), `--manila`, `--manila-rule`, `--on-manila`
and `--on-manila-muted` (the permit), `--stamp` and `--stamp-refused`,
`--warn-surface` (Needs you), `--masthead-rule`, and quieter `--rail-*`
values. Light Hedgerow is `#12291F`, Manila `#F5EEDB`; dark Hedgerow is a
step darker than the page (`#0C120F`) and Manila a warm deep `#2A2619`.

Selection: no stored choice means dark, with no `data-theme`. The Account
picker offers Dark, Light, Match my system and High contrast. Light, system
and high contrast are stored in `localStorage` (`homestead.theme`) and set as
`data-theme` on `<html>`; choosing Dark clears the key. Only
`[data-theme='system']` follows `prefers-color-scheme`.
`data-theme` also works on any element (the validation board nests all
three). Dark is the `:root` block (shared with `dark` and `system`); the
light block appears twice (manual, and system under
`prefers-color-scheme: light`), and a spec keeps them identical. The spec
also checks the depth luminance order and that high contrast has no
shadows or glows. `theme/tokens-contrast.spec.ts` asserts WCAG AA (4.5:1 text, 3:1
borders, focus rings and glyph colours) for every token pair in every theme.
Add a token pair there when you add a token.

## Type
Display: Barlow Semi Condensed SemiBold (600, `--font-display`, SIL OFL,
self-hosted woff2) for `h1`, `h2`, the wordmark, the permit title and the
stamp only: sentence case, `--tracking-display` (-0.01em), never all caps.
Barlow comes from highway and rail signage, which suits the signage voice.
Text: Atkinson Hyperlegible Next for all other text; Atkinson Hyperlegible Mono
(`--font-mono`, `.mono`, `code`, `pre`) only for hostnames, fingerprints,
commands and IDs. Regular and Bold only for text. Prose blocks stay within `--measure`. Files live in `public/fonts/`
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
Hedgerow sidebar with the current-page lit tab (see the language), skip link, grouped sidebar (`core/nav.ts`: Overview, Apps, Network, Storage
and backups, System), collapsible groups: only the group holding the current
page is open by default, and the owner's explicit open or close choices are
persisted in `localStorage` (`homestead.nav.groups`); current page with `aria-current="page"`, a
drawer below md with a menu button, focus management and Escape. Logged-out
visitors see Status, Log in and, while unclaimed, Claim. Log in, Log out and
the user name live in the sidebar foot. The slot `data-slot="work-orders"`
holds the work-orders rail (S283), now quieter (a hairline border, a smaller heading, the band's own tones); below md the rail is a bottom sheet. Each page has one `<h1>`; the brand is not a heading.

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
Since S309 the one bold element is the permit (see Hierarchy). The
work-orders rail is Tier 2: present, legible and quiet. Everything else stays
quiet.

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
- Motion answers a user action, except the rail's progress and the permit's
  single stamp press. No fade-and-slide
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
  in the Needs you field: a badge, what happened and one action (a link, a
  command in `app-copy-code`, or Check again). Healthy checks sit in their own
  ok field, collapsed into a `<details>` with a count. Identity facts (service, claim, version) sit in a
  ledger (a definition list in a well), not a card grid.
- **App list** (Catalog, Craft apps). One row per app: `.app-icon`, name, a
  one-line purpose, a state badge and the install or open action. Rows, not a
  uniform card grid. Craft advisories use the S252 component in `.advisory-list`.
- **Data table** (Operations, Audit, Storage, Backups, Edge, Trust, Craft
  files, Tunnel routes). A real `<table>` inside `.table-scroll`, with a
  `<caption>` or heading, status in `.badge`, numbers in `.num`, and sortable
  headers where order matters. Use `core/sort.ts` (`nextSort`, `ariaSort`,
  `sortRows`) and the `th button.sort` style; set `aria-sort` on the header.
- **Plan review** (every plan the owner approves). An `app-permit`, as
  described under Hierarchy.
- **Risky action** (Deploy Caddy, Reconcile routes, re-key, volume removal,
  tunnel teardown). A `.risky` block with a "What
  changes" preview next to the button, a `danger` or `danger quiet` button, and
  an `app-dialog` (danger variant) that states the consequence before the
  action runs. The dialog only gates the existing call; it adds no API.

Forms use the `.field` layout with an inline `.field-error` message tied to the
input by `aria-describedby`. Errors say what happened and how to fix it.

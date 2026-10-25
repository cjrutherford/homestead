# S311 — Lamplight on every page

## Goal
S310 applied the lamplight depths (plates, wells, lifted, lamps) to Status,
Setup, the permit and Catalog only. Every other page still lays its
sections flat on the ground. The owner rejected that as incomplete. After
S311, **every route in the dashboard** follows the five rules in
`docs/architecture/DASHBOARD_DESIGN.md`, with no exceptions and no
deferrals.

## Every route
`status`, `setup`, `claim`, `login`, `sign-in`, `account`, `config`,
`firewall`, `catalog`, `catalog/:name/install`, `updates`, `data`,
`advanced`, `drift`, `exposure`, `tunnel`, `names`, `backups`, `storage`,
`edge`, `tls`, `trust`, `craft-apps`, `craft-files`, `audit`, `secrets`,
`operations`, `operations/:id`, `validation-board`, and the not-found
page.

## What "applied" means
On every page:
- **Masthead.** The page opens with the masthead: h1, the lead sentence,
  and the horizon over the contour band.
- **Sections are plates.** Every top-level section below the masthead is a
  plate (`--elevation-plate`, `--radius-panel`, top highlight). No section
  sits flat on the ground, and the hairline dividers between sections are
  removed.
  - A section with a state carries the furrow: amber for needs review,
    Brick for failure, Meadow for ok.
  - A section that needs the owner is lifted and lit, as Needs you is on
    Status.
- **Detail sits in wells.** Tables, definition lists, logs, code, inputs
  and empty states ("No tunnels yet") are wells cut into their plate.
  Empty states are wells with the action beside them, not a flat bordered
  box.
- **Status is lamps.** Status badges, dots and glyphs in tables and lists
  are lamps: warn and fail have halos, ok is a steady lamp with no halo.
  This closes the S310 DEFERRED item.
- **Plan reviews are permits.** This includes Nextcloud, updates and
  uninstall.
- **Forms and dialogs.**
  - Forms are plates.
  - Fieldsets inside them are grouped as sub-plates or wells, not nested
    borders.
  - Dialogs and the drawer are lifted.
- **Claim, login and sign-in** use a centred lifted plate on the contour
  ground. They are the first thing a new owner sees, so they must carry
  the language too.

Prefer one shared mechanism over per-page CSS: a page-level rule or a
small `field` / `well` class set used by every page template, so new
pages get the language by default. Remove the per-page flat section
styles the shared rule replaces.

## Enforcement
Add a spec that walks every routed page component's template and fails
when:
- the masthead is missing;
- a top-level `<section>` (or the page's section element) is not a
  field/plate; or
- a raw `box-shadow` or colour literal appears in page CSS.
Keep an explicit allowlist only for genuine exceptions, each with a
written reason. The aim is that the S310 gap cannot recur.

## Out of scope
New data, routes, API changes, or reworking page content. Layout changes
are limited to wrapping sections in plates and wells.

## Acceptance Criteria
- `pnpm exec ng test` passes, including the new enforcement spec;
  `make dashboard-build dashboard-embed` succeeds; the Python tests pass.
- There is a dark desktop screenshot of **every route** in the list,
  including populated states where the mock can provide them. Each one is
  reviewed by looking at it, and any flat section is fixed before commit.
- There are light and high-contrast desktop screenshots and dark 375px
  screenshots of at least Tunnel, Names, Backups, Operations, Audit, Claim
  and Login.
- `docs/DEFERRED.md`: the "Fields only on Status and Setup" and "state dots
  outside badges" items are closed by S311.

# S394 — Catalog bundles render their apps and survive empty lists

## Goal
Fix the live `/admin/catalog` Bundles section, where a bundle rendered blank
bullets and threw `TypeError: Cannot read properties of null (reading 'length')`.

## Dependencies
S344 (catalog bundles).

## Root cause
`catalog.CheckBundle` left `Members`, `Missing` and `Storage` nil until
appended to. Go serialises a nil slice as JSON `null`, so a complete bundle
returned `Missing: null` and the template's `bundle.Missing.length` threw
mid-render, leaving empty `<li>` bullets. The template also never listed the
bundle's member packages.

## Scope
- `CheckBundle` always initialises the three lists, so the API emits `[]`.
- The bundles section lists each member, flagging ones not yet in the catalog,
  and reads `Members`, `Storage` and `Missing` defensively (`?? []`).
- No route, CLI or dashboard-surface changes (PARITY unaffected).

## Acceptance Criteria
- The bundles API JSON carries arrays, never `null`, for the three lists.
- The Starter bundle shows its package names.
- A report with `null` lists renders without error.

## Verification
Go tests in `internal/catalog` and `internal/server`; dashboard spec in
`catalog.spec.ts`.

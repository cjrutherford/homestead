# S283 — Dashboard states, transitions and work orders

## Goal
Make every change of state legible: loading, empty, error and success states that don't flicker or jump, page transitions, confirmation feedback, and a persistent "work orders" rail that follows running operations across pages.

## Dependencies
S282

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Builds on the S282 tokens and primitives. Uses the existing operations API and its live update stream (operation detail already streams); no new daemon routes are expected. If the rail needs an API addition (for example a filter for running operations), make it in the shared place with a test and a `docs/PARITY.md` row, with a CLI equivalent per ADR-004.
Owner-approved direction (2026-10-18): the work-orders rail is the one bold element; all other motion is quiet and answers a user action. Non-user-triggered motion is limited to the rail's progress.

## Acceptance Criteria
- Dashboard states and transitions implemented and documented
- Loading: content-shaped skeletons replace "Loading…" text and bare `…`; a skeleton appears only after ~150 ms and, once shown, stays at least ~300 ms, so fast responses don't flash; layout does not jump when data arrives
- StateMessage revised: one component for empty, error and notice, with a clear next action (empty states invite the action; errors say what happened and how to fix it, using the S282-era `http-error` helper); every page uses it; inconsistent loading copy unified
- Expected empty states no longer produce red console errors (pages that probe 404/422 endpoints handle them as "not set up yet")
- Page transitions: route changes use the View Transitions API through the Angular router with a short crossfade; disabled under `prefers-reduced-motion` and in browsers without support
- Feedback: a toast/announcement region for completed actions; an action keeps one name through the flow (button "Reconcile routes" → "Routes reconciled"); polite `aria-live` announcements for progress and results
- Work orders rail: every operation started from the dashboard, plus any running operation on load, appears in a rail at the foot of the sidebar (a bottom sheet on mobile) with title, current step, step count, elapsed time and result; it persists across navigation, survives reload by re-reading running operations, links to the operation detail, and settles finished orders after a short time with the outcome kept in Operations; at most a handful shown with a "more" link
- Operation detail and in-page progress (Edge, TLS, Catalog install, Updates, Backups) use the same step-list component as the rail, with no "Operation N: running / Working…" text left
- Dialogs: confirm flows use the S282 dialog primitive with enter and exit transitions that respect reduced motion
- Component specs: skeleton timing with fake timers, reduced-motion behaviour, rail add, update, finish and reload from running operations, toast and aria-live output, view-transition opt-out
- KVM visual and behavioural review on VM 2: start a real long operation (for example a catalog install of a small app, then uninstall it), navigate away and back while it runs, confirm the rail tracks it to completion; record a short screen recording or a screenshot sequence and the findings in `docs/DEVELOPMENT.md`; leave VM 2 as found
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Extend the dashboard design doc with state, motion and feedback rules.
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/PARITY.md`: confirm no row changes, or add rows for any API addition.
- `docs/DEVELOPMENT.md`: S283 section with the KVM review summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

# S321 — The Manage panel opens inside its app's card

## Goal
From the owner's VM 2 acceptance run (2026-10-27): pressing "Manage" on
an installed app opened its panel at the bottom of the catalog page, far
from the card that was pressed.

## Owner decision (2026-10-27)
- Expand inside the card (the "happy medium" over a modal).

## Scope
- The catalog page renders the Manage panel inside the selected app's
  card, below the card's own row, spanning the card's width. Its content
  and the API calls behind it do not change.
- The "Manage <app>" button toggles the panel and carries
  `aria-expanded` and `aria-controls`. One panel is open at a time.
- The lamplight heading rule (S311) accepts the panel's `h2` as opening
  a field.

## Rules
AGENTS.md slice rules apply. The dashboard surface keeps its name and its
calls, so PARITY is unchanged. No ADR.

## Acceptance Criteria
- Dashboard test: the panel opens inside the pressed app's card, only one
  panel is open, and pressing the button again closes it.
- All dashboard, Go and Python checks pass; the embedded dashboard is
  rebuilt.

## Live check
Deploy to VM 2 and confirm the bundle is served. Opening the panel needs
the owner's session, which the agent does not have, so the owner checks
it in a browser.

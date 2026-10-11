# Clean-room UI Reuse

## Status
Accepted

## Decision
Dashboard UX improvements inspired by the optimistic-tanuki design
system are reimplemented clean-room in the Homestead dashboard:
design tokens, spacing scales, responsive rules, and component patterns
may inform new Homestead code, but no Tanuki source files are vendored
or copied. This keeps Homestead's licensing unentangled while letting
both projects converge on the same user experience.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

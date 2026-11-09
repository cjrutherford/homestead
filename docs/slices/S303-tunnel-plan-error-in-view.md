# S303 — Tunnel plan refusal shown beside Review plan

## Goal
In the fresh-install walkthrough (setup Domain and access step), adopting
the kept `homestead-vm2` tunnel on a reset appliance was refused correctly
("no connector config at /etc/homestead/tunnel/config.yml"), but the message
appeared at the top of the tunnel section, about a screen above the Review
plan button. Clicking the button looked like it did nothing.

## Dependencies
S290 (adopt), S291 (embedded in setup), S301 (claim first; a hand-laid
connector config is the only way to adopt). No ADR: wording and placement only.

## Scope
- The tunnel page shows a refused plan request under Review plan
  (`reviewError`), not in the page-level error at the top.
- The adopt hint says a new or reset appliance has no connector config, so
  the owner names a new tunnel instead.

Out of scope: none discovered.

## Acceptance Criteria
- `tunnel.spec.ts`: a refused plan shows one alert, inside the setup form.
- Main build remains green; `docs/PARITY.md` unchanged (no route or surface
  change).

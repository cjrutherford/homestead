# Craft Apps Start Page

## Status
Accepted

Proposed 2026-10-17 after the owner dropped Craft Launcher from the catalog.
Accepted 2026-10-17 by the owner with option (b), the daemon-derived
dashboard page with CLI and API parity; options (a) and (c) are rejected. Related: ADR-018 (static browser apps), ADR-028 (craft packaging),
ADR-029 (craft file proxy), ADR-020 (operator session boundary) and
ADR-030 (local HTTPS).

## Decision
Provide a Homestead-owned "Craft apps" start page: a list of the installed
craft apps with their local HTTPS URLs, built by the daemon from the catalog
and the existing edge route state, with no external network calls. The owner
chose option (b) below.

## Context
Upstream Craft Launcher (dropped) has a compiled-in list of the upstream suite
with GitHub download buttons, cannot list installed apps, and calls
`api.github.com` 14 times per load. Owners currently bookmark each
`https://<app>.lan/` host by hand.

## Options
- (a) Ask upstream for a runtime app list (a JSON file or `?apps=`
  parameter), restore the launcher package and generate that list from
  installed craft packages with a drift check. Depends on an upstream change
  and an upstream release; the launcher's GitHub calls and trademarks remain.
- (b) **Chosen.** A Homestead-owned page produced by the daemon: a new
  authenticated dashboard page, a CLI command and an API route with
  parity (`docs/PARITY.md` rows), listing installed craft packages and their
  local HTTPS URLs from the catalog and edge route state. No change to the
  signed images and no new generated-config data class; stays under the
  operator session boundary (management remains local/overlay by default).
  Cost: it lives in the dashboard, not at a craft host, so it is not a
  standalone page for family members without a Homestead session.
- (c) A generated static page served by nginx beside the craft apps (for
  example from a small craft package). It works without a session but adds a
  generated-config data class, cross-package regeneration on every install
  and remove, a drift check and a change to the signed build context.

## Consequences
- A start page needs no GitHub or other external call; it works offline.
- Under (b), the page is derived state, so there is nothing to back up or
  drift-check; it adds one route class entry in the ADR-020 route table.
- Under (c), the signed build context and the craft manifest change and the
  owner must re-sign.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

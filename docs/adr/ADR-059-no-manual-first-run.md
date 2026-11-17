# No manual first run for any catalog app

## Status
Accepted

Accepted on 2026-11-07 on the owner's instruction: users should not have to walk through first run for any app in the catalog, and every app with a login must have single sign-on. On the same day the owner excluded Authentik: in a real deployment its first run stays with the owner. Amends ADR-055. The Vaultwarden exception (point 4) was decided on the owner's behalf and is flagged for review.

## Context
After install several apps still wait for a person: FreshRSS's web installer, and a first account in Actual Budget, Gitea, Memos, Navidrome and Vaultwarden. Others land with a well-known seeded login (Mealie, Calibre-Web Automated) or an empty first-visitor setup screen (Uptime Kuma). Komga and Audiobookshelf land with no library. Until a person acts the app is either unusable or open to whoever reaches it first.

## Decision
1. **The rule.** When a catalog install reports success, the app is usable: its administrator exists, no setup screen or seeded login is reachable, and, if it has a login and sign-in is available, Authentik sign-in works. A step that cannot complete fails the install visibly; it is never handed to the owner as a to-do.
2. **Authentik is excluded.** Authentik's first run, API token and owner link remain the owner's, as ADR-033, ADR-036 and ADR-054 say. The identity provider is the root of trust for the appliance, so the owner sets it up knowingly. An acceptance run may drive those same owner steps with a test harness; the product gains no automation for them.
3. **App routines.** Each app that needs it gets a routine in `internal/firstrun`: idempotent, pinned to the packaged version by test, with generated credentials sealed and revealable. Seeded and default passwords are replaced. Apps that need a library get one on the bulk storage class. Navidrome and Uptime Kuma get their administrator and forward-auth gate during install rather than by a later plan, amending ADR-055 point 3. The refusals of that ADR remain as safety checks.
4. **Vaultwarden exception.** A Vaultwarden master password is the key to that person's vault, so Homestead must not generate or hold it. Install applies sign-in and leaves no open signup path beyond it; each person chooses their own master password at first sign-in. This is the only permitted personal step in an app, and it concerns a person's own vault, not the app's setup.

## Consequences
- The per-app first-run steps in the owner checklist are replaced by checks that nothing is required. PLAT-SETUP-3a and the link in PLAT-SETUP-4 stay.
- Every routine is coupled to upstream behaviour, so an update can break it; pin tests make that a packaging failure rather than a surprise on the appliance.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

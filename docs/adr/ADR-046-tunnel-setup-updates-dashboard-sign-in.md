# Tunnel Setup Updates the Dashboard's Sign-in

## Status
Accepted

Accepted by the owner on 2026-10-27 after the VM 2 acceptance run.
Amends ADR-037 ("SSO re-plan") and S296's decision of 2026-10-20 that
the public dashboard name needs a separate Sign-in re-plan.

## Context
On VM 2 the tunnel was set up after sign-in with Authentik. The public
dashboard name answered, but its login page showed no Authentik button:
the configured sign-in held only the local origins, so
`/api/v1/sso/options` said `enabled: false` on the public name. ADR-037
asked the owner to re-plan Sign-in and approve it after the tunnel. The
tunnel plan said so in a note, and the owner missed it. The owner
expects setting up the tunnel to make the public sign-in work.

## Decision
1. **One approval.** When the tunnel plan publishes the dashboard, and
   sign-in with Authentik is configured, and the owner kept the Authentik
   API token (ADR-033's opt-in), the tunnel plan carries the sign-in
   change:
   - the public dashboard origin;
   - its redirect URI;
   - Authentik's public address, where browsers at that origin sign in.

   The change is shown in the plan's preview and is bound into its
   digest. Approving the tunnel plan approves it.
2. **Applied last.** A final `sign-in` step of the tunnel operation runs
   once the names are on the edge and the connector is up:
   - it plans sign-in again, from the configured sign-in plus the public
     origin;
   - it checks the result matches the change the owner approved;
   - it records the approval under the owner's name, and applies it with
     the kept token.

   Each part is audited as a normal Sign-in plan, approval and apply,
   plus a `tunnel.sign-in` entry that names the tunnel plan's digest.
3. **A failed sign-in step leaves the tunnel up.** The tunnel itself is
   working, so nothing is undone. The operation fails with a message
   saying sign-in was not updated, and the Sign-in page shows the
   re-plan.
4. **Without a kept token, nothing changes.** Homestead cannot change
   Authentik without the token. The plan keeps the note and links to the
   Sign-in re-plan, as before.
5. **Already covered.** If the configured sign-in already covers the
   public origin and Authentik's public address, the plan says so and
   adds no step.

Unchanged:
- Only Authentik sign-in works on the public name (ADR-037).
- The dashboard is published only once sign-in is linked.
- The token is used only if the owner chose to keep it.

## Alternatives considered
- **Keep the separate re-plan:** it already caused one failed acceptance
  run. Rejected by the owner.
- **Ask for the API token in the tunnel plan:** this puts a second
  secret prompt into the tunnel flow. Owners without a kept token
  already get a direct link to the re-plan.
- **Change the sign-in configuration without Authentik:** the redirect
  URI must also be added in Authentik, or Authentik refuses the callback.

## Consequences
- The tunnel plan gains a `sign_in_update` field (origin, redirect URI,
  Authentik URL), included in its digest.
- The tunnel setup operation gains a `sign-in` step, present only when
  the plan carries the change.
- Teardown does not remove the public origin from sign-in: the name no
  longer reaches the appliance, so the extra origin is inert. This is
  recorded in DEFERRED.
- Changes require a superseding ADR.

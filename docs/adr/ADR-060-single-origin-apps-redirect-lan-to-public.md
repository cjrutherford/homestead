# Single-origin apps redirect their LAN names to the public name

## Status
Accepted

**Decided by the orchestrator of the acceptance run (2026-11-07), not yet
reviewed by the owner.** The owner should review it; reversing it is a
manifest change (drop the marker) plus a re-render.

Amends ADR-047.

## Context
ADR-047 makes Authentik's public address the sign-in authority for a published
app. A browser therefore needs the public path to sign in at all. Some apps
also accept only one origin for the sign-in round trip. Observed on VM 2 once
the app was published, from its `.lan` name:
- Gitea: "could not find a matching session" after Authentik (the session
  cookie was set on the LAN origin, the callback arrives on the public one).
- Actual Budget: "Invalid redirect URL" (Actual checks the return address
  against its one OpenID server hostname).
- Mealie: state mismatch, because the app itself redirects to its public name.
- Calibre-Web Automated: the root redirects to the public name's `/login`.
FreshRSS and Memos sign in from both names, so the defect is per app.

## Decision
1. A package may declare `single_origin_sign_in: true`: sign-in completes on
   only one origin. First users: gitea, actual-budget, mealie,
   calibre-web-automated. It is set only for apps that truly need it.
2. While such an app is published (its route has a public tunnel name), the
   edge answers its LAN and `.local` names with a **307** redirect to the same
   path and query on the public name. 307, not 308, so a browser does not cache
   it: unpublishing makes the LAN name serve the app again at once. Request
   methods and bodies are preserved.
3. The public name keeps serving the app (tunnel entry is unchanged). The
   redirect exists only while the route has a public name, so unpublishing or
   removing the tunnel removes it and the app returns to its LAN name. An app
   that is not published, and an app that works on both names, is untouched.
4. The redirect is part of the public-address plan preview (the plan warnings,
   covered by the digest), so public exposure stays planned, previewed and
   auditable. The marker is stamped onto routes from the installed manifests on
   every edge re-render, so apps installed earlier take it through the normal
   re-render path with no manual step.
5. A route behind forward-auth (ADR-055) already redirects its other names and
   is not changed by this.

## Consequences
- LAN clients of such an app go through the tunnel (Cloudflare) and the
  internet, even on the home network.
- If the tunnel or the internet is down, those apps cannot be used from the
  LAN either; unpublish the app to bring its LAN name back.
- Non-browser clients follow the redirect (Gitea git over HTTP does; clients
  that do not follow redirects must use the public name).

Alternatives considered:
- Per-app second-origin settings: not available upstream for Actual, and
  fragile for the others.
- Leaving LAN sign-in broken with a warning (S447): a login that fails is a
  defect.

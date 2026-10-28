# People Administration on the Public Dashboard; Authentik Recovery Links

## Status
Accepted

The owner chose each decision below on 2026-10-28, after the VM 2
acceptance run at S331. Amends ADR-033 (step-up for SSO sessions) and
ADR-037 §4 (step-up refused on the public dashboard name), for the People
routes only, and ADR-043 (how a person first signs in).

## Context
Found on VM 2 (2026-10-28), signed in to the public dashboard through
Authentik:
- Adding a person succeeded, but no one-time link was shown. The default
  Authentik brand has no recovery flow, so Authentik refused the link.
  The dashboard showed a passing notice and closed the dialog, so the
  owner had neither a link nor a password to give.
- Resetting the password was refused with `reauth-required`. People's
  password and second-factor resets, admin grants, removal and drift
  settling are step-up routes (ADR-033, S317): an Authentik session must
  present the local password. The public name refuses the local password
  (ADR-037 §4), so these actions cannot run off the home network at all.

## Decision
### 1. Step-up on the public name is a fresh Authentik sign-in
On a public dashboard name, the People step-up routes accept a fresh
Authentik sign-in instead of the local password:
- The dashboard sends the owner through Authentik again, with the same
  fresh ceremony as linking (`max_age`, ADR-033). Authentik asks for the
  password and the second factor if the owner has one.
- The callback checks that the sign-in is fresh and is the linked owner's
  Authentik account. It marks the current session stepped up for five
  minutes. It creates no new session.
- Within those five minutes, People step-up routes from that session run
  without the local password. Each step-up and each action is audited, and
  the audit names the Authentik step-up.

The local password is still never accepted on a public name. On local and
overlay names, step-up stays as it is (the local password).

### 2. Only the People routes
These step-up routes accept the Authentik step-up on a public name:
- password reset;
- second-factor reset;
- app admin grant;
- removal;
- settling drift.

Every other step-up route stays local or overlay only: sign-in plans,
approve and apply, turning sign-in off, linking, secret reveal, rekey and
identity export.

### 3. Homestead sets up Authentik's recovery flow
When app sign-in through Authentik is set up (ADR-033 plan and apply), the
plan also creates Authentik's recovery flow and sets it on the default
brand, unless a recovery flow is already set:
- One-time links for new people and password resets work.
- Authentik's login page shows no "Forgot password" link; see the
  amendment (2026-10-28) at the end of this ADR.

Plans for appliances set up earlier show the missing flow and add it when
applied.

### 4. A failed first sign-in keeps the dialog open
If a person is added but their sign-in could not be set up, the add
dialog stays open:
- it names the reason;
- it offers to generate a password instead, shown once.

The person is kept, as today.

## Alternatives considered
- **No extra check on the public name:** a stolen dashboard session could
  reset passwords and grant admin. Not chosen.
- **Accept the local password over the tunnel:** Cloudflare's edge would
  see it in clear. Rejected; it reverses ADR-037 §4.
- **Every step-up route over the tunnel:** sign-in plans, secret reveal
  and identity export stay local. Not chosen by the owner.
- **Drop one-time links:** links are the safer default, because no
  password is shown. Not chosen.

## Consequences
- A new step-up start route and callback state for the public name. The
  dashboard's re-authentication dialog offers "Sign in with Authentik
  again" on a public name instead of the password field. PARITY gains the
  route; the CLI has no browser and keeps the local password.
- Sessions carry a short step-up expiry.
- The sign-in plan gains the recovery flow. Its digest covers it.
- Changes require a superseding ADR.

## Implementing slices
In order, one commit each:
1. S333: the recovery flow in the sign-in plan (§3) and the add dialog
   that stays open (§4).
2. S334: the Authentik step-up on the public name for the People routes
   (§1–§2).

## Amendment (2026-10-28)
The owner decided that people who forget their Authentik password ask the
owner, who resets it from People (over the tunnel since S334). This
corrects §3:
- the recovery flow exists only for one-time links that Homestead issues
  (new people and password resets);
- Authentik's login page shows no self-service recovery link, because the
  appliance has no email. Authentik shows that link only from the
  identification stage's own recovery flow, which Homestead leaves unset;
  the brand's flow is used for one-time links alone;
- the login page tells people to ask the owner to reset their password
  (S336);
- outbound email remains out of scope.

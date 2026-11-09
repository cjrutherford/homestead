# S335 — Step-up messages

## Goal
Close two message gaps the owner found testing S334 live: a failed
Authentik step-up showed nothing, and a local-only action on a public name
asked for a password the name never takes.

## Scope
- Dashboard: on load at `/` the shell reads `?sso_error=<code>`, shows a
  plain sentence for the code (`stale`, `session`, `unlinked`, `state`,
  `unavailable`, `denied`, `rate-limited`; anything else gets a generic
  line) as an error toast, then removes the param with a `replaceUrl`
  navigation. The login and sign-in pages keep reading their own codes.
- Server: before this slice a step-up route that is not one of the five
  People routes, on a public name, fell through to the password check and
  answered 403 `reauth-required`, so the dialog showed a password field
  the public name always refuses. It now answers 403 `local-only`
  (`X-Homestead-Error`). The link routes are still refused earlier as
  `public-host`. Local and overlay names are unchanged.
- Dashboard: on `local-only` the re-authentication dialog says "This
  action only works from your home network or overlay; open the dashboard
  at <local name> to continue", with no password field and no Authentik
  button. The local name is read from the sign-in setup's dashboard
  addresses (first one that is not this address); if unknown the dialog
  says "your local or overlay address".

## Rules
AGENTS.md slice rules apply. PARITY: no route changes; the new error code
is a refusal on existing routes only.

## Acceptance criteria
- Go: every non-People step-up route on the public name answers
  `local-only` (or `public-host` for the link routes); the local name still
  answers `reauth-required`.
- Dashboard specs: the message per code, the param removed from the
  address, the local-only interceptor path and dialog.
- All Go, Python and dashboard checks pass.

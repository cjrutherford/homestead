# S333 — Authentik recovery flow, and the add dialog keeps a failed sign-in

## Goal
ADR-049 §3 and §4. Found live on VM 2: the default Authentik brand has no
recovery flow, so `core/users/{pk}/recovery/` refused the one-time link.
Adding a person kept them with a warning, but the dashboard showed a
passing notice and closed the dialog, so the owner had neither a link nor
a password.

## Scope
- The dashboard sign-in spec (ADR-033) carries `recovery_flow:
  homestead-recovery`. Plan and apply, when the default brand has no
  recovery flow, create the flow (designation `recovery`, title "Reset
  your password", authentication `none`), a prompt stage with password
  and password-repeat fields (Authentik's stock password-change prompts
  when present, else Homestead-named ones), a user-write stage (never
  creates users) and a user-login stage, bound in that order, and set the
  flow on the default brand. Each object is found by slug or name first,
  so a re-apply after a partial one converges. A brand that already has a
  recovery flow is left alone.
- The plan lists a "Recovery flow" object with the change line; the
  digest covers the spec field. A sign-in applied before S333 shows a
  re-plan reason on the Sign-in page; setup's sign-in check is unchanged.
  App sign-in specs never set the field and never touch the brand.
- `POST /api/v1/people` also returns `sign_in_failed`. Grants are made
  even when sign-in failed. The dashboard keeps the dialog open on the
  added person, names the reason and offers "Generate a password
  instead" (the existing password-reset route, method `generated`), shown
  once. The CLI prints the reason and the `people reset-password PERSON
  --generate` command.

## Rules
AGENTS.md slice rules apply. No new routes, commands or surfaces; the add
response gains a field. PARITY is unchanged. Public-name step-up is S334.

## Acceptance criteria
- Go tests: the flow, prompts, stages and ordered bindings are created and
  set on the brand; re-plan shows unchanged and re-apply posts nothing; a
  partial apply converges; stock prompts are reused; a brand's own flow is
  left alone; no default brand is an error; app specs never touch it; the
  digest covers the field; an older spec gives a re-plan reason. People:
  a failed sign-in sets `sign_in_failed`, keeps the grants, and a
  generated password works after. CLI prints the suggestion.
- Dashboard specs: the dialog stays open with the reason, generates a
  password once, shows a refusal in place, and a grant-only warning still
  closes it.
- All Go, Python and dashboard checks pass.

## Live check
On VM 2, re-plan Sign-in: the recovery flow is listed and the re-plan
reason shows. After apply the default brand's recovery flow is
`homestead-recovery`. Adding a person shows a one-time link that opens
"Reset your password", sets the password and signs them in.

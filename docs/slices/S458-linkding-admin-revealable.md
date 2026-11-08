# S458 - Linkding: the local administrator's password is revealable

## Goal and dependencies
ADR-059 (no manual first run). Depends on S349, S409. Linkding creates its superuser `admin` once from the generated `superuser_password`, but the owner could never read that password (`apps secrets reveal` answered 403), so the local administrator account was unreachable.

## What changed
- `catalog/linkding.yaml`: `superuser_password` is now `reveal: true` (still immutable, still exported to the container as a 0600 file). The owner reveals it in the app card Manage panel or with `homesteadctl apps secrets reveal linkding superuser_password`.
- Dashboard first-login copy for Linkding replaced by the real flow. Checklist step SRV-LINKDING-3a added.

## Live (VM 2, fresh install through the wizard)
Sign-in with "Login with OIDC" from the LAN name: owner and person A signed in and landed on their own empty bookmark lists, person C was refused by Authentik ("Permission denied"). Person A saved a tagged bookmark and the tag filter found it. No sign-up route exists (registration, signup and register answer 404).

## Tests
`tests/test_s458_linkding_reveal.py`.

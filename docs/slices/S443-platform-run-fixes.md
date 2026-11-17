# S443 - Platform run fixes

## Goal and dependencies
Fix the defects found by the first browser acceptance run of the platform checklist (PLAT-CLAIM-1, PLAT-DEPLOY-1 and smaller findings). Depends on S442 only for ordering.

## What changed
- Claim: `GET /api/v1/identity` adds `setup_pending: true` when the appliance is claimed but has no administrator yet. The claim page resumes at the account step on that flag, so a reload or failure between the token step and account creation no longer reads "already set up". Security is unchanged: `POST /api/v1/admins/setup` was already open exactly while claimed with no administrator, and the flag only reveals that window.
- Unclaimed visit: the apps-page guard sends a signed-out visitor to `/claim` when the appliance is unclaimed or still awaiting its administrator, otherwise to `/login`.
- A 409 or 422 now shows the server's reason ("setup failed: ..."), so a refused domain explains itself.
- Authentik sign-in review: a typed API token is kept through approve even when Authentik was not read, so the field is not asked for again.
- `storage mounts` lists each mountpoint once (daemon namespace lists some paths twice; the visible one wins).
- Checklist text refreshed (commit reference, public Authentik address, step-up on action, real CLI command names, people added by CLI during setup); form regenerated.

## Not changed
The Exposure and Firewall pages' 404 reads (`/exposure/review`, `/wizards/firewall`) are the documented "nothing saved yet" answers (see DEFERRED.md); the pages handle them and show no error.

## Tests
Go: identity `setup_pending` before and after setup; mount dedupe. Dashboard specs: claim resume, home guard (unclaimed, pending), 409/422 message, sign-in token kept.

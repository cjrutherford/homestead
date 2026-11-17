# S386 — CLI installs complete the local address setup

## Goal
Make a normal `homesteadctl apps install` schedule the same reviewed local
address setup as the dashboard, so an installed app with a planned local
route can appear on the Apps page after the setup succeeds.

## Behavior
- For a planned non-public route, send the install digest and a setup
  object. The appliance applies the route after installation through the
  existing S324 operation, with its normal stop and resume behavior.
- Preserve any supplied sign-in and people approvals and the existing
  Nextcloud files-link approval. `--follow` reports setup failures.
- Installs without a route or files link do not acquire a new setup step.

## Verification and limits
CLI tests check that the reviewed digest and setup are sent, including
preservation of sign-in and people approvals. This slice does not implement
install-time Authentik approval in the CLI, new app adapters, person grants,
or a fallback tile for apps with no route; those remain separate work.

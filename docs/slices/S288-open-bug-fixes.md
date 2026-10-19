# S288 — Open bug fixes from the S285–S287 reviews

## Goal
Close the bugs left open by the S285, S286 and S287 KVM reviews: claim tokens building up in the system log, uninstall leaving empty stack folders, no way to unregister storage, secrets in CLI argv and output, and two risky dashboard actions without a confirm step.

## Dependencies
S284, S287

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Owner direction 2026-10-19: fix these as one slice, including the system log buildup. ADR-015 (secure first claim) is unchanged: the token stays short-lived and single-use; only how the owner reads it changes. New routes and commands follow ADR-004 parity.

## Acceptance Criteria
- Claim token, no log buildup: the daemon never writes a claim token's plaintext to stdout, stderr or the journal. While unclaimed it keeps exactly one live token, written to a root-only file under the runtime directory (mode 0600, replaced atomically, removed on claim), and mints a fresh one when the current one expires without a restart. The journal says once per start that the appliance is unclaimed and names the command that shows the token. `sudo homesteadctl claim token` (local, root, reads the file; no API) prints the token and its expiry, and says plainly when the appliance is already claimed or the daemon is not running. Expired token rows are pruned. Claim docs, the acceptance script and any test or doc that read the token from the journal are updated.
- Uninstall removes the app's empty stack directory under the stacks root (only when empty after the stack files are removed; never data folders), and a second uninstall of the same app gives a clear "not installed" answer rather than 422 on a leftover folder.
- Storage unregister and unassign: API routes to unassign a storage class and to unregister a mount, refused with a 409 naming the apps when installed apps still use them; never touches data on the disk; audited. CLI equivalents, Storage page actions behind the danger-confirm dialog with the impact shown, `docs/PARITY.md` rows, `routeClasses` entries. The Storage page also stops listing a mount twice when discovery returns it twice.
- Tunnel teardown CLI: the account ID (and token) can come from a file or stdin, never only from an argument; the argument form is removed or warns.
- CLI login: the session token is not printed to stdout by default (stored as today; an explicit flag prints it for scripts, documented).
- Confirm steps: Account "Unlink Authentik" and Trust "Re-issue all" open the S282 danger-confirm dialog with a plain description of the effect before acting (unlink still asks for the password through the S286 re-auth flow).
- Tests for each fix (Go, CLI, dashboard specs); route classification and parity tests green.
- KVM on VM 2: an unclaimed boot shows no token in the journal and `homesteadctl claim token` works, including after expiry (use VM 1 or a throwaway second daemon with its own state directory and port, so VM 2's claimed setup — owner, Authentik link, Jellyfin, tunnel — is untouched); uninstall and reinstall a small app and check the stack folder; unassign and unregister a test mount; the two confirm dialogs; transcript in `docs/DEVELOPMENT.md`.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Claim and operations docs: reading the claim token.
- `docs/DEFERRED.md`: close the fixed entries; record anything discovered.
- `docs/PARITY.md`: new rows.
- `docs/DEVELOPMENT.md`: S288 section with the KVM summary.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.

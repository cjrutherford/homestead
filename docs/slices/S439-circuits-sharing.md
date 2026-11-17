# S439 - Circuits sharing (ADR-058)

## Goal and dependencies
Turn Circuits' project sharing back on with Homestead meaning (ADR-058 slice row "Circuits: enable sharing"). Depends on S425-S429 (people registry, grants, FS sharing) and the Circuits package (S375).

## What upstream uses (pinned source, read from the archive)
`src/app/share.js` only: `fs.share(path, {username|email}, 'read'|'write')`, `fs.unshare`, `fs.getShares`, `fs.listShared` (the "Shared with me" section of Open project). No `getReadURL`, no `kv.share`, no `events`, no `perms`, no `appID`. So this is person-to-person file sharing and needs the sharing switch; there are no read links and the S432 viewer is not involved (an outsider has nothing to open; circuit files have no viewer renderer and none was invented).

## What changed
- `catalog/circuits.yaml`: `HOMESTEAD_EVERYTHING_SHARING: "1"`, description updated. `TestEverythingSharingSwitchIsPerPackage` now allows Tasks and Circuits.
- `scripts/everything/apps/circuits.json` (19 patches, same count): the two hiding patches (`file.share` command and menu entry) are replaced by two wording patches in `share.js` ("Save the project to your Homestead first", placeholder "Homestead username"). `sdk_surface.refused` is empty, `fs.share/unshare/getShares/listShared` are supported, `ai` and `net` stay disabled, `sharing` is no longer listed as disabled.
- `scripts/everything/puter-bridge.js` (synced): `fs.write` now fails with `subject_does_not_exist` when the bridge answers 404. A write through a revoked share previously resolved as a success, so Circuits would have shown "saved" for an edit that was thrown away. Own-path writes never answer 404.
- ADR-057 table row, review plan (S375 step 5 reworded, S439 section), DEFERRED, SLICE_INDEX.

## SDK table (Circuits)
| Call | Where | Result |
| --- | --- | --- |
| `fs.share(path, {username}, 'read'/'write')` | Share dialog | supported; recipient must have signed in to Circuits; unknown name `user_does_not_exist`, self `cannot_share_with_self`, email refused |
| `fs.getShares(path)`, `fs.unshare` | Share dialog list, Remove | supported |
| `fs.listShared()` | Open project, "Shared with me" | supported |
| `fs.read/write` on `~shared/<id>` | open, save | supported; read-only refused, revoked refused |
| `fs.rename/delete` on a shared file | Rename, Delete | refused for recipients (file grant) |

## Tests
- `tests/test_s375_circuits_package.py` (surface, no hiding patches, wording, switch on, no net), `tests/test_s368_tasks_package.py` (switch is Tasks and Circuits only), `internal/catalog` `TestEverythingSharingSwitchIsPerPackage`.
- `scripts/everything/gate/run_browser_check_circuits_sharing.sh` (real Chrome, three contexts, dev identity, sharing on): alice saves the example and uses the app's own Share dialog; an unknown name and herself are refused and nothing is shared; bob (view) finds the project under "Shared with me" ("view only"), opens it, his save is refused and alice's file is unchanged, he cannot delete, rename or re-share; alice sets edit, bob's save lands in alice's file (one file, bob holds none); carol (no grant) lists nothing, cannot read or write through bob's handle or alice's path, sees nothing in the dialog; alice removes bob, his list is empty and read and write fail; no CSP violation, no exception.
- `run_browser_check_circuits.sh` updated (it started the bridge without the switch and asserted `fs.share` absent) and passes.
- Upstream vitest on the patched tree: 206 of 207; the failure ("links to the source code on GitHub") predates this slice (S375 patch removes that link).

## Unproven
See DEFERRED S439 lines: no live Authentik run, no Firefox/Safari, no read link or viewer, rename/delete of a shared project error text, concurrent editing overwrites.

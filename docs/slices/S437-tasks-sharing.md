# S437 - Tasks sharing, read links and live updates (ADR-058)

## Goal and dependencies
Turn the Tasks UI's sharing back on with Homestead meaning (ADR-058, ADR-057 table): invite people by name to work on a list, read-only view links, a calendar feed, and live updates by events. Depends on S425-S432 (people registry, grants, KV and FS sharing, events, read links, viewer) and the Tasks package (S368). Tasks only; no other package sets the switch.

## What changed
- `catalog/tasks.yaml`: `HOMESTEAD_EVERYTHING_SHARING: "1"` (the same kind of switch as `HOMESTEAD_EVERYTHING_NET` for api-client). `cmd/everything-bridge/main.go` now reads `HOMESTEAD_EVERYTHING_SHARING` (the bare `SHARING` variable of S428 still works). The `-sharing` switch also turns on events and fs sharing, so this is the only switch. Description text updated.
- `scripts/everything/apps/tasks.json`: the two hiding patches (share button, calendar feed) are removed; `sdk_surface.refused` is empty and `getReadURL`, `kv.share/unshare/listShared/listSharedByMe`, the `{handle}` option, `perms.request`, `appID` and `events.onLocal` are listed as supported; only `ai` and `net` stay disabled. Nine exact-once patches:
  - `store.js`: the read-only link handed out is the bridge viewer (`/_homestead/read/<token>` becomes `/_homestead/view/<token>`), because an outsider has no sign-in and the app page requires one. The snapshot file and its token are unchanged; the snapshot is `tasks.shared-list`, which the S432 viewer renders (checked by the browser test, names, tasks and no script).
  - Puter wording in the invite sheet and error messages replaced ("Homestead username"; unknown name: "No one with access to Tasks is named ..."). Two upstream tests that assert the old strings are patched to match.
- Recipients: typed by name. The bridge resolves only people of this app who have signed in (people registry, S425); an unknown, never-signed-in, purged or other-app name gives the same `subject_does_not_exist` answer. Closed-by-default access (ADR-043) is what decides who can sign in at all, so only people with app access can ever be in the registry.
- `appID`: in the shim it is a synchronous getter that is `undefined` until the first `whoami` or share reply, then `app-tasks`. Tasks asks `auth.isSignedIn()` at boot, so it is set before any use; the code paths that need it (`events` subject for an owner's list, the `appUid` comparison) already treat "unknown" as "skip, poll instead". Checked in the browser test (`puter.appID === 'app-tasks'`).
- Dev identity (test only): `DevIdentity.RecordPeople(store)`, used by the `everythingdev` bridge, registers each development login in the people registry with the subject as name, so development people can share by name. Release builds are unaffected.
- Test: `scripts/everything/gate/browser-check-tasks-sharing.mjs` with `run_browser_check_tasks_sharing.sh`.

## SDK table (Tasks)
| Call | Where | Result |
| --- | --- | --- |
| `kv.share/unshare/listSharedByMe` | `store.js` invite, remove, leave, members | supported |
| `kv.listShared`, `kv.get/set/list/del {handle}` | share discovery and sync of a shared list | supported |
| `perms.request(KV_SHARE)` | first collaboration | resolves true; with sharing off, `invalid_argument` and Tasks uses links |
| `appID`, `events.onLocal` (`notif:app-user`, `kv:app-tasks:<prefix>*`, `kv:<handle>:*`) | live sync, share notices | supported |
| `fs.getReadURL(path, '1y')` | view-only link, calendar feed, legacy link collaboration | supported (365 days is the cap) |

## Tests
- Upstream vitest on the patched tree: 235 of 235.
- `tests/test_s368_tasks_package.py` (surface, switch is Tasks-only, patches), `internal/catalog` `TestEverythingSharingSwitchIsPerPackage`, `internal/everything` `TestDevIdentityRecordsPeople`.
- Real browser (headless Chrome, three contexts, bridge with sharing): alice creates a list and task, invites a never-signed-in name (refused), invites bob; bob sees the list and task, alice's next task arrives on bob by an event frame (counted `msg` frames), bob's task arrives on alice; carol (signed in, no grant) cannot read or write through the handle and sees nothing; alice's view-only link opens in the viewer in a context with no session (names and tasks shown, no script, no CSP violation) and returns 404 after "Stop sharing"; alice removes bob, bob's handle answers `kv_handle_not_found`, his list of shared items is empty and later tasks do not reach him.

## Behaviour to know
Removing a person (or leaving) keeps a private copy of the list as it is then, upstream's design; the shared data and handle are gone.

## Unproven
No OIDC two-person run against a live Authentik; no live Caddy or Cloudflare streaming for events (Tasks also polls); public read links need the S433 exposure plan; calendar feed reachability from a real calendar client; Firefox/Safari.

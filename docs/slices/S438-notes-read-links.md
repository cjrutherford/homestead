# S438 - Notes read-only links (ADR-058)

## Goal and dependencies
Turn Notes' "Create Link" back on with Homestead meaning (ADR-058 slice row: "Notes: enable read links with the viewer link"). Depends on S431 (read links), S432 (viewer), S433 (public exposure) and the Notes package (S371).

## What upstream offers
Notes has one sharing feature: publish a page as `published/<page id>.json` (`format: "notes-shared-page"`, `v: 2`, `title`, `outlines[{html,x,y,w}]`, `ink`, `published`) and hand out `fs.getReadURL(path, '1d'|'7d'|'30d'|'365d')`. It has no person-to-person sharing, no `kv.share`/`fs.share`, no events and no `perms` calls. So, unlike S437, the sharing switch (`HOMESTEAD_EVERYTHING_SHARING`) is **not** turned on: `fs.getReadURL` is not gated by it (checked in `handler.go`), and `TestEverythingSharingSwitchIsPerPackage` still requires Tasks to be the only package with it.

## What changed
- `scripts/everything/apps/notes.json` (11 patches, was 10):
  - the hiding patch for the Share dialog's read-only section is removed;
  - the link handed out is the bridge viewer: `/_homestead/read/<token>` becomes `/_homestead/view/<token>` (an outsider has no sign-in and the app page requires one);
  - the status text says the viewer shows text only (pictures and handwriting are not shown).
  `sdk_surface`: `fs.getReadURL` supported, `refused` unchanged otherwise (`kv.share`, `kv.unshare`, `kv.listShared`, `kv.listSharedByMe`, `perms.*`, `events.*`, `appID`: Notes never calls them), `ai` and `net` stay disabled, `sharing` stays disabled for this package.
- `catalog/notes.yaml`: description only (read-only link, text only, public only when planned and approved).
- Format check: the snapshot Notes writes (`format: notes-shared-page`) is exactly what `viewer.go` `renderViewer` parses; no viewer change was needed. The check proves text is rendered and picture and ink are marked "not shown in this viewer".
- ADR-057 table row for the sharing UI updated; review-plan section added.

## SDK table (Notes)
| Call | Where | Result |
| --- | --- | --- |
| `fs.getReadURL(path, '30d')` | `backend.readURL`, the Share dialog "Create Link" | supported (365 days is the cap) |
| Stop Sharing = `fs.delete(published/<id>.json)` | Share dialog | revokes the file's links (S431), link is a 404 |
| `ai`, `net` | Assist, none | disabled, unchanged |

## Tests
- `tests/test_s371_notes_package.py`: surface (getReadURL supported, nothing refused is called), 11 patches, viewer link and wording present, sharing and net switches absent from `catalog/notes.yaml`.
- `scripts/everything/gate/run_browser_check_notes_sharing.sh` (real Chrome, four contexts, dev identity, bridge as shipped without the sharing switch): alice publishes a page with text, a picture and a stroke; the link is a viewer URL; bob (signed in) and a visitor (no session) read the text with image and ink marked "not shown", no `<img>`, no script, no CSP violation; alice adds text and publishes again, the same link shows it; carol (signed in, no link) cannot read or link alice's file, has none of her pages, and a guessed token is a 404; `kv.share` answers `kv_handles_disabled`; alice's "Stop Sharing" makes the link a 404 for everyone.
- `scripts/everything/gate/run_browser_check_notes.sh` updated (it asserted no link controls) and passes.

## Unproven
No live Authentik run; public links need the S433 plan and were not tried through Caddy or Cloudflare; Firefox/Safari; pictures and ink never appear in the viewer by design (S432).

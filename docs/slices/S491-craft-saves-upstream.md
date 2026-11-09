# S491 - Craft saves recorded as upstream gaps

## Goal and dependencies
FigCraft, SoundCraft and DesignCraft keep no work across reload (acceptance run 2, CRAFT-FIGCRAFT-4/5, CRAFT-SOUNDCRAFT-5, CRAFT-DESIGNCRAFT-5). Owner 2026-11-08: record as an upstream gap for all three; build no shims. Docs and tests only. Depends on S487.

## Findings (read-only, upstream web zips pinned in `catalog/craft/craft-manifest.yaml`)
- FigCraft 0.6.0: the wasm glue imports no Blob, download, file-picker or IndexedDB calls; only localStorage, clipboard, files, href. HOSTING.md: native file dialogs are not wired up. A JS shim has nothing to hook.
- SoundCraft 0.7.0: glue imports `createObjectURL`, anchor `click` and `localStorage` (Bounce Mix downloads a WAV) but no IndexedDB or file picker. HOSTING.md: opens the synthesised demo session, no native file dialogs. No session save or open.
- DesignCraft 0.8.0: same imports. HOSTING.md calls Save and Export browser downloads (needs `allow-downloads` when sandboxed). Export Page as PNG works; Ctrl+S gave no file in the run.
- Homestead blocks nothing: the apps are served without an iframe sandbox and the manifests set no CSP that stops downloads (PNG and WAV exports work on VM 2).

## What changed
`docs/upstream/{figcraft,soundcraft,designcraft}-save-export.md` (issue drafts, not filed), DEFERRED entries, a SLICE_PLAN row, a note on the four checklist steps (known upstream gap, not a Homestead failure), regenerated acceptance form.

## Verification
`tests/test_s491_craft_saves_upstream.py`.

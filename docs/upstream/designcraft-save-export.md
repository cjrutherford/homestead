# DesignCraft: web save/export of the document (upstream request draft)

Status: save and export WORK in web build 0.8.0 (S491a re-check, VM 2, LAN and public path). The original problem below was a test-harness mistake (no download listener). Only the optional autosave request (option 2) remains; not filed. Target: storytold/designcraft.

## Problem (withdrawn for save/export; see status)
What works: Ctrl+S, Save, Save As download `Untitled-1.designcraft` (zip: mimetype, meta.json, document.json); Export Page as PNG, IDML, EPUB, interactive PDF, HTML, XML, Print Booklet download; Open reads the file back. Not autosaved: closing the tab loses unsaved work.

Original text:
Hosted on Homestead (one origin, no iframe sandbox), the DesignCraft web build keeps no work. After editing and pressing Ctrl+S, a reload shows a fresh or demo document, and localStorage and IndexedDB hold no document data. Export Page as PNG downloads, and HOSTING.md says Save and Export are browser downloads, but Ctrl+S produced no file and nothing is restored on reload (VM 2, 2026-11-08).

## Request
Give the web build a way to keep a document, either of:
1. **Download and upload**: Save writes the document to a file through a Blob download (`createObjectURL` plus an anchor click, or `showSaveFilePicker` where present); Open reads one through a file input or drop.
2. **IndexedDB**: autosave the current document to IndexedDB and restore it on load, with a way to clear it.

## Acceptance criteria
- Make a small document, save it, reload the page, reopen it: content is identical.
- Works in a normal tab and in an iframe with `sandbox="allow-scripts allow-same-origin allow-downloads"`.
- No network request carries the document; the file or IndexedDB entry stays on the device.
- HOSTING.md states how the web build saves and reopens, and what is lost on clearing site data.
- Round trip is covered by an upstream test.

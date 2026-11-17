# FigCraft: web save/export of the document (upstream request draft)

Draft only; not filed (S491). Target: storytold/figcraft, web build 0.6.0.

## Problem
Hosted on Homestead (one origin, no iframe sandbox), the FigCraft web build keeps no work. After editing and pressing Ctrl+S, a reload shows a fresh or demo document, and localStorage and IndexedDB hold no document data. The Export dialog (PNG to PDF) closes without a download; the 0.6.0 wasm imports no Blob, anchor-download, file-picker or IndexedDB APIs, and upstream HOSTING.md says native file dialogs are not wired up yet.

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

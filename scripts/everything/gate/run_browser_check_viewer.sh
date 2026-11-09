#!/usr/bin/env bash
# S432 optional real-browser check of the read-link viewer (dev identity, test only).
# usage: run_browser_check_viewer.sh <empty-work-dir>
# Builds the dev bridge, mints read links to hostile Tasks and Notes snapshots
# and drives headless Chrome to the viewer pages. Skips (exit 0, "SKIP:") when
# no Chrome/Chromium is available.
set -euo pipefail
work=${1:?empty work dir}
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
[ -d "$work" ] && [ -z "$(ls -A "$work")" ] || { echo "work dir must exist and be empty" >&2; exit 2; }
if ! node -e 'process.exit(typeof WebSocket==="function"?0:1)'; then echo "SKIP: Node without global WebSocket"; exit 0; fi
if ! command -v "${CHROME:-}" >/dev/null 2>&1 && ! command -v google-chrome >/dev/null && ! command -v chromium >/dev/null && ! command -v chromium-browser >/dev/null; then
  echo "SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)"; exit 0
fi
( cd "$repo" && go build -tags everythingdev -o "$work/everything-bridge-dev" ./cmd/everything-bridge )
mkdir -p "$work/tmp" "$work/dist"
echo '<!doctype html><title>x</title>' > "$work/dist/index.html"
node "$here/browser-check-viewer.mjs" "$work/everything-bridge-dev" "$work/dist" "$work/tmp"

#!/usr/bin/env bash
# S440 real-browser check of Parametric sharing (dev identity, test only).
# usage: run_browser_check_parametric_sharing.sh <empty-work-dir>
# Prepares the pinned Parametric source through prepare_app.py (patches applied),
# builds it, starts the dev bridge with sharing on, and drives three people
# in three separate browser contexts. Skips (exit 0, "SKIP:") without Chrome.
set -euo pipefail
work=${1:?empty work dir}
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
[ -d "$work" ] && [ -z "$(ls -A "$work")" ] || { echo "work dir must exist and be empty" >&2; exit 2; }
if ! node -e 'process.exit(typeof WebSocket==="function"?0:1)'; then echo "SKIP: Node without global WebSocket"; exit 0; fi
if ! command -v "${CHROME:-}" >/dev/null 2>&1 && ! command -v google-chrome >/dev/null && ! command -v chromium >/dev/null && ! command -v chromium-browser >/dev/null; then
  echo "SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)"; exit 0
fi
python3 -I "$repo/scripts/everything/prepare_app.py" parametric "$repo/third_party/everything/parametric-source.tar.gz" "$repo/scripts/everything/puter-bridge.js" "$work/prepared"
( cd "$repo" && go build -tags everythingdev -o "$work/everything-bridge-dev" ./cmd/everything-bridge )
app="$work/prepared/src/parametric"
( cd "$app" && npm ci --ignore-scripts --no-audit --no-fund >&2 && npm run build >&2 )
mkdir -p "$work/tmp"
node "$here/browser-check-parametric-sharing.mjs" "$work/everything-bridge-dev" "$app/dist" "$work/tmp"

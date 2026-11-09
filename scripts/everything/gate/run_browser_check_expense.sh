#!/usr/bin/env bash
# S380 optional real-browser check for Expenses (dev identity, test only).
# usage: run_browser_check_expense.sh <empty-work-dir>
# Prepares the pinned Expenses source with the shim, builds it,
# serves it from the dev bridge under /apps/expense and drives headless
# Chrome. Skips (exit 0, "SKIP:") when no Chrome/Chromium is available.
set -euo pipefail
work=${1:?empty work dir}
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
[ -d "$work" ] && [ -z "$(ls -A "$work")" ] || { echo "work dir must exist and be empty" >&2; exit 2; }
if ! node -e 'process.exit(typeof WebSocket==="function"?0:1)'; then echo "SKIP: Node without global WebSocket"; exit 0; fi
if ! command -v "${CHROME:-}" >/dev/null 2>&1 && ! command -v google-chrome >/dev/null && ! command -v chromium >/dev/null && ! command -v chromium-browser >/dev/null; then
  echo "SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)"; exit 0
fi
sha=$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["sha256"])' "$repo/scripts/everything/apps/expense.json")
echo "$sha  $repo/third_party/everything/expense-source.tar.gz" | sha256sum -c - >&2
python3 -I "$repo/scripts/everything/prepare_app.py" expense "$repo/third_party/everything/expense-source.tar.gz" "$repo/scripts/everything/puter-bridge.js" "$work/prepared"
( cd "$repo" && go build -tags everythingdev -o "$work/everything-bridge-dev" ./cmd/everything-bridge )
app="$work/prepared/src/expense"
( cd "$app" && npm ci --ignore-scripts --no-audit --no-fund >&2 && npm run build >&2 )
mkdir -p "$work/tmp"
node "$here/browser-check-expense.mjs" "$work/everything-bridge-dev" "$app/dist" "$work/tmp"

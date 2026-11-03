#!/usr/bin/env bash
# S423 optional real-browser check of the in-app source offer (dev identity, test only).
# usage: run_browser_check_source_offer.sh <empty-work-dir> [app...]   (default: notes three-d parametric)
# Prepares and builds each pinned app with the shim, builds its source offer exactly as the
# image does, serves both from the dev bridge and drives headless Chrome. Skips (exit 0,
# "SKIP:") when no Chrome/Chromium is available.
set -euo pipefail
work=${1:?empty work dir}; shift || true
apps=("$@"); [ ${#apps[@]} -gt 0 ] || apps=(notes three-d parametric)
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
[ -d "$work" ] && [ -z "$(ls -A "$work")" ] || { echo "work dir must exist and be empty" >&2; exit 2; }
if ! node -e 'process.exit(typeof WebSocket==="function"?0:1)'; then echo "SKIP: Node without global WebSocket"; exit 0; fi
if ! command -v "${CHROME:-}" >/dev/null 2>&1 && ! command -v google-chrome >/dev/null && ! command -v chromium >/dev/null && ! command -v chromium-browser >/dev/null; then
  echo "SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)"; exit 0
fi
( cd "$repo" && go build -tags everythingdev -o "$work/everything-bridge-dev" ./cmd/everything-bridge )
for app in "${apps[@]}"; do
  def="$repo/scripts/everything/apps/$app.json"
  archive=$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["archive"])' "$def")
  sub=$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["subtree"])' "$def")
  sha=$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["sha256"])' "$def")
  echo "$sha  $repo/third_party/everything/$archive" | sha256sum -c - >&2
  python3 -I "$repo/scripts/everything/prepare_app.py" "$app" "$repo/third_party/everything/$archive" "$repo/scripts/everything/puter-bridge.js" "$work/$app/prepared"
  ( cd "$work/$app/prepared/$sub" && npm ci --ignore-scripts --no-audit --no-fund >&2 && npm run build >&2 )
  python3 -I "$repo/scripts/everything/make_source_offer.py" "$app" "$repo/third_party/everything/$archive" "$repo/images/everything/Dockerfile" "$work/$app/offer"
  mkdir -p "$work/$app/tmp"
  node "$here/browser-check-source-offer.mjs" "$work/everything-bridge-dev" "$app" "$work/$app/prepared/$sub/dist" "$work/$app/offer" "$work/$app/tmp"
done

#!/usr/bin/env bash
# S367 behavioural gate: run pinned upstream Tasks tests against the REAL bridge.
# usage: run_gate.sh <empty-work-dir> [suite|bridge]
# The extracted source is untrusted data: dependencies install with
# --ignore-scripts and nothing from it is executed except the vitest runner
# (the upstream tests themselves) on purpose.
set -euo pipefail
work=${1:?empty work dir}; mode=${2:-suite}
here=$(cd "$(dirname "$0")" && pwd); repo=$(cd "$here/../../.." && pwd)
[ -d "$work" ] && [ -z "$(ls -A "$work")" ] || { echo "work dir must exist and be empty" >&2; exit 2; }
# pin.yaml has one document per app (separated by ---); the gate runs Tasks.
sha=$(sed -n '1,/^---$/s/^sha256: //p' "$repo/third_party/everything/pin.yaml")
echo "$sha  $repo/third_party/everything/tasks-source.tar.gz" | sha256sum -c - >&2
tar -xzf "$repo/third_party/everything/tasks-source.tar.gz" -C "$work"
( cd "$repo" && go build -tags everythingdev -o "$work/everything-bridge-dev" ./cmd/everything-bridge )
app="$work/src/tasks"
( cd "$app" && npm ci --ignore-scripts --no-audit --no-fund >&2 )
mkdir -p "$app/gate" "$work/scripts/everything"
cp "$repo/scripts/everything/puter-bridge.js" "$work/scripts/everything/"
cp "$here"/bridge-harness.mjs "$here"/global-setup.mjs "$here"/vitest.gate.config.mjs "$app/gate/"
cp "$here"/*.test.js "$app/gate/" 2>/dev/null || true
cp "$app/tests/helpers/mock-puter.js" "$app/tests/helpers/mock-puter-orig.js"
cp "$here/mock-puter-bridge.js" "$app/tests/helpers/mock-puter.js"
cd "$app"
HS_BRIDGE_BIN="$work/everything-bridge-dev" HS_GATE_MODE=$mode \
  npx vitest run --config gate/vitest.gate.config.mjs --reporter=json --outputFile="$work/result.json" || true
node -e '
const r=JSON.parse(require("fs").readFileSync(process.argv[1]));
console.log("tests passed",r.numPassedTests,"failed",r.numFailedTests,"total",r.numTotalTests);
for (const f of r.testResults){const p=f.assertionResults.filter(a=>a.status==="passed").length,t=f.assertionResults.length;console.log(f.name.split("/").slice(-2).join("/"),p+"/"+t, f.assertionResults.length===0?f.message.slice(0,200):"")}
' "$work/result.json"

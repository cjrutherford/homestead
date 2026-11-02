#!/usr/bin/env bash
# Developer/CI build of an Everything image outside the appliance, from the
# same context and pinned archive the appliance uses (ADR-057):
#   build-image.sh APP [TAG]      default tag homestead/everything-APP:dev
set -euo pipefail
app="${1:?usage: build-image.sh APP [TAG]}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tag="${2:-homestead/everything-$app:dev}"
def="$repo/scripts/everything/apps/$app.json"
archive="$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["archive"])' "$def")"
sha="$(python3 -I -c 'import json,sys;print(json.load(open(sys.argv[1]))["sha256"])' "$def")"
ctx="$(mktemp -d)"
trap 'rm -rf "$ctx"' EXIT
cp -r "$repo/images/everything/." "$ctx/"
mkdir "$ctx/artifact"
cp "$repo/third_party/everything/$archive" "$ctx/artifact/"
docker build --file "$ctx/Dockerfile" --build-arg "APP=$app" --build-arg "ARTIFACT_FILE=$archive" \
  --build-arg "ARTIFACT_SHA256=$sha" --tag "$tag" "$ctx"

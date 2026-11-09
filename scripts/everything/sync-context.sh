#!/usr/bin/env bash
# Copy the bridge sources into the Everything build context (ADR-057).
# images/everything/ besides its Dockerfile is a generated copy of the
# repository files the image build needs; a test fails if it drifts.
set -euo pipefail
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dst="$repo/images/everything"
find "$dst" -mindepth 1 ! -name Dockerfile ! -name compose.example.yaml ! -name compose.env.example -delete
mkdir -p "$dst/cmd" "$dst/internal" "$dst/scripts/everything/apps" "$dst/scripts/everything/files"
cp "$repo/go.mod" "$repo/go.sum" "$dst/"
cp -r "$repo/cmd/everything-bridge" "$dst/cmd/"
cp -r "$repo/internal/everything" "$dst/internal/"
find "$dst" -name '*_test.go' -delete
cp "$repo/scripts/everything/prepare_app.py" "$repo/scripts/everything/make_source_offer.py" "$repo/scripts/everything/puter-bridge.js" "$repo/scripts/everything/wording.json" \
   "$dst/scripts/everything/"
cp "$repo"/scripts/everything/*-sw.template.js "$dst/scripts/everything/"
cp "$repo"/scripts/everything/apps/*.json "$dst/scripts/everything/apps/"
cp "$repo"/scripts/everything/files/* "$dst/scripts/everything/files/"
echo "synced $dst"

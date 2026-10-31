#!/usr/bin/env bash
# record-debug-fixtures.sh -- record headscale /debug/* and /metrics bodies
#
# Runs the integration shape-drift check (test/integration, TestShapeDrift)
# against a dockerised headscale and saves the raw responses to OUT/<series>
# (v029 for 0.29.x, v030 for main). The check fails if any parser returns
# ErrUnsupportedShape. Compare the recordings with testdata/debug/ by hand:
# the committed fixtures carry the values the unit tests assert.
#
# Usage: scripts/record-debug-fixtures.sh [TAG] [OUT]
#   TAG  headscale/headscale image tag (default 0.29.6; "development" is main)
#   OUT  output directory (default ./recorded-debug)
set -euo pipefail

tag=${1:-0.29.6}
out=$(cd "$(dirname "${2:-recorded-debug}")" && pwd)/$(basename "${2:-recorded-debug}")
mkdir -p "$out"
HEADSCALE_IMAGE_TAG="$tag" HEADSUP_RECORD_FIXTURES="$out" \
  go test -count=1 -tags integration -run TestShapeDrift -v ./test/integration/
echo "recorded into $out"

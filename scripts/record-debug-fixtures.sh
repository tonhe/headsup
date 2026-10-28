#!/usr/bin/env bash
# record-debug-fixtures.sh -- record headscale /debug/* and /metrics fixtures
#
# STATUS: stub. The fixtures in testdata/debug/{v029,v030} are currently
# hand-built from the handler source (see testdata/debug/README.md). This
# script documents how they will be recorded from real servers; the weekly
# shape-drift CI job (research.md R20) will run it and then
# `go test ./internal/host/debug/...`.
#
# Plan:
#   1. For each ref (v0.29.6 -> v029, main -> v030), start
#      `docker run -d --name hs-fixture-$dir -p 127.0.0.1:19090:9090
#        -v $tmp/config.yaml:/etc/headscale/config.yaml
#        headscale/headscale:$tag serve`
#      with metrics_listen_addr: 0.0.0.0:9090 and a throwaway sqlite DB.
#   2. Create a user, two nodes advertising 10.0.0.0/24 (HA pair) and one
#      pending web registration, using `docker exec ... headscale ...`.
#   3. Fetch, saving raw bodies:
#        /debug/registration-cache                    -> registration-cache.json
#        /debug/routes  (Accept: application/json)    -> routes.json
#        /debug/routes  (default text)                -> routes.txt
#        /debug/ping?node=<id>                        -> ping-ok.html / ping-timeout.html
#        /debug/ping?node=999                         -> ping-not-connected.html
#        /metrics                                     -> metrics.txt
#   4. Remove the container and diff against the committed fixtures.
#
# Usage: scripts/record-debug-fixtures.sh [v029|v030]   (not implemented yet)
set -euo pipefail

echo "record-debug-fixtures.sh: not implemented yet; fixtures in testdata/debug are hand-built." >&2
echo "See the header of this script for the recording plan." >&2
exit 2

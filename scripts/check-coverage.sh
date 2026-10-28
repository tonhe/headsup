#!/usr/bin/env bash
# Enforces per-package coverage floors listed in scripts/coverage-floors.txt.
# Floors only go up (constitution principle V).
set -euo pipefail
cd "$(dirname "$0")/.."
FAILED=0
while read -r pkg floor; do
  [[ -z "${pkg}" || "${pkg}" == \#* ]] && continue
  out=$(go test -cover "${pkg}" 2>&1 || true)
  pct=$(printf '%s\n' "${out}" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p' | head -1)
  if [[ -z "${pct}" ]]; then
    echo "FAIL ${pkg}: no coverage reported"; printf '%s\n' "${out}" | tail -5; FAILED=1; continue
  fi
  if awk -v p="${pct}" -v f="${floor}" 'BEGIN{exit !(p+0 < f+0)}'; then
    echo "FAIL ${pkg}: ${pct}% < floor ${floor}%"; FAILED=1
  else
    echo "ok   ${pkg}: ${pct}% (floor ${floor}%)"
  fi
done < scripts/coverage-floors.txt
exit "${FAILED}"

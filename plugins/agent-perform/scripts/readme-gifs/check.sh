#!/usr/bin/env bash
# Scan the text of every page state record.py captured: no removed features and no real paths. The only
# home path allowed is the synthetic /Users/dev; the temp HOME must never show.
#
#   scripts/readme-gifs/check.sh <workdir>
set -uo pipefail
bad=0
for f in "${1:?usage: check.sh <workdir>}"/*.txt; do
  states=$(( $(grep -c '^=====' "$f") + 1 ))
  hits=$(grep -oE '更正|本機模型|派工|/private/|/tmp/|/var/folders/' "$f" | sort | uniq -c)
  odd=""
  for u in $(grep -oE '/Users/[A-Za-z0-9_.-]*' "$f" | sort -u); do
    [[ "/Users/dev" == "$u"* ]] || odd="$odd $u"
  done
  echo "$(basename "$f" .txt): $states page states"
  [ -z "$hits" ] || { echo "  FAIL: $hits"; bad=1; }
  [ -z "$odd" ] || { echo "  FAIL: unexpected home path$odd"; bad=1; }
done
exit $bad

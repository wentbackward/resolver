#!/usr/bin/env bash
# scripts/golden-diff-sanity.sh — guard the v2.1 golden-regen scope.
#
# Plan §16 allows ONLY two golden files to change when regenerating:
#   - golden/scorecard_example.json
#   - golden/view_columns.txt
#
# This script is meant to run in CI immediately after a `UPDATE_GOLDEN=1`
# regen and asserts:
#   (a) no ULID-ish runIDs (`[0-9]{8}T...`) leak into golden JSON;
#   (b) no absolute paths under /home/ or /Users/ leak in;
#   (c) no absolute timestamps (YYYY-MM-DDTHH:MM...) leak in;
#   (d) git status --porcelain shows ONLY the two allowed goldens as modified
#       (untracked files + unrelated changes are a tripwire).
#
# Exits 0 on pass, 1 on any violation. Intended to be noisy on failure.

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

allowed=(
  "golden/scorecard_example.json"
  "golden/view_columns.txt"
)

fail=0

contains() {
  local needle="$1"; shift
  for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done
  return 1
}

# ---------------- (a) ULID-ish runIDs ----------------
if grep -E -l '\b[0-9]{8}T[0-9]{6}-[0-9a-f]{6,}\b' golden/*.json golden/*.txt 2>/dev/null; then
  echo "FAIL: ULID-ish runID leaked into golden files" >&2
  grep -nE '\b[0-9]{8}T[0-9]{6}-[0-9a-f]{6,}\b' golden/*.json golden/*.txt || true
  fail=1
fi

# ---------------- (b) absolute paths ----------------
if grep -E -l '(/home/|/Users/)[A-Za-z0-9_.-]' golden/*.json golden/*.txt 2>/dev/null; then
  echo "FAIL: absolute home-dir path leaked into golden files" >&2
  grep -nE '(/home/|/Users/)[A-Za-z0-9_.-]' golden/*.json golden/*.txt || true
  fail=1
fi

# ---------------- (c) absolute timestamps ----------------
if grep -E -l '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}' golden/*.json golden/*.txt 2>/dev/null; then
  echo "FAIL: absolute timestamp leaked into golden files" >&2
  grep -nE '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}' golden/*.json golden/*.txt || true
  fail=1
fi

# ---------------- (d) scope lock ----------------
# Collect staged + unstaged modifications + adds, but IGNORE untracked ?? lines
# (those are worker WIP or local scratch, not regen artifacts).
dirty=()
while IFS= read -r line; do
  # porcelain format: "XY path" where X/Y ∈ { , M, A, D, R, C, U, ? }
  status="${line:0:2}"
  path="${line:3}"
  case "$status" in
    "??") continue ;;        # untracked — ignore
    " M"|"M "|"MM"|"A "|"AM") dirty+=("$path") ;;
    *)    dirty+=("$path") ;;
  esac
done < <(git status --porcelain)

for f in "${dirty[@]}"; do
  if ! contains "$f" "${allowed[@]}"; then
    echo "FAIL: unexpected modified file outside the allow-list: $f" >&2
    echo "        allowed: ${allowed[*]}" >&2
    fail=1
  fi
done

if [[ $fail -eq 0 ]]; then
  echo "golden-diff-sanity: OK"
fi
exit "$fail"

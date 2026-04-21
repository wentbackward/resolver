#!/usr/bin/env bash
# scripts/sweep.sh — run the resolver v2.1 role sweep across virtual models.
#
# For every (model, role) pair this runs:
#
#   resolver --endpoint $ENDPOINT --model <virtual> --role <role> \
#            --n $N --run-config <sidecar> --out <tmp>
#
# then moves the resulting scorecards + manifests into
# research/captures/<real_model_slug>/<virtual>/<role>/ and aggregates
# the DuckDB at the end.
#
# Everything committable lives under research/captures/. Temporary
# per-run output goes to mktemp dirs and is cleaned up.
#
# Usage:
#   scripts/sweep.sh                                           # defaults
#   scripts/sweep.sh --models "gresh-general gresh-coder"      # subset
#   scripts/sweep.sh --roles "agentic-toolcall safety-refuse"  # subset
#   scripts/sweep.sh --n 5                                     # more repeats
#   scripts/sweep.sh --endpoint http://other-host:4000/v1/chat/completions
#   scripts/sweep.sh --sidecar-dir /path/to/sidecars           # custom
#   scripts/sweep.sh --dry-run                                 # show plan, exit
#   scripts/sweep.sh --skip-existing                           # don't rerun (model,role) that already has a scorecard
#   scripts/sweep.sh --help
#
# Sidecars are expected at $SIDECAR_DIR/sidecar-<virtual>.yaml. Each must
# set `real_model: <owner/name>` so this script knows where to land the
# captures under research/captures/<slug>/.
#
# Env var overrides: MODELS, ROLES, N, ENDPOINT, SIDECAR_DIR.
#
# Exits 0 on a clean sweep, 1 on pre-flight error, 130 on Ctrl-C.

set -euo pipefail

# ---- defaults (any can be overridden via env or flags) --------------------
MODELS_DEFAULT="gresh-general gresh-coder gresh-reasoner"
ROLES_DEFAULT="agentic-toolcall safety-refuse safety-escalate health-check \
node-resolution dep-reasoning hitl multiturn tool-count-survival long-context \
reducer-json classifier"

MODELS="${MODELS:-$MODELS_DEFAULT}"
ROLES="${ROLES:-$ROLES_DEFAULT}"
N="${N:-3}"
ENDPOINT="${ENDPOINT:-http://localhost:4000/v1/chat/completions}"
SIDECAR_DIR="${SIDECAR_DIR:-/tmp}"
DRY_RUN=0
SKIP_EXISTING=0

# ---- arg parsing ---------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --models)         MODELS="$2"; shift 2 ;;
    --roles)          ROLES="$2"; shift 2 ;;
    --n)              N="$2"; shift 2 ;;
    --endpoint)       ENDPOINT="$2"; shift 2 ;;
    --sidecar-dir)    SIDECAR_DIR="$2"; shift 2 ;;
    --dry-run)        DRY_RUN=1; shift ;;
    --skip-existing)  SKIP_EXISTING=1; shift ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      echo "use --help for options" >&2
      exit 2
      ;;
  esac
done

# ---- repo root + pre-flight ----------------------------------------------
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

if [[ ! -x resolver ]]; then
  echo "==> resolver binary missing; building..."
  go build -o resolver ./cmd/resolver
fi
if [[ ! -x .reporting/resolver-duckdb ]]; then
  echo "==> .reporting/resolver-duckdb missing; building..."
  mkdir -p .reporting
  go build -tags duckdb -o .reporting/resolver-duckdb ./cmd/resolver
fi

# endpoint probe (warn, don't fail — user may have a reason to run offline)
probe_url="${ENDPOINT%/chat/completions}/models"
probe_code=$(curl -sS -m 3 -o /dev/null -w '%{http_code}' "$probe_url" || echo 000)
case "$probe_code" in
  200|401|404) : ;;
  *) echo "WARN: $probe_url probe returned HTTP $probe_code — endpoint may be unreachable" >&2 ;;
esac

# validate sidecars
missing_sidecars=()
for virt in $MODELS; do
  sidecar="$SIDECAR_DIR/sidecar-$virt.yaml"
  if [[ ! -f "$sidecar" ]]; then
    missing_sidecars+=("$sidecar")
  elif ! grep -q '^real_model:' "$sidecar"; then
    echo "ERROR: $sidecar missing 'real_model:' line" >&2
    exit 1
  fi
done
if [[ ${#missing_sidecars[@]} -gt 0 ]]; then
  echo "ERROR: missing sidecar(s):" >&2
  printf '  %s\n' "${missing_sidecars[@]}" >&2
  echo "Each virtual model needs $SIDECAR_DIR/sidecar-<virtual>.yaml." >&2
  echo "See existing examples under $SIDECAR_DIR or research/captures/*/*/run-config.yaml." >&2
  exit 1
fi

# ---- plan ----------------------------------------------------------------
N_MODELS=$(wc -w <<< "$MODELS")
N_ROLES=$(wc -w <<< "$ROLES")
TOTAL=$((N_MODELS * N_ROLES))

echo "==> sweep plan"
echo "    models:   $MODELS"
echo "              ($N_MODELS)"
echo "    roles:    $ROLES"
echo "              ($N_ROLES)"
echo "    n:        $N   (repeats per scenario)"
echo "    endpoint: $ENDPOINT"
echo "    sidecars: $SIDECAR_DIR/sidecar-<virtual>.yaml"
echo "    total role-runs: $TOTAL"
[[ $SKIP_EXISTING -eq 1 ]] && echo "    --skip-existing: will skip (model,role) that already has a scorecard"
echo

if [[ $DRY_RUN -eq 1 ]]; then
  echo "==> --dry-run: stopping before firing"
  exit 0
fi

# ---- Ctrl-C cleanup ------------------------------------------------------
DONE=0
OK=0
FAILED=0
SKIPPED=0
cleanup() {
  # kill any child resolver processes we spawned
  pkill -P $$ 2>/dev/null || true
  echo ""
  echo "==> interrupted; $DONE/$TOTAL role-runs completed ($OK ok, $FAILED failed, $SKIPPED skipped) before stop"
  exit 130
}
trap cleanup INT TERM

# ---- run -----------------------------------------------------------------
START_TS=$(date +%s)
echo "==> sweep starting $(date -u +%H:%M:%S) UTC"
echo ""

for virt in $MODELS; do
  sidecar="$SIDECAR_DIR/sidecar-$virt.yaml"
  real=$(grep '^real_model:' "$sidecar" | awk '{print $2}' | tr -d '"' | tr / _)
  if [[ -z "$real" ]]; then
    echo "ERROR: $sidecar has blank real_model" >&2
    exit 1
  fi

  for role in $ROLES; do
    DONE=$((DONE + 1))
    dest="research/captures/$real/$virt/$role"

    if [[ $SKIP_EXISTING -eq 1 ]] && compgen -G "$dest/*.json" >/dev/null; then
      printf '[%2d/%d] %-18s × %-22s SKIP (existing capture)\n' "$DONE" "$TOTAL" "$virt" "$role"
      SKIPPED=$((SKIPPED + 1))
      continue
    fi

    mkdir -p "$dest"
    cp "$sidecar" "$dest/run-config.yaml"
    tmpout=$(mktemp -d -t sweep.XXXXXX)

    printf '[%2d/%d] %-18s × %-22s ' "$DONE" "$TOTAL" "$virt" "$role"
    t0=$(date +%s)
    if ./resolver \
         --endpoint "$ENDPOINT" \
         --model "$virt" \
         --role "$role" \
         --n "$N" \
         --run-config "$dest/run-config.yaml" \
         --out "$tmpout" >"$tmpout/log" 2>&1; then
      find "$tmpout" -maxdepth 1 -type f -name '*.json' -exec mv {} "$dest/" \;
      [[ -d "$tmpout/manifests" ]] && mv "$tmpout/manifests" "$dest/"
      verdict=$(grep -oE '^\s+'"$role"'\s+\S+' "$tmpout/log" 2>/dev/null | tail -1 | awk '{print $2}')
      printf 'OK  (%s, %ds)\n' "${verdict:-?}" "$(( $(date +%s) - t0 ))"
      OK=$((OK + 1))
    else
      printf 'FAIL (see %s)\n' "$dest/run-config.yaml"
      cp "$tmpout/log" "$dest/sweep-failure.log" 2>/dev/null || true
      FAILED=$((FAILED + 1))
    fi
    rm -rf "$tmpout"
  done
done

ELAPSED=$(($(date +%s) - START_TS))
echo ""
echo "==> aggregating reports/resolver.duckdb..."
./.reporting/resolver-duckdb aggregate 2>&1 | tail -3

echo ""
echo "==> sweep done in ${ELAPSED}s  ($OK ok, $FAILED failed, $SKIPPED skipped, of $TOTAL)"
echo "    captures:  research/captures/"
echo "    heat-map:  scripts/report.sh   (opens the notebook)"

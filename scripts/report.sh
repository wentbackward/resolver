#!/usr/bin/env bash
# scripts/report.sh — one-command resolver reporting environment.
#
# Sets up a repo-local venv, builds the duckdb-tagged resolver, refreshes
# the aggregate DuckDB, copies the tracked notebook templates into a
# personal workspace, and launches Jupyter.
#
# Everything ephemeral goes under .reporting/ (gitignored). Safe to delete
# .reporting/ at any time — the next run will recreate it.
#
# Usage:
#   scripts/report.sh              # full run: setup + aggregate + launch
#   scripts/report.sh --no-launch  # stop before launching Jupyter (for CI / smoke tests)
#   scripts/report.sh --refresh    # rebuild binary + re-aggregate even if cached

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

REPORTING=".reporting"
VENV="$REPORTING/venv"
BIN="$REPORTING/resolver-duckdb"
NBDIR="$REPORTING/notebooks"
DB="reports/resolver.duckdb"

NO_LAUNCH=0
REFRESH=0
for arg in "$@"; do
  case "$arg" in
    --no-launch) NO_LAUNCH=1 ;;
    --refresh)   REFRESH=1 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      echo "use --help for options" >&2
      exit 2
      ;;
  esac
done

mkdir -p "$REPORTING" "$NBDIR"

# 1. Python venv (one-time, unless deleted)
if [[ ! -x "$VENV/bin/jupyter" ]]; then
  echo "==> setting up reporting venv at $VENV (one-time, ~30s)..."
  if ! command -v uv >/dev/null 2>&1; then
    echo "ERROR: uv not found on PATH." >&2
    echo "  Install from https://github.com/astral-sh/uv (curl -LsSf https://astral.sh/uv/install.sh | sh)" >&2
    exit 1
  fi
  uv venv "$VENV"
  uv pip install --python "$VENV/bin/python" -e "tools/analyze[notebook]"
fi

# 2. Resolver binary (duckdb-tagged, CGO)
if [[ "$REFRESH" -eq 1 ]] || [[ ! -x "$BIN" ]]; then
  echo "==> building resolver -tags duckdb..."
  go build -tags duckdb -o "$BIN" ./cmd/resolver
fi

# 3. Aggregate (idempotent — upserts by run_id)
echo "==> aggregating reports/ + research/captures/ -> $DB..."
"./$BIN" aggregate

# 4. Seed user notebook workspace from tracked templates.
# Only copies files that don't already exist in the user workspace — if you
# want the latest template back, delete .reporting/notebooks/<name>.ipynb
# and rerun this script.
for src in tools/analyze/notebooks/*.ipynb; do
  dst="$NBDIR/$(basename "$src")"
  if [[ ! -f "$dst" ]]; then
    echo "==> seeding $dst"
    cp "$src" "$dst"
  fi
done

# 5. Launch (unless suppressed)
if [[ "$NO_LAUNCH" -eq 1 ]]; then
  echo ""
  echo "==> setup complete (--no-launch). To open Jupyter manually:"
  echo "    $VENV/bin/jupyter notebook --notebook-dir=$NBDIR"
  exit 0
fi

echo ""
echo "==> launching Jupyter"
echo "    workspace: $NBDIR/"
echo "    open 'quickstart.ipynb' and click Kernel -> Restart & Run All"
echo "    Ctrl-C here to stop the server"
echo ""
exec "$VENV/bin/jupyter" notebook --notebook-dir="$NBDIR"

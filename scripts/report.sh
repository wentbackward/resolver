#!/usr/bin/env bash
# scripts/report.sh — one-command resolver reporting environment.
#
# Sets up a repo-local venv, builds the duckdb-tagged resolver, refreshes
# the aggregate DuckDB, copies the tracked notebook templates into a
# personal workspace, and (by default) launches Jupyter.
#
# Everything ephemeral goes under .reporting/ (gitignored). Safe to delete
# .reporting/ at any time — the next run will recreate it.
#
# Usage:
#   scripts/report.sh             # full run: setup + aggregate + launch Jupyter (foreground)
#   scripts/report.sh --shell     # Jupyter in background + venv-activated subshell;
#                                 # type 'exit' to stop Jupyter and return
#   scripts/report.sh --no-launch # stop before launching Jupyter (for CI / smoke tests)
#   scripts/report.sh --refresh   # rebuild binary + re-aggregate even if cached
#   scripts/report.sh --help      # print this message

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

REPORTING=".reporting"
VENV="$REPORTING/venv"
BIN="$REPORTING/resolver-duckdb"
NBDIR="$REPORTING/notebooks"
DB="reports/resolver.duckdb"
PIDFILE="$REPORTING/jupyter.pid"
LOGFILE="$REPORTING/jupyter.log"

NO_LAUNCH=0
REFRESH=0
SHELL_MODE=0
for arg in "$@"; do
  case "$arg" in
    --no-launch) NO_LAUNCH=1 ;;
    --refresh)   REFRESH=1 ;;
    --shell)     SHELL_MODE=1 ;;
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

if [[ "$SHELL_MODE" -eq 1 && "$NO_LAUNCH" -eq 1 ]]; then
  echo "ERROR: --shell and --no-launch are mutually exclusive." >&2
  exit 2
fi

mkdir -p "$REPORTING" "$NBDIR"

# Safety net: clean up a stale jupyter pid from a prior --shell run that
# crashed without the EXIT trap firing.
if [[ -f "$PIDFILE" ]]; then
  oldpid=$(cat "$PIDFILE" 2>/dev/null || echo 0)
  if [[ "${oldpid:-0}" -gt 0 ]] && kill -0 "$oldpid" 2>/dev/null; then
    echo "==> killing stale jupyter (pid $oldpid) from prior run..."
    kill "$oldpid" 2>/dev/null || true
    sleep 0.5
    kill -9 "$oldpid" 2>/dev/null || true
  fi
  rm -f "$PIDFILE"
fi

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
# Only copies files that don't already exist so user edits survive reruns.
# Delete .reporting/notebooks/<name>.ipynb to pick up template updates.
for src in tools/analyze/notebooks/*.ipynb; do
  dst="$NBDIR/$(basename "$src")"
  if [[ ! -f "$dst" ]]; then
    echo "==> seeding $dst"
    cp "$src" "$dst"
  fi
done

# 5a. --no-launch: done.
if [[ "$NO_LAUNCH" -eq 1 ]]; then
  echo ""
  echo "==> setup complete (--no-launch). To open Jupyter manually:"
  echo "    $VENV/bin/jupyter notebook --notebook-dir=$NBDIR"
  exit 0
fi

# 5b. --shell: background Jupyter + venv-activated subshell + trap cleanup.
if [[ "$SHELL_MODE" -eq 1 ]]; then
  cleanup() {
    local code=$?
    if [[ -f "$PIDFILE" ]]; then
      local pid
      pid=$(cat "$PIDFILE" 2>/dev/null || echo 0)
      if [[ "${pid:-0}" -gt 0 ]] && kill -0 "$pid" 2>/dev/null; then
        echo ""
        echo "==> stopping jupyter (pid $pid)..."
        kill "$pid" 2>/dev/null || true
        sleep 0.5
        kill -9 "$pid" 2>/dev/null || true
      fi
      rm -f "$PIDFILE"
    fi
    exit $code
  }
  trap cleanup EXIT INT TERM

  echo "==> starting jupyter in background (log: $LOGFILE)..."
  : > "$LOGFILE"
  "$VENV/bin/jupyter" notebook --notebook-dir="$NBDIR" --no-browser > "$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"

  # Wait up to 6s for jupyter to print its URL.
  for _ in $(seq 1 30); do
    if grep -qE 'http://[^[:space:]]+' "$LOGFILE" 2>/dev/null; then
      break
    fi
    sleep 0.2
  done

  echo ""
  echo "==> jupyter URLs:"
  grep -oE 'http://[^[:space:]]+' "$LOGFILE" | sort -u | sed 's/^/    /' || \
    echo "    (URL not yet in log — tail $LOGFILE)"
  echo ""
  echo "==> launching venv-activated subshell"
  echo "    python / pytest / analyze all point at $VENV/"
  echo "    type 'exit' to stop jupyter and return"
  echo ""

  # Don't exec — we need the EXIT trap to fire when the subshell returns.
  env VIRTUAL_ENV="$PWD/$VENV" \
      PATH="$PWD/$VENV/bin:$PATH" \
      "${SHELL:-bash}" -i || true

  # Trap handles cleanup.
  exit 0
fi

# 5c. Default: foreground Jupyter.
echo ""
echo "==> launching Jupyter"
echo "    workspace: $NBDIR/"
echo "    open 'quickstart.ipynb' and click Kernel -> Restart & Run All"
echo "    Ctrl-C here to stop the server"
echo ""
exec "$VENV/bin/jupyter" notebook --notebook-dir="$NBDIR"

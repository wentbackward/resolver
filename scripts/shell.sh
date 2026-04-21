#!/usr/bin/env bash
# scripts/shell.sh — enter the resolver project shell.
#
# Drops you into an interactive subshell with:
#   - `resolver` + `resolver-duckdb` binaries on PATH
#     (via .reporting/bin/ symlinks; built on demand)
#   - `scripts/` on PATH — `sweep.sh`, `report.sh`, `shell.sh` invokable by name
#   - the analyze tool's venv active (python/pytest/analyze/jupyter)
#   - a `(resolver)` prompt prefix so you know you're in the project shell
#   - Jupyter running in the background for reports
#
# Type `exit` to leave — Jupyter and the shell both shut down together.
#
# Usage:
#   scripts/shell.sh              # full setup + Jupyter + subshell
#   scripts/shell.sh --no-jupyter # skip Jupyter, just the shell
#   scripts/shell.sh --refresh    # rebuild binaries + re-aggregate DB
#   scripts/shell.sh --help

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

REPORTING=".reporting"
VENV="$REPORTING/venv"
BIN_DIR="$REPORTING/bin"
NBDIR="$REPORTING/notebooks"
PIDFILE="$REPORTING/jupyter.pid"
LOGFILE="$REPORTING/jupyter.log"

NO_JUPYTER=0
REFRESH=0
for arg in "$@"; do
  case "$arg" in
    --no-jupyter) NO_JUPYTER=1 ;;
    --refresh)    REFRESH=1 ;;
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

# 1. Venv + aggregate + notebook seeds (idempotent; reuses report.sh's logic
#    so we have a single source of truth for reporting setup).
report_flags="--no-launch"
[[ "$REFRESH" -eq 1 ]] && report_flags="$report_flags --refresh"
scripts/report.sh $report_flags

# 2. Ensure the pure-Go resolver binary exists (sweep.sh + manual runs).
if [[ "$REFRESH" -eq 1 ]] || [[ ! -x resolver ]]; then
  echo "==> building resolver (pure-Go)..."
  go build -o resolver ./cmd/resolver
fi

# 3. Scoped bin dir — symlinks so PATH covers both binaries without
#    exposing the whole repo root.
mkdir -p "$BIN_DIR"
ln -sfn "../../resolver" "$BIN_DIR/resolver"
ln -sfn "../resolver-duckdb" "$BIN_DIR/resolver-duckdb"

# 4. Jupyter background + cleanup trap.
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
  [[ -n "${RCFILE:-}" ]] && rm -f "$RCFILE"
  [[ -n "${ZDOTDIR_TMP:-}" ]] && rm -rf "$ZDOTDIR_TMP"
  exit $code
}
trap cleanup EXIT INT TERM

# Clear stale pid from a prior run that exited without firing its trap,
# regardless of whether we start a new Jupyter this time — otherwise the
# cleanup trap would try to kill a stale (or worse, reused) pid.
if [[ -f "$PIDFILE" ]]; then
  oldpid=$(cat "$PIDFILE" 2>/dev/null || echo 0)
  if [[ "${oldpid:-0}" -gt 0 ]] && kill -0 "$oldpid" 2>/dev/null; then
    echo "==> killing stale jupyter (pid $oldpid) from prior run..."
    kill "$oldpid" 2>/dev/null || true
  fi
  rm -f "$PIDFILE"
fi

if [[ "$NO_JUPYTER" -eq 0 ]]; then
  echo "==> starting jupyter in background (log: $LOGFILE)..."
  : > "$LOGFILE"
  "$VENV/bin/jupyter" notebook --notebook-dir="$NBDIR" --no-browser > "$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"

  # Wait up to 6 s for the URL to appear.
  for _ in $(seq 1 30); do
    grep -qE 'http://[^[:space:]]+' "$LOGFILE" 2>/dev/null && break
    sleep 0.2
  done

  echo ""
  echo "==> jupyter URLs:"
  grep -oE 'http://[^[:space:]]+' "$LOGFILE" | sort -u | sed 's/^/    /' || \
    echo "    (URL not yet in log — tail $LOGFILE)"
  echo ""
fi

# 5. Interactive shell with project PATH + prompt marker.
#    Handles bash and zsh; falls back to bash otherwise so the prompt
#    override is reliable.
PROJECT_PATH="$REPO_ROOT/$BIN_DIR:$REPO_ROOT/scripts:$REPO_ROOT/$VENV/bin"

echo "==> entering resolver shell — type 'exit' to leave"
echo ""

case "${SHELL:-}" in
  *zsh)
    ZDOTDIR_TMP=$(mktemp -d -t resolver-shell.XXXXXX)
    cat > "$ZDOTDIR_TMP/.zshrc" <<EOF
[ -f "\$HOME/.zshrc" ] && . "\$HOME/.zshrc"
export RESOLVER_SHELL=1
export VIRTUAL_ENV="$REPO_ROOT/$VENV"
export PATH="$PROJECT_PATH:\$PATH"
PROMPT="%F{cyan}(resolver)%f \$PROMPT"
EOF
    ZDOTDIR="$ZDOTDIR_TMP" zsh -i || true
    ;;
  *)
    RCFILE=$(mktemp -t resolver-shell.XXXXXX)
    cat > "$RCFILE" <<EOF
[ -f "\$HOME/.bashrc" ] && . "\$HOME/.bashrc"
export RESOLVER_SHELL=1
export VIRTUAL_ENV="$REPO_ROOT/$VENV"
export PATH="$PROJECT_PATH:\$PATH"
PS1="\[\e[36m\](resolver)\[\e[0m\] \$PS1"
EOF
    bash --rcfile "$RCFILE" -i || true
    ;;
esac

# Trap handles cleanup.

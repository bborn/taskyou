#!/usr/bin/env bash
# Start the isolated FULL daemon (harness Tier 3), running real end-to-end executor
# spawning — alongside the live daemon, without touching it. Works because:
#   - the daemon PID lock is keyed to the instance DB dir (not a global path), so
#     both daemons can hold their own lock; and
#   - tmux is isolated via TMUX_TMPDIR (set in lib.sh), so the isolated daemon's
#     executor windows land on a private tmux server.
# Usage: ty-qa-daemon.sh          # start (backgrounded)
#        ty-qa-daemon.sh stop      # stop just the daemon
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PIDFILE="$TY_QA_ROOT/daemon.pid"
LOG="$TY_QA_ROOT/daemon.log"

if [[ "${1:-}" == "stop" ]]; then
  [[ -f "$PIDFILE" ]] && kill "$(cat "$PIDFILE")" 2>/dev/null && echo "stopped isolated daemon" || echo "not running"
  exit 0
fi

ty_qa_require_built

if [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
  echo "isolated daemon already running (pid $(cat "$PIDFILE"))"
  exit 0
fi

nohup env -u TMUX "$TY_BIN" daemon >"$LOG" 2>&1 &
sleep 2
if [[ -f "$PIDFILE" ]]; then
  echo "isolated daemon started (pid $(cat "$PIDFILE")); log: $LOG"
else
  echo "daemon did not write a PID file — check $LOG" >&2
  tail -5 "$LOG" >&2 || true
  exit 1
fi

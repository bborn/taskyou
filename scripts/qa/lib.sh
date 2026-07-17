#!/usr/bin/env bash
# Shared config + helpers for the ty TUI QA harness.
# Source this from the other ty-qa-*.sh scripts.

TY_QA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TY_REPO_ROOT="$(git -C "$TY_QA_DIR" rev-parse --show-toplevel)"

# Where the isolated instance lives, and its tmux session id. Override via env.
TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-qa}"
TY_QA_SID="${TY_QA_SID:-qa}"

# ty reads these — they fully isolate the DB and tmux sessions from the live instance.
export WORKTREE_DB_PATH="$TY_QA_ROOT/tasks.db"
export WORKTREE_SESSION_ID="$TY_QA_SID"

# Isolate ALL tmux usage onto a private tmux server via TMUX_TMPDIR. tmux's default
# socket lives under $TMUX_TMPDIR, so pointing it at the instance dir gives every
# tmux command here — AND ty's own internal `tmux` calls, which inherit this env
# (they're plain exec.Command with no cmd.Env) — a private server that never touches
# the user's live tmux. This is what lets a full isolated *daemon* (Tier 3) run
# alongside the live one: the daemon inherits TMUX_TMPDIR, so its executor windows
# land on the private server. We also clear $TMUX so a script or daemon launched
# from inside the user's live tmux still routes to the private server, not the
# ambient one. (Requires the daemon PID lock to be keyed to the DB dir — it is.)
export TMUX_TMPDIR="$TY_QA_ROOT/tmux"
mkdir -p "$TMUX_TMPDIR"
unset TMUX
# Resolve the real binary once instead of using `command tmux` inside the
# wrapper: macOS stock bash 3.2 has an errexit bug where a failing
# `command foo || true` still aborts a `set -e` script, which broke every
# ty-qa script at the first `tmux kill-session` against a not-yet-running
# QA server.
TY_QA_TMUX_BIN="$(command -v tmux)"
tmux() { env -u TMUX "$TY_QA_TMUX_BIN" "$@"; }

# Derived handles.
TY_BIN="${TY_BIN:-$TY_QA_ROOT/ty}"
TY_QA_PROJECTS="$TY_QA_ROOT/projects"
TY_QA_STATE="$TY_QA_ROOT/uistate.json"
TY_UI_SESSION="task-ui-$TY_QA_SID"
TY_DAEMON_SESSION="task-daemon-$TY_QA_SID"
TY_UI_PANE="$TY_UI_SESSION:tui"

# Run the isolated binary with the instance env. TMUX is cleared (see above) so
# ty's own tmux calls route to the private server.
ty() { env -u TMUX "$TY_BIN" "$@"; }

ty_qa_require_built() {
  if [[ ! -x "$TY_BIN" ]]; then
    echo "ty-qa: binary not built ($TY_BIN). Run scripts/qa/ty-qa-up.sh first." >&2
    exit 1
  fi
}

# --- Daemon freeze (for stable, presentation-clean screenshots) --------------
# Launching the ty TUI always ensures a daemon (ensureDaemonRunning in the TUI
# path). That daemon immediately picks up `queued` tasks and executes them —
# spawning worktrees and, in a throwaway env with no working agent, demoting them
# to `blocked`. That churn ruins seeded board/detail screenshots.
#
# `ty_qa_freeze_daemon` parks a harmless decoy process in the daemon pidfile
# (keyed to the DB dir, see getPidFilePath). ensureDaemonRunning sees a live pid
# and becomes a no-op, so no real executor ever starts and seeded `queued` tasks
# sit still in the In Progress column exactly as authored. Idempotent; the decoy
# is an orphaned `sleep` that outlives the short-lived shoot processes and is
# reaped by ty_qa_unfreeze_daemon / ty-qa-down.sh.
TY_QA_DAEMON_PID_FILE="$TY_QA_ROOT/daemon.pid"
TY_QA_DECOY_PID_FILE="$TY_QA_ROOT/.qa-decoy-daemon.pid"

ty_qa_freeze_daemon() {
  # Already frozen with a live decoy? Nothing to do.
  if [[ -f "$TY_QA_DECOY_PID_FILE" ]]; then
    local d; d="$(cat "$TY_QA_DECOY_PID_FILE" 2>/dev/null || true)"
    if [[ -n "${d:-}" ]] && kill -0 "$d" 2>/dev/null; then return 0; fi
  fi
  # Stop any real daemon bound to this instance before parking the decoy.
  if [[ -f "$TY_QA_DAEMON_PID_FILE" ]]; then
    local p; p="$(cat "$TY_QA_DAEMON_PID_FILE" 2>/dev/null || true)"
    [[ -n "${p:-}" ]] && kill "$p" 2>/dev/null || true
  fi
  mkdir -p "$TY_QA_ROOT"
  sleep 100000 &
  local decoy=$!
  disown "$decoy" 2>/dev/null || true
  echo "$decoy" > "$TY_QA_DAEMON_PID_FILE"
  echo "$decoy" > "$TY_QA_DECOY_PID_FILE"
  echo "ty-qa: daemon frozen (decoy pid $decoy) — queued tasks will not execute"
}

ty_qa_unfreeze_daemon() {
  if [[ -f "$TY_QA_DECOY_PID_FILE" ]]; then
    local d; d="$(cat "$TY_QA_DECOY_PID_FILE" 2>/dev/null || true)"
    [[ -n "${d:-}" ]] && kill "$d" 2>/dev/null || true
    rm -f "$TY_QA_DECOY_PID_FILE" "$TY_QA_DAEMON_PID_FILE"
    echo "ty-qa: daemon unfrozen"
  fi
}

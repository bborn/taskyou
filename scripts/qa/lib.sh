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

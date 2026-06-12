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

# Isolate ALL tmux usage onto a dedicated server socket so server-global state —
# the root S-arrow key bindings and join/break-pane the TUI issues — never touches
# the user's live tmux server. This function shadows the `tmux` binary for every
# script that sources lib.sh, so bare `tmux ...` calls route to the isolated socket.
# ty itself is launched *inside* this server (via `tmux new-session ... ty`), so it
# inherits $TMUX and routes its own bare `tmux` calls to the same socket. Use
# `command tmux` to reach the real binary on the default server.
export TY_QA_TMUX_SOCKET="${TY_QA_TMUX_SOCKET:-taskyou-qa-$TY_QA_SID}"
# Resolve the real binary once instead of using `command tmux` inside the
# wrapper: macOS stock bash 3.2 has an errexit bug where a failing
# `command foo || true` still aborts a `set -e` script, which broke every
# ty-qa script at the first `tmux kill-session` against a not-yet-running
# QA server.
TY_QA_TMUX_BIN="$(command -v tmux)"
tmux() { "$TY_QA_TMUX_BIN" -L "$TY_QA_TMUX_SOCKET" "$@"; }

# Derived handles.
TY_BIN="${TY_BIN:-$TY_QA_ROOT/ty}"
TY_QA_PROJECTS="$TY_QA_ROOT/projects"
TY_QA_STATE="$TY_QA_ROOT/uistate.json"
TY_UI_SESSION="task-ui-$TY_QA_SID"
TY_DAEMON_SESSION="task-daemon-$TY_QA_SID"
TY_UI_PANE="$TY_UI_SESSION:tui"

# Run the isolated binary with the instance env.
ty() { "$TY_BIN" "$@"; }

ty_qa_require_built() {
  if [[ ! -x "$TY_BIN" ]]; then
    echo "ty-qa: binary not built ($TY_BIN). Run scripts/qa/ty-qa-up.sh first." >&2
    exit 1
  fi
}

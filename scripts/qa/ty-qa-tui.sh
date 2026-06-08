#!/usr/bin/env bash
# Launch the real TUI for the isolated instance inside its own tmux session.
# The TUI must live in task-ui-<sid> because joinTmuxPane attaches agent panes there.
#
# Usage: ty-qa-tui.sh
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ty_qa_require_built

COLS="${TY_QA_COLS:-230}"
ROWS="${TY_QA_ROWS:-55}"

qtmux kill-session -t "$TY_UI_SESSION" 2>/dev/null || true

# Launch on the isolated qa tmux server (qtmux). Inside tmux, TMUX is set
# automatically, so ty runs the bubbletea TUI in-pane (runLocal) AND inherits
# this socket for all its own tmux calls — fully isolated from the live server.
# --debug-state-file dumps UI state as JSON on every update for assertions.
qtmux new-session -d -s "$TY_UI_SESSION" -x "$COLS" -y "$ROWS" -n tui \
  "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID='$WORKTREE_SESSION_ID' '$TY_BIN' --debug-state-file '$TY_QA_STATE'"

sleep 4
echo "==> TUI running: session '$TY_UI_SESSION', pane '$TY_UI_PANE' (tmux -L $TY_QA_TMUX_SOCKET)"
echo "    attach : tmux -L $TY_QA_TMUX_SOCKET attach -t $TY_UI_SESSION"
echo "    drive  : scripts/qa/ty-qa-key.sh <keys>"
echo "    state  : scripts/qa/ty-qa-state.sh"
echo "    view   : scripts/qa/ty-qa-capture.sh"

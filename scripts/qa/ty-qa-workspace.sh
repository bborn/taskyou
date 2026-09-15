#!/usr/bin/env bash
# Exercise real workspace tabs beside a fake agent, preserving a live shell job.
# Optional: TY_QA_WORKSPACE_SHOTS=1 records the actual tmux layout with VHS.
set -euo pipefail
export TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-qa-workspace}"
export TY_QA_SID="${TY_QA_SID:-workspace-panels}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"$DIR/ty-qa-up.sh" >/dev/null
source "$DIR/lib.sh"
trap '"$DIR/ty-qa-down.sh" >/dev/null 2>&1 || true' EXIT
"$DIR/ty-qa-freeze.sh" >/dev/null
"$DIR/ty-qa-seed.sh" >/dev/null
sqlite3 "$WORKTREE_DB_PATH" "UPDATE tasks SET status='backlog' WHERE status IN ('queued','processing');"
"$DIR/ty-qa-agent.sh" 1 storefront 'bash -c "printf \"Account theme preferences\\n\\nContrast checks passed. Ready for review.\\n\"; exec cat"' >/dev/null
python3 - "$WORKTREE_DB_PATH" <<'PY'
import sqlite3,sys,json,pathlib
c=sqlite3.connect(sys.argv[1]);root=pathlib.Path(c.execute('select worktree_path from tasks where id=1').fetchone()[0])
(root/'README.md').write_text('# Account theme preferences\n\nFollow the system theme by default and save each customer’s choice.\n\n## Review checklist\n\n- Keyboard focus stays visible\n- Both themes meet contrast requirements\n- Preferences survive sign-out\n')
pr=dict(number=42,title='Add account theme preferences',url='https://github.com/example/storefront/pull/42',state='OPEN',checkState='SUCCESS',mergeable='MERGEABLE',additions=128,deletions=24)
c.execute('update tasks set pr_info_json=? where id=1',(json.dumps(pr),));c.commit()
PY
"$DIR/ty-qa-tui.sh" >/dev/null
# Use exact pane IDs: the window's active pane changes when the workspace opens.
TUI=$(tmux list-panes -t "$TY_UI_SESSION:tui" -F '#{pane_id}' | head -1)
wait_for() { local n=0; until "$@"; do n=$((n+1)); [ "$n" -lt 50 ] || return 1; sleep .2; done; }
shows() { tmux capture-pane -p -t "$1" | grep -q "$2"; }
state_is() { [ "$(jq -r "$1" "$TY_QA_STATE")" = "$2" ]; }
check() { if "$@"; then echo "PASS $*"; else echo "FAIL $*"; exit 1; fi; }
tmux send-keys -t "$TUI" P
check wait_for state_is .dashboard.selected_task_id 1
tmux send-keys -t "$TUI" Enter
check wait_for state_is .detail.has_panes true
workspace() { tmux list-panes -t "$TY_UI_SESSION:tui" -F '#{pane_id} #{@ty_viewer}' | awk '$2=="workspace"{print $1}'; }
workspace_exists() { [ -n "$(workspace)" ]; }
request_workspace() { tmux send-keys -t "$TUI" w; workspace_exists; }
check wait_for request_workspace
PANEL=$(workspace)
check wait_for shows "$PANEL" Shell
SHELL_PANE=$(sqlite3 "$WORKTREE_DB_PATH" 'select shell_pane_id from tasks where id=1')
AGENT_PANE=$(sqlite3 "$WORKTREE_DB_PATH" 'select claude_pane_id from tasks where id=1')
SHELL_PID=$(tmux display-message -p -t "$SHELL_PANE" '#{pane_pid}')
tmux send-keys -t "$PANEL" -l 'sleep 120 & echo workspace-shell-ready'
tmux send-keys -t "$PANEL" Enter
check wait_for shows "$SHELL_PANE" workspace-shell-ready
tmux send-keys -t "$PANEL" M-t
check wait_for shows "$PANEL" 'Your workspace'
if [ "${TY_QA_WORKSPACE_SHOTS:-}" = 1 ]; then
  mkdir -p "$TY_QA_ROOT/shots"
  tmux set-window-option -t "$TY_UI_SESSION:tui" window-size smallest
  cat > "$TY_QA_ROOT/workspace.tape" <<TAPE
Output "$TY_QA_ROOT/shots/tui-workspace.gif"
Set Width 1600
Set Height 1200
Set FontSize 17
Set Padding 12
Set Shell "bash"
Hide
Type "env -u TMUX TMUX_TMPDIR='$TMUX_TMPDIR' tmux -L '$TASKYOU_TMUX_SOCKET' attach -t '$TY_UI_SESSION'"
Enter
Sleep 2s
Show
Sleep 2s
Screenshot "$TY_QA_ROOT/shots/tui-launcher.png"
Down
Enter
Sleep 2s
Screenshot "$TY_QA_ROOT/shots/tui-pr.png"
TAPE
  vhs "$TY_QA_ROOT/workspace.tape" >/dev/null
else
  tmux send-keys -t "$PANEL" Down Enter
fi
check wait_for shows "$PANEL" SUCCESS
# Open a file by keyboard and ensure canonical reopens don't duplicate it.
tmux send-keys -t "$PANEL" M-t
tmux send-keys -t "$PANEL" -l README.md
tmux send-keys -t "$PANEL" Enter
check wait_for shows "$PANEL" 'Review checklist'
"$TY_BIN" panel open 1 file README.md >/dev/null
check test "$(sqlite3 "$WORKTREE_DB_PATH" "select count(*) from task_panels where task_id=1 and provider_id='file';")" = 1
check test "$(tmux display-message -p -t "$SHELL_PANE" '#{pane_pid}')" = "$SHELL_PID"
check pgrep -P "$SHELL_PID" sleep
check test "$(tmux display-message -p -t "$AGENT_PANE" '#{window_name}')" = task-1
tmux send-keys -t "$TUI" Escape
check wait_for state_is .view dashboard
check test "$(tmux display-message -p -t "$SHELL_PANE" '#{pane_pid}')" = "$SHELL_PID"
check pgrep -P "$SHELL_PID" sleep
helper_gone() { [ -z "$(workspace)" ]; }
check wait_for helper_gone
echo 'Workspace QA passed: launcher, PR, Markdown, duplicate opens, shell job survival, and viewer cleanup.'

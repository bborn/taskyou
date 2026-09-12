#!/usr/bin/env bash
# QA of the in-place task view: opening a task shows its daemon window through a
# nested tmux client, and no pane ever leaves the daemon session.
#
# Runs on an isolated instance (its own DB and private tmux server) with fake
# agents (`cat`), so no real executor ever starts. Prints PASS/FAIL per check and
# exits non-zero if any check fails.
#
#   A. open a task, see the agent through the view, type to it, hide/show the
#      shell, switch tasks, go back to the board
#   B. reload the TUI while a task is open
#   C. a second TUI views the same task at the same time
#   D. ty running inside another tmux server (the user's own tmux)
#
# Usage: scripts/qa/ty-qa-views.sh      (TY_QA_ROOT defaults to /tmp/ty-qa-views)
#
# Run it after touching the detail view's pane code (internal/ui/detail_view.go),
# internal/tmuxctl, or how the executor creates task windows.
set -uo pipefail
export TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-qa-views}"
export TY_QA_SID="${TY_QA_SID:-views}"
export TY_QA_KEY_DELAY="${TY_QA_KEY_DELAY:-1.2}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0 FAIL=0
ok()  { echo "  PASS  $*"; PASS=$((PASS+1)); }
bad() { echo "  FAIL  $*"; FAIL=$((FAIL+1)); }
check() { local what=$1; shift; if "$@"; then ok "$what"; else bad "$what"; fi; }
wait_until() { local n=0; until "$@"; do n=$((n+1)); [ $n -ge 40 ] && return 1; sleep 0.25; done; }

"$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
"$DIR/ty-qa-up.sh" >/dev/null 2>&1 || { echo "ty-qa-up failed"; exit 1; }
source "$DIR/lib.sh"
[ "$TMUX_TMPDIR" = "$TY_QA_ROOT/tmux" ] || { echo "refusing: TMUX_TMPDIR is $TMUX_TMPDIR"; exit 1; }
teardown() {
  env -u TMUX "$TY_QA_TMUX_BIN" -L userown kill-server 2>/dev/null
  "$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
  env -u TMUX "$TY_QA_TMUX_BIN" -L "$TASKYOU_TMUX_SOCKET" kill-server 2>/dev/null
}
trap teardown EXIT

"$DIR/ty-qa-freeze.sh" >/dev/null
ty create "Wire up checkout webhooks" -p qa >/dev/null
ty create "Tighten the onboarding copy" -p qa >/dev/null
"$DIR/ty-qa-agent.sh" 1 qa 'bash -c "echo AGENT-1-READY; exec cat"' >/dev/null 2>&1
"$DIR/ty-qa-agent.sh" 2 qa 'bash -c "echo AGENT-2-READY; exec cat"' >/dev/null 2>&1

dbq() { sqlite3 "$WORKTREE_DB_PATH" "$1"; }
agent_of() { dbq "select claude_pane_id from tasks where id=$1"; }
shell_of() { dbq "select shell_pane_id from tasks where id=$1"; }
ALL_TASK_PANES="$(dbq "select claude_pane_id||' '||shell_pane_id from tasks order by id" | tr '\n' ' ')"

state_of() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); v=d; [v:=(v or {}).get(k) for k in sys.argv[2].split(".")]; print(v)' "$1" "$2" 2>/dev/null; }
state() { state_of "$TY_QA_STATE" "$1"; }
panes_in() { tmux list-panes -s -t "=$1" -F '#{pane_id}' 2>/dev/null; }
viewer_in() { tmux list-panes -t "$1" -F '#{pane_id} #{@ty_viewer}' 2>/dev/null | awk 'NF==2{print $1; exit}'; }
view_of() { [ -n "$1" ] && tmux show-options -pqv -t "$1" @ty_viewer 2>/dev/null; }
view_sessions() { tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^ty-view-'; }
window_of() { tmux display-message -p -t "$1" '#{window_name}' 2>/dev/null; }
has_client() { [ -n "$1" ] && [ -n "$(tmux list-clients -t "=$1" 2>/dev/null)" ]; }
no_views() { [ -z "$(view_sessions)" ]; }
state_is()  { [ "$(state_of "$1" "$2")" = "$3" ]; }
state_not() { [ "$(state_of "$1" "$2")" != "$3" ]; }
window_is() { [ "$(window_of "$1")" = "$2" ]; }
no_viewer_in() { [ -z "$(viewer_in "$1")" ]; }
# The window the TUI's current view shows, read fresh every time.
view_shows() { [ "$(tmux display-message -p -t "$(view_of "$(viewer_in "$TY_UI_PANE")"):" '#{window_name}' 2>/dev/null)" = "$1" ]; }
session_gone() { ! tmux has-session -t "=$1" 2>/dev/null; }
shows() { tmux capture-pane -p -t "$1" 2>/dev/null | grep -q "$2"; }
# Every task pane is in the daemon session, and none is in a TUI session.
nothing_in_ui() {
  local p s; for p in $ALL_TASK_PANES; do
    panes_in "$TY_DAEMON_SESSION" | grep -qx "$p" || return 1
    for s in $(tmux list-sessions -F '#{session_name}' | grep '^task-ui-'); do panes_in "$s" | grep -qx "$p" && return 1; done
  done; return 0; }

echo "== A. open, see the agent, type to it, hide/show the shell, switch, back"
"$DIR/ty-qa-tui.sh" >/dev/null 2>&1
"$DIR/ty-qa-key.sh" P
FIRST=$(state dashboard.selected_task_id)
"$DIR/ty-qa-key.sh" Enter
wait_until state_is "$TY_QA_STATE" detail.has_panes True
check "detail view open on task $FIRST" [ "$(state view)" = detail ]
check "no pane error" [ "$(state detail.pane_error)" = None ]
V=$(viewer_in "$TY_UI_PANE"); VS=$(view_of "$V")
check "a marked view pane sits under the TUI" [ -n "$VS" ]
check "a client is attached to the view session" wait_until has_client "$VS"
check "the view shows task $FIRST's window" [ "$(tmux display-message -p -t "$VS:" '#{window_name}')" = "task-$FIRST" ]
check "the agent's output shows through the view" wait_until shows "$V" "AGENT-$FIRST-READY"
tmux select-pane -t "$(agent_of "$FIRST")"   # as a click into the agent would
tmux send-keys -t "$V" "typed-through-the-view" Enter
check "keys typed into the view reach the agent" wait_until shows "$(agent_of "$FIRST")" typed-through-the-view
check "nothing moved: task panes stay in the daemon session" nothing_in_ui
"$DIR/ty-qa-key.sh" '\'
check "a hidden shell waits in the daemon session" wait_until window_is "$(shell_of "$FIRST")" "_hidden_shell_$FIRST"
check "...and not in the TUI's" nothing_in_ui
"$DIR/ty-qa-key.sh" '\'
check "a shown shell is back in the task window" wait_until window_is "$(shell_of "$FIRST")" "task-$FIRST"
"$DIR/ty-qa-key.sh" Down
wait_until state_not "$TY_QA_STATE" detail.task_id "$FIRST"
SECOND=$(state detail.task_id)
check "switching tasks points a view at task $SECOND" wait_until view_shows "task-$SECOND"
check "the previous view session is gone" wait_until session_gone "$VS"
"$DIR/ty-qa-key.sh" Escape
check "Esc returns to the board" wait_until state_is "$TY_QA_STATE" view dashboard
check "no view pane left under the TUI" wait_until no_viewer_in "$TY_UI_PANE"
check "no view sessions left" wait_until no_views
check "all task panes still in the daemon session" nothing_in_ui

echo "== B. reload while a task is open"
"$DIR/ty-qa-key.sh" Enter
wait_until state_is "$TY_QA_STATE" detail.has_panes True
VSB=$(view_of "$(viewer_in "$TY_UI_PANE")")
PID_BEFORE=$(tmux display-message -p -t "$TY_UI_PANE" '#{pane_pid}')
dbq "insert or replace into settings(key, value) values('tui_reload_request', 'qa-$RANDOM')"
reloaded() { local c; c=$(view_of "$(viewer_in "$TY_UI_PANE")"); [ -n "$c" ] && [ "$c" != "$VSB" ]; }
check "the TUI reloads and comes back with a new view" wait_until reloaded
check "the pre-reload view session is gone" wait_until session_gone "$VSB"
check "the reload re-exec'd in place (same pane process)" [ "$(tmux display-message -p -t "$TY_UI_PANE" '#{pane_pid}')" = "$PID_BEFORE" ]
check "state says detail after the reload" wait_until state_is "$TY_QA_STATE" view detail
check "nothing stranded by the reload" nothing_in_ui

echo "== C. a second TUI views the same task"
S2="task-ui-${TY_QA_SID}2"; STATE2="$TY_QA_ROOT/uistate2.json"
tmux new-session -d -s "$S2" -x 230 -y 55 -n tui -c "$TY_QA_ROOT" \
  "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID=${TY_QA_SID}2 TASKYOU_TMUX_SOCKET='$TASKYOU_TMUX_SOCKET' '$TY_BIN' --debug-state-file '$STATE2'"
wait_until state_is "$STATE2" view dashboard
tmux send-keys -t "$S2:tui" P
wait_until state_not "$STATE2" dashboard.selected_task_id None
T2=$(state_of "$STATE2" dashboard.selected_task_id)
tmux send-keys -t "$S2:tui" Enter
check "the second TUI opens task $T2 with a view of its own" wait_until state_is "$STATE2" detail.has_panes True
check "...without a 'busy elsewhere' error" [ "$(state_of "$STATE2" detail.pane_error)" = None ]
check "...while the first TUI's view is still attached" has_client "$(view_of "$(viewer_in "$TY_UI_PANE")")"
tmux send-keys -t "$S2:tui" Escape
wait_until state_is "$STATE2" view dashboard
check "closing the second view leaves the first attached" has_client "$(view_of "$(viewer_in "$TY_UI_PANE")")"
tmux kill-session -t "=$S2"

echo "== D. ty inside the user's own tmux (a different server)"
U() { env -u TMUX "$TY_QA_TMUX_BIN" -L userown "$@"; }
USERPANE=$(U new-session -d -s mywork -x 230 -y 55 -P -F '#{pane_id}' "sleep 600")
TYPANE=$(U split-window -v -t "$USERPANE" -c "$TY_QA_ROOT" -P -F '#{pane_id}' \
  "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID=${TY_QA_SID}3 TASKYOU_TMUX_SOCKET='$TASKYOU_TMUX_SOCKET' '$TY_BIN' --debug-state-file '$TY_QA_ROOT/uistate3.json'")
STATE3="$TY_QA_ROOT/uistate3.json"
wait_until state_is "$STATE3" view dashboard
U send-keys -t "$TYPANE" P
wait_until state_not "$STATE3" dashboard.selected_task_id None
U send-keys -t "$TYPANE" Enter
check "ty in the user's tmux opens a task view" wait_until state_is "$STATE3" detail.has_panes True
UV=$(U list-panes -t "$TYPANE" -F '#{pane_id} #{@ty_viewer}' | awk 'NF==2{print $1; exit}')
UVS=$(U show-options -pqv -t "$UV" @ty_viewer 2>/dev/null)
check "the view pane is in the user's window" [ -n "$UV" ]
check "...attached across servers to the private one" wait_until has_client "$UVS"
check "the user's own pane is untouched" [ "$(U display-message -p -t "$USERPANE" '#{pane_id}')" = "$USERPANE" ]
check "task panes never left the private daemon session" nothing_in_ui
U send-keys -t "$TYPANE" Escape
no_user_viewer() { [ -z "$(U list-panes -t "$TYPANE" -F '#{pane_id} #{@ty_viewer}' | awk 'NF==2')" ]; }
check "Esc removes the view pane from the user's window" wait_until no_user_viewer
check "...and its view session" wait_until session_gone "$UVS"
check "the user's own pane survived" [ "$(U display-message -p -t "$USERPANE" '#{pane_id}')" = "$USERPANE" ]

echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

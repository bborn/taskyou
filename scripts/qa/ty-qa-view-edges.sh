#!/usr/bin/env bash
# Edge cases of the in-place task view, beyond ty-qa-views.sh: the moments a
# view is most likely to leave something behind or show the wrong task.
#
#   E1. twelve Down/Up presses in a row inside a task
#   E2. a finished task's window closes while it is viewed: nothing restarts
#   E2b. a blocked task's window closes while it is viewed, as the idle sweep
#       leaves it: it is not restarted, and the view says its session closed
#   E3. ctrl+c while a task is open
#   E4. `ty open 3` typed outside tmux, in a 190x48 terminal
#   E5. a running task's window closes while it is viewed: ty waits for the
#       daemon's executor, then starts the agent itself and shows it again
#   E6. the TUI is killed (kill -9) while a task is open
#
# Isolated instance (its own DB and private tmux server) with fake agents. The
# `claude` on PATH is a fake as well and tasks use a throwaway Claude config
# dir, so when E5 has ty start an agent, no real Claude session starts and
# ~/.claude is not touched.
#
# Usage: scripts/qa/ty-qa-view-edges.sh   (TY_QA_ROOT defaults to /tmp/ty-qa-edges)
# Takes about four minutes: E5 sits out the TUI's 60-second wait for the daemon.
set -uo pipefail
export TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-qa-edges}"
export TY_QA_SID="${TY_QA_SID:-edges}"
export TY_QA_KEY_DELAY="${TY_QA_KEY_DELAY:-1.2}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0 FAIL=0
ok()   { echo "  PASS  $*"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL  $*"; FAIL=$((FAIL+1)); }
note() { echo "  NOTE  $*"; }
check() { local what=$1; shift; if "$@"; then ok "$what"; else bad "$what"; fi; }
# wait_until [-n polls] cmd...: every 0.25s, 40 polls (10s) unless -n says otherwise.
wait_until() { local n=0 max=40; if [ "$1" = -n ]; then max=$2; shift 2; fi
  until "$@"; do n=$((n+1)); [ $n -ge $max ] && return 1; sleep 0.25; done; }

# The fake claude goes first on PATH before any tmux server starts, so every
# pane and every ty inherits it.
FAKEBIN="$TY_QA_ROOT.fakebin"
rm -rf "$FAKEBIN"; mkdir -p "$FAKEBIN"
printf '#!/bin/sh\necho FAKE-CLAUDE-STARTED\nexec cat\n' > "$FAKEBIN/claude"
chmod +x "$FAKEBIN/claude"
export PATH="$FAKEBIN:$PATH"

"$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
"$DIR/ty-qa-up.sh" >/dev/null 2>&1 || { echo "ty-qa-up failed"; exit 1; }
source "$DIR/lib.sh"
[ "$TMUX_TMPDIR" = "$TY_QA_ROOT/tmux" ] || { echo "refusing: TMUX_TMPDIR is $TMUX_TMPDIR"; exit 1; }
OPENPID=""
teardown() {
  exec 3>&- 2>/dev/null
  [ -n "$OPENPID" ] && kill "$OPENPID" 2>/dev/null
  "$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
  env -u TMUX "$TY_QA_TMUX_BIN" -L "$TASKYOU_TMUX_SOCKET" kill-server 2>/dev/null
  pkill -f "$TY_QA_ROOT" 2>/dev/null
  rm -rf "$FAKEBIN"
}
trap teardown EXIT

"$DIR/ty-qa-freeze.sh" >/dev/null
dbq() { sqlite3 "$WORKTREE_DB_PATH" "$1"; }
CLAUDE_DIR="$TY_QA_ROOT/claude-config"; mkdir -p "$CLAUDE_DIR"
dbq "UPDATE projects SET claude_config_dir='$CLAUDE_DIR'"
for t in "Verify Stripe webhook signatures" "Tighten the onboarding copy" "Cache the catalog sync" "Retry failed payouts"; do
  ty create "$t" -p qa >/dev/null
done
dbq "UPDATE tasks SET claude_config_dir='$CLAUDE_DIR'"
for id in 1 2 3 4; do "$DIR/ty-qa-agent.sh" $id qa "bash -c \"echo AGENT-$id; exec cat\"" >/dev/null 2>&1; done
ORIGINAL_PANES="$(dbq "select claude_pane_id||' '||shell_pane_id from tasks order by id" | tr '\n' ' ')"

state_of() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); v=d; [v:=(v or {}).get(k) for k in sys.argv[2].split(".")]; print(v)' "$1" "$2" 2>/dev/null; }
state_is() { [ "$(state_of "$TY_QA_STATE" "$1")" = "$2" ]; }
panes_in() { tmux list-panes -s -t "=$1" -F '#{pane_id}' 2>/dev/null; }
viewers_in() { tmux list-panes -t "$1" -F '#{pane_id} #{@ty_viewer}' 2>/dev/null | awk 'NF==2{print $1}'; }
view_of() { [ -n "$1" ] && tmux show-options -pqv -t "$1" @ty_viewer 2>/dev/null; }
view_sessions() { tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^ty-view-'; }
count() { if [ -z "$1" ]; then echo 0; else echo "$1" | wc -l | tr -d ' '; fi; }
# The daemon window a TUI pane's view shows; empty without a view.
view_window() { local v; v=$(viewers_in "$1" | head -1); [ -n "$v" ] && tmux display-message -p -t "$(view_of "$v"):" '#{window_name}' 2>/dev/null; }
has_window() { tmux list-windows -t "=$TY_DAEMON_SESSION" -F '#{window_name}' 2>/dev/null | grep -qx "task-$1"; }
no_window() { ! has_window "$1"; }
no_views() { [ -z "$(view_sessions)" ]; }
session_gone() { ! tmux has-session -t "=$1" 2>/dev/null; }
has_client() { [ -n "$(tmux list-clients -t "=$1" 2>/dev/null)" ]; }
same_dir() { [ "$(cd "$1" 2>/dev/null && pwd -P)" = "$(cd "$2" 2>/dev/null && pwd -P)" ]; }
# alive 1,2: every agent and shell the DB records for those tasks is in the daemon session.
alive() { local p; for p in $(dbq "select claude_pane_id||' '||shell_pane_id from tasks where id in ($1)"); do
  panes_in "$TY_DAEMON_SESSION" | grep -qx "$p" || return 1; done; }
# No task pane is in any TUI session.
nothing_in_ui() { local p s; for p in $ORIGINAL_PANES; do for s in $(tmux list-sessions -F '#{session_name}' | grep '^task-ui-'); do
  panes_in "$s" | grep -qx "$p" && return 1; done; done; return 0; }
# select_task ID: move the board's In Progress selection to task ID.
select_task() { local i
  "$DIR/ty-qa-key.sh" P
  for i in 1 2 3 4; do state_is dashboard.selected_task_id "$1" && return 0; "$DIR/ty-qa-key.sh" Down; done
  for i in 1 2 3 4 5 6 7; do state_is dashboard.selected_task_id "$1" && return 0; "$DIR/ty-qa-key.sh" Up; done
  state_is dashboard.selected_task_id "$1"; }

echo "== E1. twelve Down/Up presses in a row inside a task"
"$DIR/ty-qa-tui.sh" >/dev/null 2>&1
"$DIR/ty-qa-key.sh" P Enter
wait_until state_is detail.has_panes True || bad "the first task did not open"
tmux send-keys -t "$TY_UI_PANE" Down Down Up Down Down Up Up Down Down Down Up Down
one_view() { state_is detail.has_panes True && [ "$(count "$(viewers_in "$TY_UI_PANE")")" = 1 ]; }
sleep 6
wait_until -n 60 one_view
check "one view pane after the burst (found $(count "$(viewers_in "$TY_UI_PANE")"))" one_view
WANT=$(state_of "$TY_QA_STATE" detail.task_id)
check "the view shows the task the TUI has open (task-$WANT; shows $(view_window "$TY_UI_PANE"))" [ "$(view_window "$TY_UI_PANE")" = "task-$WANT" ]
one_session() { [ "$(count "$(view_sessions)")" = 1 ]; }
check "exactly one view session" wait_until one_session
check "every agent alive" alive 1,2,3,4
check "no task pane in a TUI session" nothing_in_ui

echo "== E2. a finished task's window closes while it is viewed"
tmux send-keys -t "$TY_UI_PANE" Escape; sleep 2
select_task 4 || bad "could not select task 4"
"$DIR/ty-qa-key.sh" Enter
opened4() { state_is detail.task_id 4 && state_is detail.has_panes True; }
check "task 4 opens" wait_until opened4
dbq "UPDATE tasks SET status='done' WHERE id=4"   # its agent finished
sleep 3                                            # the TUI reads it
tmux kill-window -t "$TY_DAEMON_SESSION:task-4"
sleep 12                                           # several health checks
check "nothing restarted the finished task" no_window 4
check "no view pane left in the TUI (found $(count "$(viewers_in "$TY_UI_PANE")"))" [ "$(count "$(viewers_in "$TY_UI_PANE")")" = 0 ]
check "no view session left" no_views
check "every other agent alive" alive 1,2,3

echo "== E2b. a blocked task's window closes while it is viewed (the idle sweep does this)"
tmux send-keys -t "$TY_UI_PANE" Escape; sleep 2
select_task 2 || bad "could not select task 2"
"$DIR/ty-qa-key.sh" Enter
opened2() { state_is detail.task_id 2 && state_is detail.has_panes True; }
check "task 2 opens" wait_until opened2
# What the sweep leaves behind: a task parked for hours, its window killed.
dbq "UPDATE tasks SET status='blocked', completed_at=datetime('now','-7 hours') WHERE id=2"
sleep 3
tmux kill-window -t "$TY_DAEMON_SESSION:task-2"
sleep 12                                           # several health checks
check "the blocked task is not restarted" no_window 2
says_closed() { tmux capture-pane -p -t "$TY_UI_PANE" | grep -q 'Session closed'; }
check "the view says its session closed" says_closed
check "no view session left" no_views
check "every other agent alive" alive 1,3

echo "== E3. ctrl+c while a task is open"
tmux send-keys -t "$TY_UI_PANE" Escape; sleep 2
select_task 1 || bad "could not select task 1"
"$DIR/ty-qa-key.sh" Enter
wait_until state_is detail.has_panes True || bad "task 1 did not open"
tmux send-keys -t "$TY_UI_PANE" C-c
check "the TUI's session goes away" wait_until session_gone "$TY_UI_SESSION"
check "its view session goes too" wait_until no_views
check "every agent alive after the quit" alive 1,3

echo "== E4. ty open 3 typed outside tmux"
FIFO="$TY_QA_ROOT/open.fifo"; rm -f "$FIFO"; mkfifo "$FIFO"
cat "$FIFO" | script -q "$TY_QA_ROOT/open.log" sh -c "stty rows 48 cols 190; cd '$TY_QA_ROOT'; exec env -u TMUX -u TMUX_PANE '$TY_BIN' open 3" >/dev/null 2>&1 &
OPENPID=$!
exec 3>"$FIFO"
S3="$TY_UI_SESSION"
check "ty open made its own session on the private server and attached" wait_until has_client "$S3"
note "sizes: $(tmux display-message -p -t "=$S3:" 'window=#{window_width}x#{window_height} client=#{client_width}x#{client_height} status=#{status}' 2>/dev/null)"
WW=$(tmux display-message -p -t "=$S3:" '#{window_width}'); WH=$(tmux display-message -p -t "=$S3:" '#{window_height}')
check "...sized to the terminal: 190 wide (got $WW), 48 rows less the status line (got $WH)" [ "$WW" = 190 ] && [ "$WH" = 47 ]
T3=$(tmux list-panes -t "=$S3:" -F '#{pane_id} #{pane_current_command}' | awk '$2=="ty"{print $1; exit}')
shows3() { [ "$(view_window "$T3")" = task-3 ]; }
check "it lands in task 3's view" wait_until shows3
VS3=$(view_of "$(viewers_in "$T3" | head -1)")

echo "== E5. a running task's window closes while it is viewed"
KILLED_AT=$(date +%s)
tmux kill-window -t "$TY_DAEMON_SESSION:task-3"
check "the view ends instead of showing another task" wait_until session_gone "$VS3"
sleep 10
check "ty waits for the daemon's executor first (no agent of its own after 10s)" no_window 3
if wait_until -n 400 shows3; then   # up to 100s: the 60s wait, then the start
  ok "ty started the agent itself after $(( $(date +%s) - KILLED_AT ))s and shows it again"
  AGENT3=$(dbq "select claude_pane_id from tasks where id=3")
  fake_runs() { tmux capture-pane -p -t "$AGENT3" 2>/dev/null | grep -q FAKE-CLAUDE-STARTED; }
  check "...running the fake claude" wait_until fake_runs
  check "...in task 3's worktree" same_dir "$(tmux display-message -p -t "$AGENT3" '#{pane_current_path}')" "$(dbq "select worktree_path from tasks where id=3")"
else
  bad "task 3 was not shown again within 100s; its screen:"
  tmux capture-pane -p -t "$T3" | grep -v '^[[:space:]]*$' | head -8 | sed 's/^/        /'
fi
check "every other agent alive" alive 1
check "exactly one task-3 window" [ "$(tmux list-windows -t "=$TY_DAEMON_SESSION" -F '#{window_name}' | grep -cx task-3)" = 1 ]

echo "== E6. the TUI is killed (kill -9) while a task is open"
TYPID=$(tmux display-message -p -t "$T3" '#{pane_pid}')
TYPROC=$(pgrep -P "$TYPID" -f "$TY_BIN" | head -1); [ -z "$TYPROC" ] && TYPROC=$TYPID
kill -9 "$TYPROC" 2>/dev/null; sleep 3
check "every agent alive after the crash" alive 1,3
check "no task pane stranded in a TUI session" nothing_in_ui
left=$(tmux list-panes -s -t "=$S3" -F '#{pane_id} #{pane_current_command} viewer=#{@ty_viewer}' 2>/dev/null | tr '\n' ';')
check "nothing of the crashed TUI is left${left:+ (left: $left)}" [ -z "$left" ]
check "no view session left" wait_until no_views

echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

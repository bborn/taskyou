#!/usr/bin/env bash
# Real-key QA of the task view: a tmux client attached to the TUI's session is
# fed the exact bytes a terminal sends, so keys go through every layer a user's
# keys go through (client, TUI session, nested view client, agent pane).
#
#   - Shift+arrows: details -> agent -> shell -> details, both directions, and
#     details <-> agent with the shell hidden
#   - typing through the view reaches the agent
#   - Enter, Esc+Enter, CSI-u and modifyOtherKeys Shift+Enter arrive exactly as
#     they do through a direct, single-layer attach
#   - the mouse wheel over the agent scrolls the agent
#
# Isolated instance (its own DB and private tmux server) with fake agents
# (`cat -v`, which prints what it receives). Exits non-zero on any failure.
#
# Usage: scripts/qa/ty-qa-view-keys.sh   (TY_QA_ROOT defaults to /tmp/ty-qa-keys)
set -uo pipefail
export TY_QA_ROOT="${TY_QA_ROOT:-/tmp/ty-qa-keys}"
export TY_QA_SID="${TY_QA_SID:-keys}"
export TY_QA_KEY_DELAY="${TY_QA_KEY_DELAY:-1.2}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0 FAIL=0
ok()  { echo "  PASS  $*"; PASS=$((PASS+1)); }
bad() { echo "  FAIL  $*"; FAIL=$((FAIL+1)); }
wait_until() { local n=0; until "$@"; do n=$((n+1)); [ $n -ge 16 ] && return 1; sleep 0.25; done; }

"$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
"$DIR/ty-qa-up.sh" >/dev/null 2>&1 || { echo "ty-qa-up failed"; exit 1; }
source "$DIR/lib.sh"
[ "$TMUX_TMPDIR" = "$TY_QA_ROOT/tmux" ] || { echo "refusing: TMUX_TMPDIR is $TMUX_TMPDIR"; exit 1; }
CLIENTS=()
teardown() {
  exec 3>&- 4>&- 2>/dev/null
  for p in "${CLIENTS[@]}"; do kill "$p" 2>/dev/null; done
  "$DIR/ty-qa-down.sh" --purge >/dev/null 2>&1
  env -u TMUX "$TY_QA_TMUX_BIN" -L "$TASKYOU_TMUX_SOCKET" kill-server 2>/dev/null
}
trap teardown EXIT

"$DIR/ty-qa-freeze.sh" >/dev/null
ty create "Key check through the view" -p qa >/dev/null
ty create "Direct attach baseline" -p qa >/dev/null
"$DIR/ty-qa-agent.sh" 1 qa 'bash -c "echo READY; exec cat -v"' >/dev/null 2>&1
"$DIR/ty-qa-agent.sh" 2 qa 'bash -c "echo READY; exec cat -v"' >/dev/null 2>&1
dbq() { sqlite3 "$WORKTREE_DB_PATH" "$1"; }
A1=$(dbq "select claude_pane_id from tasks where id=1"); S1=$(dbq "select shell_pane_id from tasks where id=1")
A2=$(dbq "select claude_pane_id from tasks where id=2")
state_of() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); v=d; [v:=(v or {}).get(k) for k in sys.argv[2].split(".")]; print(v)' "$1" "$2" 2>/dev/null; }
state_is() { [ "$(state_of "$TY_QA_STATE" "$1")" = "$2" ]; }
has_client() { [ -n "$(tmux list-clients -t "=$1" 2>/dev/null)" ]; }

# attach_client FIFO SESSION FD: a 200x50 client attached to SESSION that types
# whatever is written to file descriptor FD, like a real terminal. A pipe, not
# the FIFO itself, feeds `script`: it cannot take a FIFO as its stdin.
attach_client() {
  local fifo=$1 session=$2 fd=$3
  rm -f "$fifo"; mkfifo "$fifo"
  cat "$fifo" | script -q "$fifo.log" sh -c "stty rows 50 cols 200; exec env -u TMUX '$TY_QA_TMUX_BIN' -L '$TASKYOU_TMUX_SOCKET' attach -t '$session'" >/dev/null 2>&1 &
  CLIENTS+=($!)
  eval "exec $fd>'$fifo'"
  wait_until has_client "$session"
}

"$DIR/ty-qa-tui.sh" >/dev/null 2>&1
TUI=$(tmux display-message -p -t "$TY_UI_PANE" '#{pane_id}')
attach_client "$TY_QA_ROOT/ui.fifo" "$TY_UI_SESSION" 3 || { echo "client did not attach"; exit 1; }
type_ui() { printf "$1" >&3; }
"$DIR/ty-qa-key.sh" P
[ "$(state_of "$TY_QA_STATE" dashboard.selected_task_id)" = 1 ] || "$DIR/ty-qa-key.sh" Down
"$DIR/ty-qa-key.sh" Enter
wait_until state_is detail.has_panes True || { echo "the task view did not open"; exit 1; }
sleep 1
V=$(tmux list-panes -t "$TY_UI_PANE" -F '#{pane_id} #{@ty_viewer}' | awk 'NF==2{print $1; exit}')
VS=$(tmux show-options -pqv -t "$V" @ty_viewer)
echo "TUI=$TUI  view pane=$V ($VS)  agent=$A1  shell=$S1"

# where: which of details / agent / shell has focus for the attached client.
where() {
  local o; o=$(tmux display-message -p -t "$TY_UI_SESSION:" '#{pane_id}')
  if [ "$o" = "$V" ]; then
    case "$(tmux display-message -p -t "$VS:" '#{pane_id}')" in "$A1") echo agent;; "$S1") echo shell;; *) echo "view:?";; esac
  elif [ "$o" = "$TUI" ]; then echo tui; else echo "other:$o"; fi
}
at() { [ "$(where)" = "$1" ]; }
keyseq() { case $1 in S-Down) echo '\033[1;2B';; S-Up) echo '\033[1;2A';; S-Right) echo '\033[1;2C';; S-Left) echo '\033[1;2D';; esac; }
step() { # key, expected focus
  type_ui "$(keyseq "$1")"
  if wait_until at "$2"; then ok "$1 -> $2"; else bad "$1 -> expected $2, at $(where)"; fi
}

echo "== Shift+arrows: the ring details -> agent -> shell -> details"
tmux select-pane -t "$TUI"; wait_until at tui
for k in S-Down S-Right; do step $k agent; step $k shell; step $k tui; done
for k in S-Up S-Left; do step $k shell; step $k agent; step $k tui; done
echo "== Shift+arrows with the shell hidden: details <-> agent"
"$DIR/ty-qa-key.sh" '\'
wait_until sh -c "[ \"\$(tmux -L $TASKYOU_TMUX_SOCKET display-message -p -t $S1 '#{window_name}')\" = _hidden_shell_1 ]"
step S-Down agent; step S-Down tui; step S-Up agent; step S-Up tui
"$DIR/ty-qa-key.sh" '\'
sleep 1

echo "== Typing through the view reaches the agent"
tmux select-pane -t "$TUI"; step S-Down agent
type_ui 'typed-through-the-view\r'
if wait_until sh -c "tmux -L $TASKYOU_TMUX_SOCKET capture-pane -p -t $A1 | grep -q typed-through-the-view"; then ok "keys reach the agent"; else bad "typed text did not reach the agent"; fi

echo "== Shift+Enter encodings: through the view vs. a direct attach"
# Before the view, the agent's pane sat directly in the attached client's
# window: one tmux layer. Task 2, attached directly, is that baseline.
tmux new-session -d -s direct -t "$TY_DAEMON_SESSION"
tmux select-window -t "direct:task-2"; tmux select-pane -t "$A2"
attach_client "$TY_QA_ROOT/direct.fifo" direct 4 || bad "baseline client did not attach"
type_direct() { printf "$1" >&4; }
NAMES=("Enter" "Esc+Enter (Option/Shift+Enter)" "CSI-u Shift+Enter" "modifyOtherKeys Shift+Enter")
SEQS=('\r' '\033\r' '\033[13;2u' '\033[27;2;13~')
for i in 0 1 2 3; do
  type_ui     "<<$i>>${SEQS[$i]}<</$i>>\r"; sleep 0.4
  type_direct "<<$i>>${SEQS[$i]}<</$i>>\r"; sleep 0.4
done
sleep 1
received() { # pane, sample index -> what cat -v printed between the markers
  tmux capture-pane -p -J -t "$1" | python3 -c '
import sys, re
text = sys.stdin.read().replace("\n", "⏎")
m = re.search(r"<<%s>>(.*?)<</%s>>" % (sys.argv[1], sys.argv[1]), text)
print(m.group(1) if m else "<nothing>")' "$2"
}
for i in 0 1 2 3; do
  via_view=$(received "$A1" $i); direct=$(received "$A2" $i)
  if [ "$via_view" = "$direct" ]; then ok "${NAMES[$i]}: same as a direct attach ($via_view)"
  else bad "${NAMES[$i]}: view delivered [$via_view], direct delivered [$direct]"; fi
done

echo "== Mouse wheel over the agent scrolls the agent"
# The view sits below the details; aim at the agent's side of it.
top=$(tmux display-message -p -t "$V" '#{pane_top}'); left=$(tmux display-message -p -t "$V" '#{pane_left}')
row=$((top + 6)); col=$((left + 10))
type_ui "\033[<64;${col};${row}M"
if wait_until sh -c "[ \"\$(tmux -L $TASKYOU_TMUX_SOCKET display-message -p -t $A1 '#{pane_in_mode}')\" = 1 ]"; then ok "wheel up puts the agent's pane in scroll mode"
else bad "wheel up did not scroll the agent (in_mode=$(tmux display-message -p -t "$A1" '#{pane_in_mode}'))"; fi
type_ui 'q'

echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

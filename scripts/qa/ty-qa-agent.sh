#!/usr/bin/env bash
# Attach a LIVE agent window to an existing task WITHOUT the daemon, and point the
# task's DB row at it. This lets you QA detail-view / pane behaviour (the real
# joinTmuxPane, "continue working" nudges, the shell pane) without standing up a
# second daemon — ty's daemon lock is global, so a second one can't run beside
# the live instance.
#
# Usage: ty-qa-agent.sh <task-id> [project] [agent-cmd]
#   default agent-cmd launches Claude with a trivial "say READY and wait" prompt.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ty_qa_require_built
command -v sqlite3 >/dev/null || { echo "sqlite3 required" >&2; exit 1; }

TASK_ID="${1:?usage: ty-qa-agent.sh <task-id> [project] [agent-cmd]}"
PROJECT="${2:-qa}"
AGENT_CMD="${3:-claude --dangerously-skip-permissions \"Say the single word READY and then wait. Do nothing else.\"}"
PROJECT_PATH="$TY_QA_PROJECTS/$PROJECT"
WT="$PROJECT_PATH/.task-worktrees/$TASK_ID-qa"
WIN="$TY_DAEMON_SESSION:task-$TASK_ID"

echo "==> worktree $WT"
git -C "$PROJECT_PATH" worktree remove --force "$WT" 2>/dev/null || true
git -C "$PROJECT_PATH" worktree add -q "$WT" -b "qa-$TASK_ID" 2>/dev/null \
  || git -C "$PROJECT_PATH" worktree add -q "$WT"

tmux has-session -t "$TY_DAEMON_SESSION" 2>/dev/null \
  || tmux new-session -d -s "$TY_DAEMON_SESSION" -n _placeholder "tail -f /dev/null"
tmux kill-window -t "$WIN" 2>/dev/null || true

runner="$TY_QA_ROOT/agent-$TASK_ID.sh"
printf '#!/usr/bin/env bash\ncd %q\nexec %s\n' "$WT" "$AGENT_CMD" > "$runner"
chmod +x "$runner"

tmux new-window -d -t "$TY_DAEMON_SESSION" -n "task-$TASK_ID" -c "$WT" "bash $runner"
sleep 6
tmux send-keys -t "$WIN.0" Enter   # accept Claude folder-trust prompt if shown
sleep 6
tmux split-window -h -t "$WIN.0" -c "$WT" "${SHELL:-/bin/zsh}"
sleep 1

CLAUDE_PANE=$(tmux display-message -t "$WIN.0" -p '#{pane_id}')
SHELL_PANE=$(tmux display-message -t "$WIN.1" -p '#{pane_id}')
WIN_ID=$(tmux display-message -t "$WIN" -p '#{window_id}')

sqlite3 "$WORKTREE_DB_PATH" "UPDATE tasks SET \
  status='processing', worktree_path='$WT', daemon_session='$TY_DAEMON_SESSION', \
  tmux_window_id='$WIN_ID', claude_pane_id='$CLAUDE_PANE', shell_pane_id='$SHELL_PANE' \
  WHERE id=$TASK_ID;"

echo "==> task $TASK_ID is live: claude=$CLAUDE_PANE shell=$SHELL_PANE window=$WIN_ID"
echo "    In the TUI: focus In-Progress (P), Enter to open -> fires the real joinTmuxPane."

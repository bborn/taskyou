#!/bin/bash
# Desktop-notify plugin: fire a native notification on a task event.
#
# TaskYou exports these for every hook:
#   TASK_ID TASK_TITLE TASK_STATUS TASK_PROJECT TASK_TYPE
#   TASK_MESSAGE TASK_EVENT WORKTREE_PATH
# and for plugin hooks specifically:
#   TASK_PLUGIN_NAME TASK_PLUGIN_DIR
set -euo pipefail

title="TaskYou: ${TASK_EVENT#task.}"
body="#${TASK_ID} ${TASK_TITLE} — ${TASK_MESSAGE:-}"

case "$(uname -s)" in
  Darwin)
    osascript -e "display notification \"${body//\"/\\\"}\" with title \"${title}\"" || true
    ;;
  Linux)
    command -v notify-send >/dev/null 2>&1 && notify-send "$title" "$body" || true
    ;;
  *)
    echo "[$title] $body"
    ;;
esac

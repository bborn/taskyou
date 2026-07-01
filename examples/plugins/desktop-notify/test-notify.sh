#!/bin/bash
# desktop-notify action: fire a test notification on demand.
#
# Actions get TASK_PLUGIN_NAME / TASK_PLUGIN_DIR always, and the TASK_* vars
# (TASK_ID, TASK_TITLE, …) only when invoked with a task in context.
set -euo pipefail

title="TaskYou: ${TASK_PLUGIN_NAME:-plugin} test"
if [[ -n "${TASK_ID:-}" ]]; then
  body="Wired up for #${TASK_ID} ${TASK_TITLE:-}"
else
  body="Notifications are wired up."
fi

case "$(uname -s)" in
  Darwin) osascript -e "display notification \"${body//\"/\\\"}\" with title \"${title}\"" || true ;;
  Linux)  command -v notify-send >/dev/null 2>&1 && notify-send "$title" "$body" || true ;;
  *)      echo "[$title] $body" ;;
esac
echo "sent test notification"

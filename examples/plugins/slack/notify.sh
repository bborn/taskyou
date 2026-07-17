#!/bin/bash
# Slack notification hook. Reads SLACK_WEBHOOK_URL from the environment or from
# config.env bundled next to this script (TASK_PLUGIN_DIR). Fails quietly (exit
# 0) when unconfigured so it never blocks the task pipeline.
set -euo pipefail

# Load bundled config, if present. cwd is already the plugin dir, but be explicit.
if [[ -n "${TASK_PLUGIN_DIR:-}" && -f "$TASK_PLUGIN_DIR/config.env" ]]; then
  # shellcheck disable=SC1091
  source "$TASK_PLUGIN_DIR/config.env"
fi

if [[ -z "${SLACK_WEBHOOK_URL:-}" ]]; then
  echo "slack plugin: SLACK_WEBHOOK_URL not set (see config.example.env)" >&2
  exit 0
fi

# Pick an emoji per event.
case "${TASK_EVENT:-}" in
  task.done)    emoji=":white_check_mark:" ;;
  task.blocked) emoji=":raised_hand:" ;;
  task.failed)  emoji=":x:" ;;
  *)            emoji=":information_source:" ;;
esac

# Minimal JSON string escaping (backslash + double-quote).
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

event="${TASK_EVENT#task.}"
header="$emoji *#${TASK_ID:-?} ${TASK_TITLE:-untitled}* — ${event}"
detail="${TASK_MESSAGE:-}"
project="${TASK_PROJECT:-}"
[[ -n "$project" ]] && header="$header  \`${project}\`"

text="$header"
[[ -n "$detail" ]] && text="$text\n${detail}"

payload=$(printf '{"text":"%s"}' "$(esc "$text")")

curl -sS -X POST -H 'Content-type: application/json' --data "$payload" "$SLACK_WEBHOOK_URL" >/dev/null \
  || echo "slack plugin: webhook POST failed" >&2

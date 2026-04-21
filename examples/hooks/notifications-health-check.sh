#!/usr/bin/env bash
# Health check: alert if notifications.jsonl goes silent longer than expected.
#
# Run from cron (e.g. every hour):
#   0 * * * * /home/exedev/bin/notifications-health-check.sh
#
# Customize NOTIFICATIONS_FILE, MAX_AGE_HOURS, and ALERT_CMD for your setup.

set -euo pipefail

NOTIFICATIONS_FILE="${NOTIFICATIONS_FILE:-$HOME/notifications.jsonl}"
MAX_AGE_HOURS="${MAX_AGE_HOURS:-24}"
# ALERT_CMD receives the alert message on stdin. Examples:
#   ALERT_CMD="mail -s 'TaskYou notifications stale' you@example.com"
#   ALERT_CMD="curl -X POST -H 'Content-Type: application/json' --data-binary @- https://hooks.slack.com/..."
ALERT_CMD="${ALERT_CMD:-cat}"
STATE_FILE="${STATE_FILE:-/tmp/notifications-health-check.state}"

if [[ ! -f "$NOTIFICATIONS_FILE" ]]; then
  echo "notifications.jsonl missing at $NOTIFICATIONS_FILE — TaskYou hooks never ran" | eval "$ALERT_CMD"
  exit 1
fi

mtime=$(stat -c %Y "$NOTIFICATIONS_FILE" 2>/dev/null || stat -f %m "$NOTIFICATIONS_FILE")
now=$(date +%s)
age_hours=$(( (now - mtime) / 3600 ))

if (( age_hours < MAX_AGE_HOURS )); then
  # Healthy — clear any prior alert state so we re-alert if it breaks again.
  rm -f "$STATE_FILE"
  exit 0
fi

# Don't re-alert on every run for the same outage.
last_alert_mtime=$(cat "$STATE_FILE" 2>/dev/null || echo 0)
if [[ "$last_alert_mtime" == "$mtime" ]]; then
  exit 0
fi

cat <<EOF | eval "$ALERT_CMD"
TaskYou notifications.jsonl has been silent for ${age_hours}h (threshold: ${MAX_AGE_HOURS}h).

File: $NOTIFICATIONS_FILE
Last write: $(date -d "@$mtime" 2>/dev/null || date -r "$mtime")

Check:
  - Is \`ty daemon\` running?  pgrep -a "ty daemon"
  - Are hook scripts in ~/.config/task/hooks/ executable?
  - Is the daemon running a version that emits task.completed? (requires TaskYou >= 0.2.37)
EOF

echo "$mtime" > "$STATE_FILE"

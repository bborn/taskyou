#!/bin/bash
# Example: Slack integration for task events
#
# Installation:
#   1. Get Slack webhook URL: https://api.slack.com/messaging/webhooks
#   2. Set SLACK_WEBHOOK_URL environment variable or hardcode below
#   3. Copy to ~/.config/task/hooks/task.completed (or any event)
#   4. chmod +x ~/.config/task/hooks/task.completed

# Configuration
SLACK_WEBHOOK_URL="${SLACK_WEBHOOK_URL:-https://hooks.slack.com/services/YOUR/WEBHOOK/URL}"
SLACK_CHANNEL="${SLACK_CHANNEL:-#tasks}"
SLACK_USERNAME="${SLACK_USERNAME:-TaskYou Bot}"

# Choose emoji based on event type
case "$TASK_EVENT" in
    task.completed)
        EMOJI=":white_check_mark:"
        COLOR="good"  # green
        ;;
    task.failed)
        EMOJI=":x:"
        COLOR="danger"  # red
        ;;
    task.blocked)
        EMOJI=":warning:"
        COLOR="warning"  # yellow
        ;;
    task.started)
        EMOJI=":arrow_forward:"
        COLOR="#0000FF"  # blue
        ;;
    *)
        EMOJI=":bell:"
        COLOR="#808080"  # gray
        ;;
esac

# Build message
MESSAGE="$EMOJI Task #$TASK_ID: *$TASK_TITLE*"

# Send to Slack
curl -X POST "$SLACK_WEBHOOK_URL" \
  -H 'Content-Type: application/json' \
  -d "{
    \"channel\": \"$SLACK_CHANNEL\",
    \"username\": \"$SLACK_USERNAME\",
    \"text\": \"$MESSAGE\",
    \"attachments\": [{
      \"color\": \"$COLOR\",
      \"fields\": [
        {\"title\": \"Project\", \"value\": \"$TASK_PROJECT\", \"short\": true},
        {\"title\": \"Status\", \"value\": \"$TASK_STATUS\", \"short\": true},
        {\"title\": \"Type\", \"value\": \"$TASK_TYPE\", \"short\": true},
        {\"title\": \"Executor\", \"value\": \"$TASK_EXECUTOR\", \"short\": true}
      ],
      \"footer\": \"TaskYou\",
      \"ts\": $(date +%s)
    }]
  }"

echo "Sent to Slack: $MESSAGE"

# Event System

TaskYou emits events when tasks change state, enabling automation, integrations, and notifications through multiple delivery mechanisms.

## Overview

Events are automatically emitted when:
- Tasks are created, updated, or deleted
- Task status changes (backlog → queued → processing → done/blocked)
- Tasks are pinned/unpinned
- Tasks are retried or interrupted

## Event Types

| Event Type | Description | When Emitted |
|------------|-------------|--------------|
| `task.created` | New task created | After CreateTask() |
| `task.updated` | Task fields updated | After UpdateTask() with changes |
| `task.deleted` | Task removed | After DeleteTask() |
| `task.status.changed` | Status transition | After UpdateTaskStatus() |
| `task.queued` | Task queued for execution | When task is queued |
| `task.started` | Execution begins | When executor starts task |
| `task.processing` | Task actively executing | When task enters processing state |
| `task.blocked` | Task needs input | When task is blocked waiting for user |
| `task.completed` | Task finished successfully | When task completes |
| `task.failed` | Task execution failed | When task fails |
| `task.retried` | Task retried with feedback | When user retries a task |
| `task.interrupted` | Task cancelled by user | When task is interrupted |
| `task.pinned` | Task pinned to top | When task is pinned |
| `task.unpinned` | Task unpinned | When task is unpinned |

## Delivery Mechanisms

### 1. Script Hooks (Recommended for Local Automation)

Place executable scripts in `~/.config/task/hooks/` named after the event type.

**Example: `~/.config/task/hooks/task.completed`**
```bash
#!/bin/bash
# Runs when a task completes

echo "Task #$TASK_ID completed: $TASK_TITLE"
echo "Project: $TASK_PROJECT"
echo "Status: $TASK_STATUS"

# Send notification
osascript -e "display notification \"$TASK_TITLE\" with title \"Task Completed\""
```

**Environment Variables Available:**
- `TASK_ID` - Task ID
- `TASK_TITLE` - Task title
- `TASK_STATUS` - Current status
- `TASK_PROJECT` - Project name
- `TASK_TYPE` - Task type (code, writing, etc.)
- `TASK_EXECUTOR` - Executor used (claude, codex, gemini, pi)
- `TASK_EVENT` - Event type (e.g., task.completed)
- `TASK_MESSAGE` - Event message
- `TASK_TIMESTAMP` - Event timestamp (RFC3339)
- `TASK_METADATA` - Additional metadata as JSON

**Make scripts executable:**
```bash
chmod +x ~/.config/task/hooks/task.completed
```

### 2. Webhooks (Recommended for External Integrations)

Configure HTTP endpoints to receive POST requests with event data.

**Add webhook:**
```bash
ty events webhooks add https://example.com/webhook
```

**List webhooks:**
```bash
ty events webhooks list
```

**Remove webhook:**
```bash
ty events webhooks remove https://example.com/webhook
```

**Webhook Payload:**
```json
{
  "type": "task.completed",
  "task_id": 123,
  "task": {
    "id": 123,
    "title": "Implement feature X",
    "status": "done",
    "project": "myapp",
    "executor": "claude"
  },
  "message": "Task completed successfully",
  "metadata": {},
  "timestamp": "2024-01-01T12:00:00Z"
}
```

**Important:** Restart the daemon after adding/removing webhooks:
```bash
ty daemon restart
```

### 3. Event Log (Audit Trail)

All events are stored in the database (`event_log` table) for audit and debugging.

**View recent events:**
```bash
ty events list                    # Last 50 events
ty events list --limit 100        # Last 100 events
ty events list --type task.created  # Filter by type
ty events list --task 42          # Events for specific task
ty events list --json             # JSON output
```

### 4. In-Process Channels (For Real-Time UI Updates)

The TUI subscribes to events for real-time updates without polling. This is handled automatically.

## Common Use Cases

### Desktop Notifications

**macOS** (`~/.config/task/hooks/task.completed`):
```bash
#!/bin/bash
osascript -e "display notification \"$TASK_TITLE\" with title \"Task Completed\""
```

**Linux** (`~/.config/task/hooks/task.completed`):
```bash
#!/bin/bash
notify-send "Task Completed" "$TASK_TITLE"
```

### Slack Notifications

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

WEBHOOK_URL="https://hooks.slack.com/services/YOUR/WEBHOOK/URL"

curl -X POST "$WEBHOOK_URL" \
  -H 'Content-Type: application/json' \
  -d "{
    \"text\": \"Task #$TASK_ID completed: $TASK_TITLE\",
    \"blocks\": [{
      \"type\": \"section\",
      \"text\": {
        \"type\": \"mrkdwn\",
        \"text\": \"*Task Completed*\n\n*Task:* $TASK_TITLE\n*Project:* $TASK_PROJECT\n*ID:* #$TASK_ID\"
      }
    }]
  }"
```

### Discord Webhooks

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

WEBHOOK_URL="https://discord.com/api/webhooks/YOUR/WEBHOOK"

curl -X POST "$WEBHOOK_URL" \
  -H 'Content-Type: application/json' \
  -d "{
    \"content\": \"✅ Task #$TASK_ID completed: **$TASK_TITLE**\",
    \"embeds\": [{
      \"title\": \"Task Details\",
      \"color\": 5814783,
      \"fields\": [
        {\"name\": \"Project\", \"value\": \"$TASK_PROJECT\", \"inline\": true},
        {\"name\": \"Type\", \"value\": \"$TASK_TYPE\", \"inline\": true}
      ]
    }]
  }"
```

### Time Tracking

Track when tasks start and complete:

```bash
#!/bin/bash
# ~/.config/task/hooks/task.started

echo "$(date -Iseconds),$TASK_ID,$TASK_TITLE,started" >> ~/.task-time-log.csv
```

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

echo "$(date -Iseconds),$TASK_ID,$TASK_TITLE,completed" >> ~/.task-time-log.csv
```

### Project-Specific Actions

Trigger actions based on project:

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

if [ "$TASK_PROJECT" = "website" ]; then
    # Deploy website
    echo "Deploying website..."
    ssh server "cd /var/www && git pull"
fi
```

### Email Notifications

```bash
#!/bin/bash
# ~/.config/task/hooks/task.blocked

# Task needs input - send email
echo "Task #$TASK_ID is blocked: $TASK_MESSAGE" | \
    mail -s "Task Blocked: $TASK_TITLE" user@example.com
```

### Integration with External Task Managers

Sync completed tasks to other systems:

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

# Sync to Todoist, Linear, Jira, etc.
curl -X POST https://api.todoist.com/rest/v2/tasks \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"content\": \"✓ $TASK_TITLE\", \"project_id\": \"123\"}"
```

## Event Hook vs Legacy Hook System

TaskYou has two hook systems:

1. **New Event System** (Recommended)
   - More event types
   - Consistent event data
   - Webhook support
   - Event log database
   - Location: `~/.config/task/hooks/`

2. **Legacy Hook System** (Deprecated)
   - Limited to 4 events: task.started, task.done, task.failed, task.blocked
   - Less consistent event naming
   - No webhook support
   - Still supported for backward compatibility

**Migration:** Simply rename your hooks from the legacy names to the new event names. Both systems work in parallel.

## Debugging

### Test Hooks Locally

```bash
# Manually trigger a hook to test
TASK_ID=123 \
TASK_TITLE="Test Task" \
TASK_STATUS="done" \
TASK_PROJECT="test" \
TASK_EVENT="task.completed" \
~/.config/task/hooks/task.completed
```

### View Hook Output

Hook execution errors are logged:
```bash
# Daemon logs show hook execution results
tail -f ~/.local/share/task/daemon.log
```

### Test Webhooks

```bash
# Start a local test server
python3 -m http.server 8080

# Add webhook
ty events webhooks add http://localhost:8080/webhook

# Restart daemon
ty daemon restart

# Create/complete a task and watch the server output
```

## Event Flow Diagram

```
┌─────────────┐
│   Action    │  (Create task, update status, etc.)
└──────┬──────┘
       │
       ▼
┌─────────────┐
│ Event       │  Event created with type, task data, metadata
│ Generated   │
└──────┬──────┘
       │
       ▼
┌─────────────┐
│ Event Queue │  Async queue (non-blocking)
└──────┬──────┘
       │
       ├──────────────────┬────────────────┬────────────────┐
       ▼                  ▼                ▼                ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  Script      │  │   Webhook    │  │  Event Log   │  │  In-Process  │
│  Hooks       │  │   HTTP POST  │  │  Database    │  │  Channels    │
└──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘
```

## Best Practices

1. **Keep hooks fast** - Hooks run asynchronously but should complete within 30 seconds
2. **Handle errors gracefully** - Exit with status 0 even if non-critical operations fail
3. **Use webhooks for heavy processing** - Offload complex logic to external services
4. **Log hook activity** - Redirect output to a log file for debugging
5. **Test hooks manually** - Use environment variables to test before deploying
6. **Secure webhook endpoints** - Validate incoming requests, use HTTPS
7. **Monitor event log** - Use `ty events list` to verify events are firing

## Security Considerations

- Hook scripts run with your user permissions - be cautious with what they execute
- Webhook URLs may be visible in database - avoid embedding secrets
- Use environment variables or secret managers for sensitive data
- Validate webhook payloads on the receiving end
- Consider rate limiting on webhook endpoints

## Troubleshooting

### Hooks not executing
- Verify script is executable: `chmod +x ~/.config/task/hooks/task.completed`
- Check script has shebang: `#!/bin/bash`
- Test manually with environment variables
- Check daemon logs for errors

### Webhooks not firing
- Verify daemon is running: `ty daemon status`
- Restart daemon after adding webhooks: `ty daemon restart`
- Check webhook URL is correct: `ty events webhooks list`
- Test endpoint is reachable: `curl -X POST <webhook-url>`

### Events not appearing in log
- Verify daemon is running with events enabled
- Check database has event_log table: `sqlite3 ~/.local/share/task/tasks.db ".schema event_log"`
- Try `ty events list --limit 200` to see older events

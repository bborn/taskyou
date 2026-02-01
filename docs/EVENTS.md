# TaskYou Event System

TaskYou emits events for all task lifecycle changes, enabling automation and integrations.

## Event Delivery Mechanisms

Events can be delivered through multiple channels:

### 1. **Real-time Streaming** (NEW!)
Stream events to stdout as newline-delimited JSON:

```bash
# Watch all events
ty events watch

# Filter by event type
ty events watch --type task.completed

# Filter by task ID
ty events watch --task 123

# Filter by project
ty events watch --project myproject

# Pipe to other tools
ty events watch | jq 'select(.type == "task.failed")'
ty events watch --type task.completed | notify-send "Task completed"
```

**Use cases:**
- Real-time monitoring dashboards
- CI/CD integrations
- Custom automation scripts
- Notifications

### 2. **Webhooks**
HTTP POST requests to configured URLs:

```bash
# Add a webhook
ty events webhooks add https://example.com/webhook

# List configured webhooks
ty events webhooks list

# Remove a webhook
ty events webhooks remove https://example.com/webhook

# After adding/removing webhooks, restart the daemon:
ty daemon restart
```

**Webhook payload format:**
```json
{
  "type": "task.completed",
  "task_id": 123,
  "task": { /* full task object */ },
  "message": "Task completed successfully",
  "metadata": { /* additional context */ },
  "timestamp": "2024-01-01T12:00:00Z"
}
```

**Use cases:**
- Slack/Discord notifications
- External system integrations
- Monitoring services
- Analytics platforms

### 3. **Script Hooks**
Execute local scripts on events:

```bash
# Create a hook script
mkdir -p ~/.config/task/hooks
cat > ~/.config/task/hooks/task.completed << 'EOF'
#!/bin/bash
echo "Task #$TASK_ID completed: $TASK_TITLE" | notify-send -u normal "TaskYou"
EOF
chmod +x ~/.config/task/hooks/task.completed
```

**Available environment variables in hooks:**
- `TASK_ID` - Task ID
- `TASK_TITLE` - Task title
- `TASK_STATUS` - Current status
- `TASK_PROJECT` - Project name
- `TASK_TYPE` - Task type
- `TASK_EXECUTOR` - Executor name (claude, codex, etc.)
- `TASK_EVENT` - Event type
- `TASK_MESSAGE` - Event message
- `TASK_METADATA` - JSON metadata object
- `TASK_TIMESTAMP` - Event timestamp (RFC3339)

**Use cases:**
- Desktop notifications
- Local file updates
- System commands
- Custom logging

### 4. **Event Log**
Query historical events from the database:

```bash
# List recent events
ty events list

# Filter by type
ty events list --type task.completed

# Filter by task
ty events list --task 123

# Limit results
ty events list --limit 100

# Output as JSON
ty events list --json
```

**Use cases:**
- Audit trail
- Debugging
- Analytics
- Historical analysis

## Event Types

### Task Lifecycle Events

| Event Type | Description | When Emitted |
|------------|-------------|--------------|
| `task.created` | Task created | When a new task is created |
| `task.updated` | Task updated | When task fields are modified |
| `task.deleted` | Task deleted | When a task is deleted |
| `task.queued` | Task queued | When task is queued for execution |
| `task.started` | Task started | When executor begins processing |
| `task.processing` | Task processing | When task is actively being worked on |
| `task.blocked` | Task blocked | When task needs user input |
| `task.completed` | Task completed | When task finishes successfully |
| `task.failed` | Task failed | When task execution fails |
| `task.interrupted` | Task interrupted | When user stops a running task |
| `task.retried` | Task retried | When a blocked task is retried |
| `task.status.changed` | Status changed | When task status changes |
| `task.pinned` | Task pinned | When task is pinned |
| `task.unpinned` | Task unpinned | When task is unpinned |

### Event Metadata

Events include rich metadata in the `metadata` field:

**Status changes:**
```json
{
  "old_status": "queued",
  "new_status": "processing"
}
```

**Task updates:**
```json
{
  "title": {"old": "Old title", "new": "New title"},
  "status": {"old": "backlog", "new": "queued"}
}
```

**Task retries:**
```json
{
  "feedback": "Please fix the error in the database migration"
}
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                       Task Operations                        │
│  (Executor modifies tasks - creates, updates, status changes)│
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
              ┌──────────────────────┐
              │   Events Manager     │
              │  (internal/events)   │
              └──────────┬───────────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
 ┌─────────────┐  ┌──────────┐  ┌─────────────┐
 │   In-Process │  │ Database │  │   HTTP/SSE  │
 │   Channels   │  │ Event Log│  │   Stream    │
 └──────┬──────┘  └──────────┘  └──────┬──────┘
        │                                │
        ▼                                ▼
  ┌──────────┐                   ┌──────────────┐
  │   TUI    │                   │ ty events    │
  │ Updates  │                   │    watch     │
  └──────────┘                   └──────────────┘
```

**Note:** Events are only emitted for operations performed by the executor (daemon). Direct CLI operations (like `ty status`) provide immediate synchronous feedback and do not emit events. This is by design - events are for automation and external integrations, not for user-initiated commands.

## Example Integrations

### Slack Notification

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

curl -X POST https://hooks.slack.com/services/YOUR/WEBHOOK/URL \
  -H 'Content-Type: application/json' \
  -d "{
    \"text\": \"Task completed: $TASK_TITLE\",
    \"blocks\": [{
      \"type\": \"section\",
      \"text\": {
        \"type\": \"mrkdwn\",
        \"text\": \"*Task #$TASK_ID completed*\n$TASK_TITLE\nProject: $TASK_PROJECT\"
      }
    }]
  }"
```

### Real-time Dashboard

```bash
# Stream events to a dashboard
ty events watch | while read event; do
  echo "$event" | jq '{
    time: .timestamp,
    task: .task_id,
    status: .task.Status,
    title: .task.Title
  }' >> /var/log/taskyou/dashboard.jsonl
done
```

### Automated PR Comments

```javascript
// Node.js webhook server
const express = require('express');
const { Octokit } = require('@octokit/rest');

const app = express();
app.use(express.json());

const octokit = new Octokit({ auth: process.env.GITHUB_TOKEN });

app.post('/webhook', async (req, res) => {
  const event = req.body;
  
  if (event.type === 'task.completed' && event.task.PRURL) {
    const [owner, repo, prNumber] = event.task.PRURL.match(/github.com\/(.+)\/(.+)\/pull\/(\d+)/).slice(1);
    
    await octokit.issues.createComment({
      owner,
      repo,
      issue_number: parseInt(prNumber),
      body: `✅ Task completed: ${event.task.Title}\n\nReview ready!`
    });
  }
  
  res.json({ ok: true });
});

app.listen(3000);
```

### Desktop Notifications (macOS)

```bash
#!/bin/bash
# ~/.config/task/hooks/task.failed

osascript -e "display notification \"$TASK_TITLE\" with title \"Task Failed\" subtitle \"Task #$TASK_ID\" sound name \"Basso\""
```

### Desktop Notifications (Linux)

```bash
#!/bin/bash
# ~/.config/task/hooks/task.completed

notify-send -u normal \
  -i checkbox-checked \
  "Task Completed" \
  "$TASK_TITLE"
```

## HTTP API

The daemon exposes an HTTP API for event streaming:

**Endpoint:** `GET /events/stream`

**Query Parameters:**
- `type` - Filter by event type (e.g., `task.completed`)
- `task` - Filter by task ID
- `project` - Filter by project name

**Response:** Server-Sent Events (SSE) stream

**Example:**
```bash
curl -N -H "Accept: text/event-stream" \
  "http://localhost:3333/events/stream?type=task.completed"
```

## Configuration

### HTTP Server Address

The HTTP server runs on port `3333` by default.

**Local daemon:**
```bash
# Default: localhost:3333
ty daemon
```

**Remote daemon (taskd):**
```bash
# Custom port
taskd -http :4444
```

### Environment Variables

None required - the event system is enabled by default.

## Troubleshooting

### Events not appearing

**Symptom:** `ty events watch` shows connection but no events

**Cause:** Events are only emitted by the executor, not by CLI commands

**Solution:** Queue a task for execution to see events:
```bash
ty execute <task-id>
```

### Webhook delivery failures

**Symptom:** Webhook requests timeout or fail

**Check:**
1. Webhook URL is accessible from the daemon
2. Webhook server is running
3. Check daemon logs for errors

### Hook scripts not executing

**Check:**
1. Script has execute permissions: `chmod +x ~/.config/task/hooks/task.*`
2. Script has correct shebang: `#!/bin/bash`
3. Check daemon logs for errors

## Best Practices

1. **Use filtering** - Filter events by type or task to reduce noise
2. **Handle errors** - Webhook servers and hooks should handle errors gracefully
3. **Idempotency** - Design hooks to be idempotent (safe to run multiple times)
4. **Timeouts** - Hooks have a 30-second timeout, keep them fast
5. **Async processing** - Use webhooks or `ty events watch` for long-running operations
6. **Security** - Validate webhook payloads and sanitize environment variables in hooks
7. **Monitoring** - Use the event log (`ty events list`) to verify event delivery

## See Also

- [AGENTS.md](../AGENTS.md) - Architecture overview
- [DEVELOPMENT.md](../DEVELOPMENT.md) - Development guide
- [internal/events/events.go](../internal/events/events.go) - Event manager implementation
- [internal/server/http.go](../internal/server/http.go) - HTTP/SSE server implementation

# Task Event Hook Examples

Example scripts demonstrating how to use TaskYou's event hook system.

## Quick Start

1. **Choose a hook script** from this directory
2. **Copy to hooks directory:**
   ```bash
   cp task.completed ~/.config/task/hooks/
   ```
3. **Make executable:**
   ```bash
   chmod +x ~/.config/task/hooks/task.completed
   ```
4. **Test it:**
   ```bash
   # Complete a task and watch for notification
   ty execute <task-id>
   ```

## Available Hooks

| Script | Event | Description |
|--------|-------|-------------|
| `task.completed` | Task completes successfully | Desktop notification + logging |
| `task.blocked` | Task needs user input | Alert notification + logging |
| `task.started` | Task execution begins | Logging with timing info |
| `slack-integration.sh` | Any event | Send notifications to Slack |

## Available Events

Hook scripts can be named after any of these events:

- `task.created` - New task created
- `task.updated` - Task fields updated
- `task.deleted` - Task removed
- `task.status.changed` - Status transition
- `task.queued` - Task queued for execution
- `task.started` - Execution begins
- `task.processing` - Task actively executing
- `task.blocked` - Task needs input
- `task.completed` - Task finished successfully
- `task.failed` - Task execution failed
- `task.retried` - Task retried with feedback
- `task.interrupted` - Task cancelled
- `task.pinned` - Task pinned
- `task.unpinned` - Task unpinned

## Environment Variables

All hook scripts receive these environment variables:

```bash
TASK_ID          # Task ID (e.g., "123")
TASK_TITLE       # Task title
TASK_STATUS      # Current status (backlog, queued, processing, blocked, done)
TASK_PROJECT     # Project name
TASK_TYPE        # Task type (code, writing, thinking, etc.)
TASK_EXECUTOR    # Executor used (claude, codex, gemini, pi)
TASK_EVENT       # Event type (e.g., "task.completed")
TASK_MESSAGE     # Event message
TASK_TIMESTAMP   # Event timestamp in RFC3339 format
TASK_METADATA    # Additional metadata as JSON
```

## Testing Hooks Locally

Test your hook script without triggering a real event:

```bash
TASK_ID=999 \
TASK_TITLE="Test Task" \
TASK_STATUS="done" \
TASK_PROJECT="test" \
TASK_TYPE="code" \
TASK_EXECUTOR="claude" \
TASK_EVENT="task.completed" \
TASK_MESSAGE="Task completed successfully" \
TASK_TIMESTAMP="$(date -Iseconds)" \
~/.config/task/hooks/task.completed
```

## Common Integrations

### Desktop Notifications

See `task.completed` for macOS and Linux examples.

### Slack

See `slack-integration.sh` for a complete Slack webhook integration.

### Discord

```bash
#!/bin/bash
WEBHOOK_URL="https://discord.com/api/webhooks/YOUR/WEBHOOK"

curl -X POST "$WEBHOOK_URL" \
  -H 'Content-Type: application/json' \
  -d "{
    \"content\": \"✅ Task #$TASK_ID: **$TASK_TITLE**\",
    \"embeds\": [{
      \"color\": 5814783,
      \"fields\": [
        {\"name\": \"Project\", \"value\": \"$TASK_PROJECT\"},
        {\"name\": \"Status\", \"value\": \"$TASK_STATUS\"}
      ]
    }]
  }"
```

### Email

```bash
#!/bin/bash
echo "$TASK_MESSAGE

Task: $TASK_TITLE
Project: $TASK_PROJECT
ID: #$TASK_ID
Time: $TASK_TIMESTAMP" | \
mail -s "Task Update: $TASK_TITLE" user@example.com
```

### Time Tracking

```bash
#!/bin/bash
# Log to CSV for time tracking
echo "$(date -Iseconds),$TASK_ID,$TASK_TITLE,$TASK_STATUS,$TASK_PROJECT" \
  >> ~/.task-tracking.csv
```

## Best Practices

1. **Keep hooks fast** - They run asynchronously but should complete quickly
2. **Handle errors** - Use `|| true` to prevent hook failures from affecting tasks
3. **Log output** - Redirect to a log file for debugging:
   ```bash
   echo "Hook executed: $TASK_EVENT" >> ~/.task-hooks.log 2>&1
   ```
4. **Use conditionals** - Filter events by project or type:
   ```bash
   if [ "$TASK_PROJECT" = "production" ]; then
       # Only for production tasks
   fi
   ```
5. **Secure secrets** - Use environment variables or secret managers, not hardcoded values

## Debugging

### Hook not executing?

1. Check if script is executable:
   ```bash
   ls -la ~/.config/task/hooks/
   ```

2. Verify script has shebang:
   ```bash
   head -n 1 ~/.config/task/hooks/task.completed
   # Should show: #!/bin/bash
   ```

3. Test manually (see "Testing Hooks Locally" above)

4. Check daemon logs:
   ```bash
   tail -f ~/.local/share/task/daemon.log
   ```

### Script has errors?

Run with bash -x for debugging:

```bash
bash -x ~/.config/task/hooks/task.completed
```

## More Examples

For more integration examples and documentation, see:
- [Event System Documentation](../../docs/EVENTS.md)
- [TaskYou Website](https://github.com/bborn/workflow)

## Contributing

Have a useful hook script? Consider contributing it to the examples!

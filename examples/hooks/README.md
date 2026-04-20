# Task Event Hooks

Scripts that run when task events occur. Place in `~/.config/task/hooks/`.

## Setup

```bash
cp examples/hooks/task.completed ~/.config/task/hooks/
chmod +x ~/.config/task/hooks/task.completed
```

## Available Events

- `task.created` - New task created
- `task.updated` - Task fields changed (including every status transition)
- `task.deleted` - Task removed
- `task.started` - Execution begins
- `task.blocked` - Task needs user input (also fires on failure via the status change)
- `task.completed` - Agent finished successfully (task moves to backlog for human review)
- `task.failed` - Agent execution failed (distinct signal from the `task.blocked` fired by the status change)
- `task.worktree_ready` - Worktree ready for the agent

## Health Check

See `notifications-health-check.sh` for a cron-friendly script that alerts when
the notifications file goes silent longer than expected (default: 24 hours).
Install as:

```bash
cp examples/hooks/notifications-health-check.sh ~/bin/
# Cron entry (runs hourly):
(crontab -l; echo "0 * * * * $HOME/bin/notifications-health-check.sh") | crontab -
```

## Environment Variables

```bash
TASK_ID          # Task ID
TASK_TITLE       # Task title
TASK_STATUS      # Current status
TASK_PROJECT     # Project name
TASK_EVENT       # Event type
TASK_TIMESTAMP   # RFC3339 timestamp
```

## Example

See `task.completed` for a desktop notification example.

# Task Event Hooks

Scripts that run when task events occur. Place in `~/.config/task/hooks/`.

## Setup

```bash
cp examples/hooks/task.completed ~/.config/task/hooks/
chmod +x ~/.config/task/hooks/task.completed
```

## Available Events

- `task.created` - New task created
- `task.updated` - Task fields changed
- `task.deleted` - Task removed
- `task.started` - Execution begins
- `task.blocked` - Task needs user input
- `task.completed` - Task finished
- `task.failed` - Execution failed

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

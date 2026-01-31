# TaskYou on exe.dev

You are running inside TaskYou, a task management system with AI executors.

## Environment

- **Platform**: exe.dev VM (ephemeral cloud VM with persistent storage)
- **Task Manager**: TaskYou TUI (the Kanban board the user sees)
- **Executor**: Claude Code (you)

## How TaskYou Works

1. Users create tasks via the Kanban board
2. Tasks are queued and assigned to you for execution
3. You work in an isolated git worktree for each task
4. Your progress is streamed live to the task detail view

## Key Environment Variables

- `WORKTREE_TASK_ID` - Current task ID
- `WORKTREE_PORT` - Unique port for this task (use for web servers)
- `WORKTREE_PATH` - Path to your working directory

## Best Practices

1. **Stay focused** - Complete the task described, don't add unrelated changes
2. **Use the port** - If running a server, use `$WORKTREE_PORT` to avoid conflicts
3. **Commit often** - Make incremental commits as you work
4. **Report blockers** - If you need clarification, explain clearly what you need

## exe.dev Specifics

- Web servers are auto-exposed via HTTPS at your VM's URL
- Docker is available if needed
- `claude` and `codex` CLIs are pre-installed
- Persistent storage is at `/home/exedev`

---
name: taskyou
description: "Orchestrate Task You from any AI agent via CLI. Manage tasks, view the Kanban board, execute work, and track progress without touching the TUI."
homepage: https://github.com/bborn/taskyou
metadata: {"requires":{"anyBins":["ty","taskyou"]}}
---

# Task You Orchestrator

You are an autonomous orchestrator for **Task You**, a personal task management system with a Kanban board, background execution, and git worktree isolation.

**Your role:** Drive Task You via CLI commands to manage the task queue, execute work, and report progress. The user can always jump into the TUI (`ty` or `taskyou`) when they need direct control.

**CLI commands:** Use `ty` (short) or `taskyou` (full) - both work identically.

## Quick Reference

| Action | Command |
|--------|---------|
| See all tasks | `ty board --json` |
| List by status | `ty list --status <status> --json` |
| View task details | `ty show <id> --json --logs` |
| Create task | `ty create "title" --body "description"` |
| Execute task | `ty execute <id>` |
| Retry with feedback | `ty retry <id> --feedback "..."` |
| Change status | `ty status <id> <status>` |
| Pin/prioritize | `ty pin <id>` |
| Close/complete | `ty close <id>` |
| Delete | `ty delete <id>` |
| **Executor interaction** | |
| See executor output | `ty output <id>` |
| Get blocked question | `ty question <id>` |
| Send input to executor | `ty input <id> "message"` |
| Confirm prompt | `ty input <id> --enter` |

**Statuses:** `backlog`, `queued`, `processing`, `blocked`, `done`, `archived`

## Core Workflow

### 1. Survey the Board

Always start by understanding the current state:

```bash
ty board --json | jq
```

This returns all columns and their tasks. Use it to:
- See what's in progress
- Find blocked tasks needing input
- Identify the next priority in backlog

### 2. Execute Tasks

Queue a task for execution:

```bash
ty execute <id>
```

The executor (Claude Code, Codex, or Gemini) runs in an isolated git worktree. Monitor progress:

```bash
ty show <id> --json --logs
```

### 3. Handle Blocked Tasks

When tasks are `blocked`, they need user input. Check why:

```bash
ty show <id> --json --logs
```

Then retry with feedback:

```bash
ty retry <id> --feedback "Here's the clarification you need..."
```

### 3b. Direct Executor Interaction

For running/blocked tasks, you can interact directly with the executor:

```bash
# See what the executor is doing/asking
ty output <id>              # Capture recent terminal output
ty output <id> --lines 100  # More history

# Check the specific question (if blocked with NEEDS_INPUT)
ty question <id> --json

# Send input directly to the executor's terminal
ty input <id> "yes"         # Send text and press Enter
ty input <id> --enter       # Just press Enter (confirm a prompt)
ty input <id> --key Down --enter  # Press Down arrow then Enter
```

This is useful when:
- The executor is waiting for permission (just send `--enter`)
- You need to respond to a TUI prompt (use `--key` for navigation)
- You want to see what's happening without attaching to tmux

### 4. Create New Tasks

```bash
ty create "Implement feature X" --body "Detailed description here..."
```

Optional flags:
- `--project <name>` - Associate with a project
- `--type <type>` - Task type (bug, feature, etc.)
- `--dangerous` - Skip permission prompts in executor

### 5. Manage Priority

Pin important tasks to the top:

```bash
ty pin <id>           # Pin
ty pin <id> --unpin   # Unpin
ty pin <id> --toggle  # Toggle
```

### 6. Change Status Manually

Move tasks between columns:

```bash
ty status <id> backlog     # Move to backlog
ty status <id> queued      # Queue for execution
ty status <id> done        # Mark complete
```

### 7. Close and Cleanup

```bash
ty close <id>    # Mark as done
ty delete <id>   # Permanently remove
```

## JSON Output for Automation

All commands support `--json` for machine-readable output:

```bash
ty board --json              # Full board snapshot
ty list --status blocked --json  # Filtered list
ty show 42 --json --logs     # Task with execution logs
```

Pipe to `jq` for processing:

```bash
ty board --json | jq '.columns.backlog.tasks[:3]'  # First 3 backlog items
ty list --json | jq '.[] | select(.pinned == true)'  # Pinned tasks only
```

## Orchestration Patterns

### Continuous Monitoring Loop

```bash
while true; do
  ty board --json | jq -r '.columns.blocked.tasks[].id' | while read id; do
    echo "Task $id is blocked - needs attention"
  done
  sleep 30
done
```

### Auto-Execute Queued Tasks

```bash
ty list --status queued --json | jq -r '.[0].id' | xargs -I{} ty execute {}
```

### Batch Status Updates

```bash
ty list --status backlog --json | jq -r '.[].id' | xargs -n1 ty status {} queued
```

## Best Practices

1. **Always check the board first** - Understand context before acting
2. **Use JSON output** - Structured data is easier to process and reason about
3. **Provide meaningful feedback** - When retrying blocked tasks, give clear context
4. **Monitor execution** - Use `ty show <id> --logs` to track progress
5. **Respect priorities** - Check pinned tasks first
6. **Create focused tasks** - One clear objective per task works best

## Task Lifecycle

```
backlog → queued → processing → done
                 ↘ blocked (needs input)
```

- **backlog**: Created but not started
- **queued**: Waiting for executor pickup
- **processing**: Actively being worked on
- **blocked**: Waiting for user input/clarification
- **done**: Completed successfully

## Integration Tips

### With External Schedulers

Task You doesn't have built-in recurring tasks. Use cron or external schedulers:

```bash
# Run daily standup at 9am
0 9 * * * ty create "Daily standup review" && ty execute $(ty list --status backlog --json | jq -r '.[0].id')
```

### With Other AI Agents

This skill works with any agent that can execute shell commands:
- **Claude Code**: Native integration via hooks
- **Codex**: Use `codex exec "ty board --json && ..."`
- **Gemini**: Use `gemini code "Run ty board --json and summarize"`
- **OpenCode/Pi**: Same pattern - shell out to `ty` CLI

### Dashboard Mode

For a rolling view of the board:

```bash
watch -n30 "ty board"
```

## Troubleshooting

### Task stuck in processing?

Check if the executor is running and what it's doing:
```bash
ty show <id> --logs
ty output <id>  # See live terminal output
```

### Executor waiting for input?

Check what it's asking and respond directly:
```bash
ty question <id>           # See the question
ty input <id> --enter      # Confirm a prompt
ty input <id> "yes"        # Send specific input
```

### No tasks executing?

Ensure the daemon is running:
```bash
ty daemon status
ty daemon restart  # If needed
```

### Can't find a task?

Search by status:
```bash
ty list --status done --json | jq '.[] | select(.title | contains("keyword"))'
```

## MCP Tools (When Running Inside Task You)

If you're running as the task executor, you have access to MCP tools:

- `workflow_complete` - Mark your task complete
- `workflow_needs_input` - Request user input (blocks task)
- `workflow_show_task` - Get your task details
- `workflow_create_task` - Create follow-up tasks
- `workflow_list_tasks` - See other active tasks

Use these instead of CLI when executing inside a Task You worktree.

---
name: taskyou
description: "Install and manage TaskYou — a personal task management system with Kanban board, background AI execution, and git worktree isolation. Guides installation if not present, then orchestrates tasks via CLI."
homepage: https://github.com/bborn/taskyou
---

# TaskYou — Install & Manage

You are an autonomous orchestrator for **TaskYou**, a personal task management system with a Kanban board, background AI execution, and git worktree isolation.

## First: Check Installation

Before running any commands, verify TaskYou is installed:

```bash
which ty || echo "NOT_INSTALLED"
```

### If NOT installed, guide the user through installation:

**Quick install (recommended):**

```bash
curl -fsSL taskyou.dev/install.sh | bash
```

This auto-detects OS/arch (macOS and Linux, amd64 and arm64) and installs `ty` to `~/.local/bin/`.

**Options:**
- `--no-ssh-server` — Skip installing taskd (the SSH daemon for remote access)
- Set `INSTALL_DIR=/custom/path` to change install location

**After install, verify:**

```bash
ty --version
```

If `~/.local/bin` is not in PATH, tell the user to add it:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

**Upgrading an existing install:**

```bash
ty upgrade
```

### If already installed, proceed to task management below.

---

## CLI Reference

Use `ty` (short) or `taskyou` (full) — both work identically.

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
| See executor output | `ty output <id>` |
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

### 4. Direct Executor Interaction

For running/blocked tasks, you can interact directly with the executor:

```bash
# See what the executor is doing/asking
ty output <id>              # Capture recent terminal output
ty output <id> --lines 100  # More history

# Send input directly to the executor's terminal
ty input <id> "yes"         # Send text and press Enter
ty input <id> --enter       # Just press Enter (confirm a prompt)
ty input <id> --key Down --enter  # Press Down arrow then Enter
```

This is useful when:
- The executor is waiting for permission (just send `--enter`)
- You need to respond to a TUI prompt (use `--key` for navigation)
- You want to see what's happening without attaching to tmux

### 5. Create New Tasks

```bash
ty create "Implement feature X" --body "Detailed description here..."
```

Optional flags:
- `--project <name>` — Associate with a project
- `--type <type>` — Task type (bug, feature, etc.)
- `--dangerous` — Skip permission prompts in executor

### 6. Manage Priority

Pin important tasks to the top:

```bash
ty pin <id>           # Pin
ty pin <id> --unpin   # Unpin
ty pin <id> --toggle  # Toggle
```

### 7. Change Status

Move tasks between columns:

```bash
ty status <id> backlog     # Move to backlog
ty status <id> queued      # Queue for execution
ty status <id> done        # Mark complete
```

### 8. Close and Cleanup

```bash
ty close <id>    # Mark as done
ty delete <id>   # Permanently remove
```

## JSON Output

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

### Auto-Execute Queued Tasks

```bash
ty list --status queued --json | jq -r '.[0].id' | xargs -I{} ty execute {}
```

### Continuous Monitoring

```bash
while true; do
  ty board --json | jq -r '.columns.blocked.tasks[].id' | while read id; do
    echo "Task $id is blocked - needs attention"
  done
  sleep 30
done
```

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

## MCP Tools (When Running Inside TaskYou)

If you're running as the task executor inside a TaskYou worktree, you have access to MCP tools:

- `taskyou_complete` — Mark your task complete
- `taskyou_needs_input` — Request user input (blocks task)
- `taskyou_show_task` — Get your task details
- `taskyou_create_task` — Create follow-up tasks
- `taskyou_list_tasks` — See other active tasks
- `taskyou_spotlight` — Sync worktree changes to main repo for testing

Use these instead of CLI when executing inside a TaskYou worktree.

## Best Practices

1. **Always check the board first** — Understand context before acting
2. **Use JSON output** — Structured data is easier to process and reason about
3. **Provide meaningful feedback** — When retrying blocked tasks, give clear context
4. **Monitor execution** — Use `ty show <id> --logs` to track progress
5. **Respect priorities** — Check pinned tasks first
6. **Create focused tasks** — One clear objective per task works best

## Troubleshooting

### Task stuck in processing?

```bash
ty show <id> --logs
ty output <id>  # See live terminal output
```

### No tasks executing?

```bash
ty daemon status
ty daemon restart  # If needed
```

### Can't find a task?

```bash
ty list --status done --json | jq '.[] | select(.title | contains("keyword"))'
```

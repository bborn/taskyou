# AGENTS.md

Guide for AI agents working in this repository.

## Project Overview

This is **Bruno's Task Queue** - a personal task management system with:
- **SQLite storage** for tasks
- **SSH-accessible TUI** via Wish
- **Background executor** running Claude Code for task processing
- **Beautiful terminal UI** built with Charm libraries

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Hetzner Server                        │
│                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌───────────┐ │
│  │   Wish SSH   │───▶│  Bubble Tea  │───▶│  SQLite   │ │
│  │   :2222      │    │     TUI      │    │ tasks.db  │ │
│  └──────────────┘    └──────┬───────┘    └───────────┘ │
│                             │                           │
│                             ▼                           │
│                    ┌─────────────────┐                  │
│                    │ Background      │                  │
│                    │ Executor        │                  │
│                    │ (goroutine)     │                  │
│                    └────────┬────────┘                  │
│                             │                           │
│                             ▼                           │
│                      Claude Code                        │
└─────────────────────────────────────────────────────────┘

Access: ssh -p 2222 user@server
```

## Repository Structure

```
workflow/
├── cmd/
│   ├── task/
│   │   └── main.go              # Local CLI
│   └── taskd/
│       └── main.go              # SSH daemon + executor
├── internal/
│   ├── db/
│   │   ├── sqlite.go            # Database connection, migrations
│   │   └── tasks.go             # Task CRUD operations
│   ├── executor/
│   │   └── executor.go          # Background Claude runner
│   ├── server/
│   │   └── ssh.go               # Wish SSH server
│   └── ui/
│       ├── app.go               # Main Bubble Tea app
│       ├── dashboard.go         # Task list view
│       ├── detail.go            # Task detail with logs
│       ├── form.go              # New task form (Huh)
│       ├── watch.go             # Real-time execution watcher
│       └── styles.go            # Lip Gloss styles
├── scripts/                     # Legacy bash scripts
├── go.mod
├── go.sum
└── Makefile
```

## Building

```bash
make build          # Build bin/task and bin/taskd
make build-task     # Build just the CLI
make build-taskd    # Build just the daemon
make install        # Install to ~/go/bin
```

## Running

### Local CLI
```bash
./bin/task                           # Launch TUI
./bin/task list                      # List tasks
./bin/task create "Fix bug" -p ol    # Create task
./bin/task queue 42                  # Queue for execution
./bin/task run 42                    # Run immediately (blocking)
```

### Daemon (on server)
```bash
./bin/taskd                          # Start SSH server on :2222
./bin/taskd -addr :22222             # Custom port
./bin/taskd -db /path/to/tasks.db    # Custom database
```

### SSH Access
```bash
ssh -p 2222 user@server              # Opens TUI directly
```

## Database Schema

```sql
CREATE TABLE tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    body TEXT DEFAULT '',
    status TEXT DEFAULT 'backlog',   -- backlog, in_progress, blocked, done
    type TEXT DEFAULT '',            -- code, writing, thinking
    project TEXT DEFAULT '',         -- offerlab, influencekit, personal
    priority TEXT DEFAULT 'normal',  -- high, normal, low
    created_at DATETIME,
    updated_at DATETIME,
    started_at DATETIME,
    completed_at DATETIME
);

CREATE TABLE task_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER REFERENCES tasks(id),
    line_type TEXT,                  -- system, text, tool, error, output
    content TEXT,
    created_at DATETIME
);

CREATE TABLE project_memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'general',  -- pattern, context, decision, gotcha, general
    content TEXT NOT NULL,
    source_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    created_at DATETIME,
    updated_at DATETIME
);
```

Database location: `~/.local/share/task/tasks.db` (configurable via `TASK_DB_PATH`)

## Task Lifecycle

```
backlog → in_progress → done
                ↘ blocked (needs input)
```

1. **backlog** - Created but not started
2. **in_progress** - Executor is running Claude
3. **blocked** - Needs clarification/input
4. **done** - Completed

## Key Bindings (TUI)

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate tasks |
| `Enter` | View task details |
| `n` | New task |
| `x` | Execute (queue) selected task |
| `c` | Close selected task |
| `d` | Delete selected task |
| `w` | Watch current execution |
| `m` | Project memories |
| `s` | Settings |
| `r` | Retry task |
| `R` | Refresh list |
| `q` | Quit / Back |

## Charm Libraries Used

- **Bubble Tea** - TUI framework (Elm architecture)
- **Bubbles** - Components (list, viewport, spinner)
- **Lip Gloss** - Styling
- **Glamour** - Markdown rendering
- **Huh** - Forms
- **Wish** - SSH server

## Executor

The background executor (`internal/executor/executor.go`):
- Polls for `in_progress` tasks every 2 seconds
- Runs Claude Code with task-specific prompts
- Streams output to `task_logs` table
- Supports real-time watching via subscriptions
- Handles `TASK_COMPLETE` and `NEEDS_INPUT` signals

### Claude Prompts

Prompts are built based on task type:
- **code** - Full dev instructions (explore, implement, test, commit)
- **writing** - Content generation
- **thinking** - Analysis and strategy

### Project Memories

Project memories provide persistent context that carries across tasks. When a task runs, relevant memories for that project are injected into the prompt.

**Memory categories:**
- `pattern` - Code patterns and conventions
- `context` - Project-specific context
- `decision` - Architectural decisions
- `gotcha` - Known pitfalls and workarounds
- `general` - General notes

**Managing memories:**
- Press `m` in the TUI to open the memories view
- Filter by project using `p` or `tab`
- Create (`n`), edit (`e`), or delete (`d`) memories
- Memories are automatically included in executor prompts

## Deployment

### On Hetzner VPS

1. Build for Linux:
   ```bash
   make release
   scp bin/taskd-linux-amd64 server:~/taskd
   ```

2. Create systemd service:
   ```ini
   [Unit]
   Description=Task Queue Daemon
   After=network.target

   [Service]
   ExecStart=/home/runner/taskd
   WorkingDirectory=/home/runner
   User=runner
   Restart=always

   [Install]
   WantedBy=multi-user.target
   ```

3. Configure SSH keys for auth (in `internal/server/ssh.go`)

### SSH Key Auth

Currently accepts all keys. To restrict:
```go
wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
    // Compare key fingerprint against allowed list
    return allowed[ssh.FingerprintSHA256(key)]
})
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TASK_DB_PATH` | SQLite database path | `~/.local/share/task/tasks.db` |

## Legacy Files

The `scripts/` directory contains the old bash implementation:
- `task.sh` - Original CLI (uses GitHub Issues)
- `setup-runner.sh` - Server provisioning
- `setup-labels.sh` - GitHub label setup

These are kept for reference but no longer used.

# AGENTS.md

Guide for AI agents working in this repository.

## Project Overview

This is **Bruno's Task Queue** - a personal task management system with:
- **SQLite storage** for tasks
- **SSH-accessible TUI** via Wish
- **Background executor** running Claude Code for task processing
- **Beautiful terminal UI** built with Charm libraries (Kanban board)

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
│   │   └── main.go              # Local CLI + TUI
│   └── taskd/
│       └── main.go              # SSH daemon + executor
├── internal/
│   ├── config/
│   │   └── config.go            # Configuration management
│   ├── db/
│   │   ├── sqlite.go            # Database connection, migrations
│   │   └── tasks.go             # Task CRUD operations
│   ├── executor/
│   │   ├── executor.go          # Background Claude runner
│   │   └── triage.go            # Task pre-processing
│   ├── hooks/
│   │   └── hooks.go             # Task lifecycle hooks
│   ├── server/
│   │   └── ssh.go               # Wish SSH server
│   └── ui/
│       ├── app.go               # Main Bubble Tea app
│       ├── kanban.go            # Kanban board view
│       ├── detail.go            # Task detail with logs
│       ├── form.go              # New task form (Huh)
│       ├── watch.go             # Real-time execution watcher
│       ├── memories.go          # Project memories view
│       ├── settings.go          # Settings view
│       └── styles.go            # Lip Gloss styles
├── scripts/
│   └── install-service.sh       # Systemd service installer
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
make test           # Run tests
```

## Running

### Local CLI
```bash
./bin/task -l                        # Launch TUI locally
./bin/task daemon                    # Start background executor
./bin/task daemon stop               # Stop daemon
./bin/task daemon status             # Check daemon status
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
    status TEXT DEFAULT 'pending',   -- pending, queued, processing, ready, blocked, closed
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

CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    aliases TEXT DEFAULT '',
    instructions TEXT DEFAULT '',
    created_at DATETIME
);

CREATE TABLE project_memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'general',
    content TEXT NOT NULL,
    source_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    created_at DATETIME,
    updated_at DATETIME
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

Database location: `~/.local/share/task/tasks.db` (configurable via `WORKTREE_DB_PATH`)

## Task Lifecycle

```
pending → queued → processing → ready
                 ↘ blocked (needs input)
                 
Any state → closed (manual)
```

1. **pending** - Created but not queued
2. **queued** - Waiting in background queue
3. **processing** - Executor is running Claude
4. **ready** - Completed successfully
5. **blocked** - Needs clarification/input
6. **closed** - Done and archived

## Key Bindings (TUI)

| Key | Action |
|-----|--------|
| `←/→` or `h/l` | Navigate columns |
| `↑/↓` or `j/k` | Navigate tasks |
| `Enter` | View task details |
| `n` | New task |
| `x` | Execute (queue) selected task |
| `c` | Close selected task |
| `d` | Delete selected task |
| `w` | Watch current execution |
| `/` | Filter tasks |
| `m` | Project memories |
| `s` | Settings |
| `r` | Retry task |
| `R` | Refresh list |
| `?` | Toggle help |
| `q` | Quit / Back |

## Charm Libraries Used

- **Bubble Tea** - TUI framework (Elm architecture)
- **Bubbles** - Components (help, textinput, viewport)
- **Lip Gloss** - Styling and layout
- **Glamour** - Markdown rendering
- **Huh** - Forms
- **Wish** - SSH server

## Executor

The background executor (`internal/executor/executor.go`):
- Polls for `queued` tasks every 2 seconds
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

## Deployment

### On Hetzner VPS

1. Build for Linux:
   ```bash
   GOOS=linux GOARCH=amd64 go build -o bin/taskd-linux ./cmd/taskd
   scp bin/taskd-linux server:~/taskd
   ```

2. Install systemd service:
   ```bash
   ./scripts/install-service.sh
   ```

### SSH Key Auth

Currently accepts all keys. To restrict, edit `internal/server/ssh.go`:
```go
wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
    // Compare key fingerprint against allowed list
    return allowed[ssh.FingerprintSHA256(key)]
})
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `WORKTREE_DB_PATH` | SQLite database path | `~/.local/share/task/tasks.db` |

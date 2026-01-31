# AGENTS.md

Guide for AI agents working in this repository.

**See also:** [DEVELOPMENT.md](DEVELOPMENT.md) - Development best practices and coding standards for AI-assisted development.

## Project Overview

This is **Task You** - a personal task management system with:
- **SQLite storage** for tasks and projects
- **SSH-accessible TUI** via Wish
- **Background executor** with support for multiple AI coding agents (Claude, Codex, Gemini, Pi, OpenClaw, OpenCode)
- **Beautiful terminal UI** built with Charm libraries (Kanban board)
- **Git worktree isolation** for parallel task execution
- **Task lifecycle hooks** for real-time task state tracking

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Local / Server                            │
│                                                                  │
│  ┌──────────────┐    ┌──────────────┐    ┌───────────────────┐ │
│  │   Wish SSH   │───▶│  Bubble Tea  │───▶│      SQLite       │ │
│  │   :2222      │    │     TUI      │    │    tasks.db       │ │
│  └──────────────┘    └──────┬───────┘    └───────────────────┘ │
│                             │                                   │
│                             ▼                                   │
│                    ┌─────────────────┐                          │
│                    │   Background    │                          │
│                    │    Executor     │                          │
│                    │  (goroutine)    │                          │
│                    └────────┬────────┘                          │
│                             │                                   │
│                             ▼                                   │
│                    ┌─────────────────┐                          │
│                    │  Claude Code    │                          │
│                    │  (in tmux)      │                          │
│                    └─────────────────┘                          │
└─────────────────────────────────────────────────────────────────┘

Local:  ty -l (launches TUI + daemon)
Remote: ssh -p 2222 user@server
```

## Repository Structure

```
workflow/
├── cmd/
│   ├── task/
│   │   └── main.go              # Local CLI + TUI + daemon management
│   └── taskd/
│       └── main.go              # SSH daemon + executor
├── internal/
│   ├── config/
│   │   └── config.go            # Configuration management
│   ├── db/
│   │   ├── sqlite.go            # Database connection, migrations
│   │   └── tasks.go             # Task CRUD operations
│   ├── executor/
│   │   ├── executor.go          # Background Claude runner + hooks
│   │   └── project_config.go    # .taskyou.yml configuration
│   ├── github/
│   │   └── github.go            # PR status tracking
│   ├── hooks/
│   │   └── hooks.go             # Task lifecycle hooks
│   ├── mcp/
│   │   └── mcp.go               # MCP server integration
│   ├── server/
│   │   └── ssh.go               # Wish SSH server
│   └── ui/
│       ├── app.go               # Main Bubble Tea app + key bindings
│       ├── kanban.go            # Kanban board view (4 columns)
│       ├── detail.go            # Task detail view with logs
│       ├── form.go              # New/edit task forms (Huh)
│       ├── retry.go             # Retry task with feedback
│       ├── settings.go          # Settings view
│       ├── command_palette.go   # Quick task navigation
│       ├── attachments.go       # File attachment handling
│       ├── filebrowser.go       # File browser component
│       ├── theme.go             # Theme configuration
│       ├── styles.go            # Lip Gloss styles
│       └── url_parser.go        # URL detection in text
├── scripts/
│   └── install-service.sh       # Systemd service installer
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Building

```bash
make build          # Build bin/ty and bin/taskd
make build-ty       # Build just the CLI
make build-taskd    # Build just the daemon
make install        # Install to ~/go/bin
make test           # Run tests
make lint           # Run linter
make fmt            # Format code
```

## Running

### Local CLI
```bash
./bin/ty -l                        # Launch TUI locally (starts daemon)
./bin/ty daemon                    # Start background executor
./bin/ty daemon stop               # Stop daemon
./bin/ty daemon status             # Check daemon status
./bin/ty daemon restart            # Restart daemon
./bin/ty claudes list              # List running Claude sessions
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
    status TEXT DEFAULT 'backlog',   -- backlog, queued, processing, blocked, done
    type TEXT DEFAULT '',            -- Customizable via task_types table
    project TEXT DEFAULT '',
    worktree_path TEXT DEFAULT '',   -- Path to git worktree
    branch_name TEXT DEFAULT '',     -- Git branch name
    port INTEGER DEFAULT 0,          -- Unique port (3100-4099)
    claude_session_id TEXT DEFAULT '',-- For resuming Claude conversations
    daemon_session TEXT DEFAULT '',  -- tmux session name
    dangerous_mode INTEGER DEFAULT 0,-- Skip Claude permissions
    pr_url TEXT DEFAULT '',          -- Pull request URL
    pr_number INTEGER DEFAULT 0,     -- Pull request number
    scheduled_at DATETIME,           -- When to next run
    recurrence TEXT DEFAULT '',      -- Deprecated recurrence pattern (kept for legacy display)
    last_run_at DATETIME,            -- Last scheduled execution time
    created_at DATETIME,
    updated_at DATETIME,
    started_at DATETIME,
    completed_at DATETIME
);

CREATE TABLE task_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    line_type TEXT DEFAULT 'output', -- system, text, tool, error, output
    content TEXT NOT NULL,
    created_at DATETIME
);

CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    aliases TEXT DEFAULT '',
    instructions TEXT DEFAULT '',    -- Project-specific AI instructions
    actions TEXT DEFAULT '[]',       -- Custom actions
    color TEXT DEFAULT '',           -- Hex color for label
    created_at DATETIME
);

CREATE TABLE task_types (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    instructions TEXT DEFAULT '',    -- Type-specific AI instructions
    sort_order INTEGER DEFAULT 0,
    is_builtin INTEGER DEFAULT 0,
    created_at DATETIME
);

CREATE TABLE task_attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    mime_type TEXT DEFAULT '',
    size INTEGER DEFAULT 0,
    data BLOB NOT NULL,
    created_at DATETIME
);

CREATE TABLE task_compaction_summaries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    trigger TEXT NOT NULL,
    pre_tokens INTEGER DEFAULT 0,
    summary TEXT NOT NULL,
    custom_instructions TEXT DEFAULT '',
    created_at DATETIME
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

Database location: `~/.local/share/task/tasks.db` (configurable via `WORKTREE_DB_PATH`)

## Task Lifecycle

```
backlog → queued → processing → done
                 ↘ blocked (needs input)

blocked can return to processing via retry
```

1. **backlog** - Created but not yet started
2. **queued** - Waiting in executor queue
3. **processing** - Claude is actively executing
4. **blocked** - Waiting for user input/clarification
5. **done** - Completed successfully

Recurring scheduling has been removed from TaskYou. Use external schedulers (cron, calendar apps, etc.) to invoke the CLI whenever a task should repeat.

## Key Bindings (TUI)

| Key | Action |
|-----|--------|
| `←/→` or `h/l` | Navigate columns |
| `↑/↓` or `j/k` | Navigate tasks |
| `Enter` | View task details |
| `n` | New task |
| `e` | Edit task |
| `x` | Execute (queue) selected task |
| `r` | Retry task with feedback |
| `c` | Close selected task |
| `d` | Delete selected task |
| `t` | Pin/unpin selected task |
| `s` | Settings |
| `S` | Change task status |
| `R` | Refresh list |
| `p` / `ctrl+p` | Command palette (go to task) |
| `!` | Toggle dangerous mode |
| `B` | Focus Backlog column |
| `P` | Focus In Progress column |
| `L` | Focus Blocked column |
| `D` | Focus Done column |
| `?` | Toggle help |
| `q` / `esc` | Back / Quit |
| `ctrl+c` | Force quit |

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
- Creates isolated git worktrees for each task
- Runs AI coding agents in tmux windows with hooks
- Streams output to `task_logs` table
- Supports real-time watching via subscriptions
- Handles task suspension and resumption
- Manages scheduled tasks (recurring runs must now be handled externally via CLI automation)

### Supported Executors

TaskYou supports multiple AI coding agent backends:

- **claude** (default) - Claude Code CLI with hooks and session resume
- **codex** - OpenAI Codex CLI with dangerous mode support
- **gemini** - Google Gemini CLI with configurable dangerous mode flags
- **pi** - Pi coding agent with session continuity
- **openclaw** - OpenClaw AI assistant
- **opencode** - OpenCode AI assistant

Each task can specify its executor in the task form. The executor runs in an isolated git worktree with environment variables for task context.

### Executor Integration (Claude Example)

Tasks run in tmux windows with hooks that track state:

- **PreToolUse** - Before tool execution, ensures task is "processing"
- **PostToolUse** - After tool completes, ensures task stays "processing"
- **Notification** - When idle or needs permission, marks task "blocked"
- **Stop** - When Claude finishes responding, updates state accordingly
- **PreCompact** - Before context compaction, saves transcript to DB

### Worktree Isolation

Each task gets an isolated git worktree:
```
~/.local/share/task/worktrees/{project}/task-{id}
```

Environment variables available in worktrees:
- `WORKTREE_TASK_ID` - Task identifier
- `WORKTREE_PORT` - Unique port (3100-4099)
- `WORKTREE_PATH` - Worktree directory path
- `WORKTREE_SESSION_ID` - tmux session ID
- `WORKTREE_DANGEROUS_MODE` - If permissions are skipped

### Task Prompts

Prompts are built with:
1. Task-type-specific instructions
2. Project-specific instructions
3. Previous conversation history (on retry/resume)
4. File attachments
5. Task title and body

## Deployment

### On Hetzner VPS

1. Build for Linux:
   ```bash
   make build-linux
   ```

2. Deploy to server:
   ```bash
   make deploy
   ```

3. Install systemd service:
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
| `TASK_EXECUTOR` | Overrides the executor name shown in the UI (e.g., `codex`) | `Claude` |
| `WORKTREE_TASK_ID` | Current task ID (set by executor) | - |
| `WORKTREE_PORT` | Task-specific port | - |
| `WORKTREE_PATH` | Worktree directory | - |
| `WORKTREE_SESSION_ID` | tmux session ID | - |
| `WORKTREE_DANGEROUS_MODE` | Skip Claude permissions | - |
| `WORKTREE_CWD` | Working directory for project detection | - |
| `GEMINI_DANGEROUS_ARGS` | Overrides the Gemini CLI flags used when dangerous mode is enabled | `--dangerously-allow-run` |

Set `TASK_EXECUTOR` before launching `task -l`/`task daemon` to change the executor label shown in the UI (e.g., `TASK_EXECUTOR=codex`). For compatibility, `WORKFLOW_EXECUTOR`, `TASKYOU_EXECUTOR`, and `WORKTREE_EXECUTOR` are also recognized.

## Project Configuration

Create `.taskyou.yml` in your project root:

```yaml
worktree:
  init_script: bin/worktree-setup
```

The init script runs after worktree creation for setup tasks like:
- Installing dependencies
- Setting up databases
- Copying configuration files
- Running migrations

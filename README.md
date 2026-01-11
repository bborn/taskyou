# Task You

A personal task management system with a beautiful terminal UI, SQLite storage, and background task execution via Claude Code.

## Features

- **Kanban Board** - Visual task management with 4 columns (Backlog, In Progress, Blocked, Done)
- **Git Worktrees** - Each task runs in an isolated worktree, no conflicts between parallel tasks
- **Background Executor** - Claude Code processes tasks automatically
- **Project Memories** - Persistent context that carries across tasks
- **Real-time Updates** - Watch tasks execute live
- **SSH Access** - Connect from anywhere via `ssh -p 2222 server`

## Installation

```bash
git clone https://github.com/bborn/workflow
cd workflow
make build
```

## Usage

### Run locally

```bash
# Launch the TUI (auto-starts background daemon)
./bin/task -l
```

### Run on server

```bash
# Start the SSH server + executor
./bin/taskd -addr :2222

# Connect from anywhere
ssh -p 2222 your-server
```

### Daemon management

```bash
./bin/task daemon         # Start daemon manually
./bin/task daemon stop    # Stop the daemon
./bin/task daemon status  # Check daemon status
```

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `←/→` or `h/l` | Navigate columns |
| `↑/↓` or `j/k` | Navigate tasks |
| `Enter` | View task details |
| `n` | Create new task |
| `x` | Execute (queue) task |
| `r` | Retry task with feedback |
| `c` | Close task |
| `d` | Delete task |
| `o` | Open task's working directory |
| `f` | View/manage attachments |
| `/` | Filter tasks |
| `m` | Project memories |
| `s` | Settings |
| `?` | Toggle help |
| `q` | Quit |

## Task Lifecycle

```
backlog → queued → processing → done
                 ↘ blocked (needs input)
```

| Status | Description |
|--------|-------------|
| `backlog` | Created but not started |
| `queued` | Waiting to be processed |
| `processing` | Currently being executed |
| `blocked` | Needs input/clarification |
| `done` | Completed |

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TASK_DB_PATH` | SQLite database path | `~/.local/share/task/tasks.db` |

### Projects

Configure projects in Settings (`s`):

- **Name** - Project identifier (e.g., `myproject`)
- **Path** - Local filesystem path to git repo
- **Aliases** - Short names for quick reference
- **Instructions** - Project-specific AI instructions

### Worktrees

Tasks run in isolated git worktrees at `~/.local/share/task/worktrees/{project}/task-{id}`. This allows multiple tasks to run in parallel without conflicts. Press `o` to open a task's worktree.

### Memories

Project memories provide persistent context for the AI. Press `m` to manage.

Categories:
- `pattern` - Code patterns and conventions
- `context` - Project-specific context  
- `decision` - Architectural decisions
- `gotcha` - Known pitfalls and workarounds

## Development

```bash
make build        # Build binaries
make test         # Run tests
make install      # Install to ~/go/bin
```

## Tech Stack

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) - Terminal styling
- [Wish](https://github.com/charmbracelet/wish) - SSH server
- [SQLite](https://modernc.org/sqlite) - Local database

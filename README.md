# Task You

A personal task management system with a beautiful terminal UI, SQLite storage, and background task execution via Claude Code.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Local Machine                           │
│                                                              │
│  ┌──────────────┐                                           │
│  │  task -l     │ ──► Local TUI + SQLite                    │
│  │  (local)     │     ~/.local/share/task/tasks.db          │
│  └──────────────┘                                           │
│                                                              │
│  ┌──────────────┐    ┌──────────────────────────────────┐  │
│  │  task        │    │        Hetzner Server             │  │
│  │  (remote)    │───►│  SSH :2222 ──► Bubble Tea TUI    │  │
│  └──────────────┘    │              ──► SQLite DB        │  │
│                      │              ──► Background        │  │
│                      │                  Executor          │  │
│                      │                  (Claude Code)     │  │
│                      └──────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Features

- **Kanban Board** - Visual task management with 4 columns (Backlog, In Progress, Blocked, Done)
- **SSH Access** - Connect from anywhere via `ssh -p 2222 server`
- **Background Executor** - Claude Code processes tasks automatically
- **Project Memories** - Persistent context that carries across tasks
- **Real-time Updates** - Watch tasks execute live

## Installation

### Build from source

```bash
git clone https://github.com/bborn/workflow
cd workflow
make build
```

### Run locally

```bash
# Start the daemon (background executor)
./bin/task daemon &

# Launch the TUI
./bin/task -l
```

### Run on server

```bash
# Start the SSH server + executor
./bin/taskd -addr :2222

# Connect from anywhere
ssh -p 2222 your-server
```

## Usage

### Keyboard Shortcuts

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
| `w` | Watch execution |
| `i` | Interrupt execution |
| `/` | Filter tasks |
| `m` | Project memories |
| `s` | Settings |
| `?` | Toggle help |
| `q` | Quit |

### Task Statuses

| Status | Description | Kanban Column |
|--------|-------------|---------------|
| `backlog` | Created but not started | Backlog |
| `queued` | Waiting to be processed | In Progress |
| `processing` | Currently being executed | In Progress |
| `blocked` | Needs input/clarification | Blocked |
| `done` | Completed | Done |

### Task Lifecycle

```
backlog → queued → processing → done
                 ↘ blocked (needs input)
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `TASK_DB_PATH` | SQLite database path | `~/.local/share/task/tasks.db` |

### Projects

Configure projects in Settings (`s`):
- **Name** - Project identifier (e.g., `offerlab`)
- **Path** - Local filesystem path
- **Aliases** - Short names (e.g., `ol` for `offerlab`)
- **Instructions** - Project-specific AI instructions

### Memories

Project memories provide persistent context for the AI. Press `m` to manage.

**Categories:**
- `pattern` - Code patterns and conventions
- `context` - Project-specific context
- `decision` - Architectural decisions
- `gotcha` - Known pitfalls and workarounds

## Deployment

### Systemd Service

```bash
# Install service (creates taskd.service)
./scripts/install-service.sh

# Manage
sudo systemctl start taskd
sudo systemctl enable taskd
sudo systemctl status taskd
```

### SSH Key Authentication

Edit `internal/server/ssh.go` to restrict access:

```go
wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
    allowed := map[string]bool{
        "SHA256:...": true,  // Your key fingerprint
    }
    return allowed[ssh.FingerprintSHA256(key)]
})
```

## Development

```bash
make build        # Build binaries
make test         # Run tests
make install      # Install to ~/go/bin
```

## Tech Stack

- **Go** - Core application
- **Bubble Tea** - TUI framework (Elm architecture)
- **Bubbles** - TUI components (help, textinput, viewport)
- **Lip Gloss** - Terminal styling
- **Huh** - Form components
- **Wish** - SSH server
- **SQLite** - Local database (via modernc.org/sqlite)

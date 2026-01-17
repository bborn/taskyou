# Task You

A personal task management system with a beautiful terminal UI, SQLite storage, and background task execution via pluggable AI agents (Claude Code or OpenAI Codex CLI).

## Screenshots

### Kanban Board
![Kanban Board](docs/screenshots/kanban-board.jpeg)
*The main view showing tasks organized across Backlog, In Progress, Blocked, and Done columns*

### Task Detail View
![Task Detail View](docs/screenshots/task-detail-view.jpeg)
*Viewing a task with Claude's output and shell access in split panes*

### Execution Log
![Execution Log](docs/screenshots/execution-log.png)
*Live execution log showing task progress, worktree creation, and Claude's actions*

### New Task Form
![New Task Form](docs/screenshots/new-task-form.png)
*Creating a new task with project selection, type, scheduling, and attachments*

## Features

- **Kanban Board** - Visual task management with 4 columns (Backlog, In Progress, Blocked, Done)
- **Git Worktrees** - Each task runs in an isolated worktree, no conflicts between parallel tasks
- **Pluggable Executors** - Choose between Claude Code or OpenAI Codex CLI per task
- **Project Memories** - Persistent context that carries across tasks
- **Real-time Updates** - Watch tasks execute live
- **SSH Access** - Connect from anywhere via `ssh -p 2222 server`

## Prerequisites

- **Go 1.24.4+** - Required to build the project

### Using mise (recommended)

If you use [mise](https://mise.jdx.dev/) for dependency management, simply run:

```bash
mise install
```

This will install the correct Go version automatically.

### Manual installation

Install Go 1.24.4 or later from [go.dev/dl](https://go.dev/dl/).

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

## Task Executors

Task You supports multiple AI executors for processing tasks. You can choose the executor when creating or editing a task.

| Executor | CLI | Description |
|----------|-----|-------------|
| Claude (default) | `claude` | [Claude Code](https://claude.ai/claude-code) - Anthropic's coding agent with session resumption |
| Codex | `codex` | [OpenAI Codex CLI](https://github.com/openai/codex) - OpenAI's coding assistant |

Both executors run in tmux windows with the same worktree isolation and environment variables. The main differences:

- **Claude Code** supports session resumption - when you retry a task, Claude continues with full conversation history
- **Codex** starts fresh on each execution but receives the full prompt with any feedback

### Installing Executors

At least one executor CLI must be installed for tasks to run:

```bash
# Claude Code (recommended)
# See https://claude.ai/claude-code for installation

# OpenAI Codex CLI
npm install -g @openai/codex
```

## Configuration

### Settings

Manage settings with `task settings`:

```bash
task settings                              # View all settings
task settings set <key> <value>            # Set a value
```

| Setting | Description |
|---------|-------------|
| `anthropic_api_key` | API key for ghost text autocomplete (optional, uses API credits) |
| `autocomplete_enabled` | Enable/disable autocomplete (`true`/`false`) |

### Ghost Text Autocomplete

When creating or editing tasks, ghost text suggestions appear as you type. This feature uses the Anthropic API directly for fast completions.

**Setup:**
```bash
task settings set anthropic_api_key sk-ant-your-key-here
```

Get an API key at [console.anthropic.com](https://console.anthropic.com/). This is optional and uses your API credits.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `WORKTREE_DB_PATH` | SQLite database path | `~/.local/share/task/tasks.db` |
| `ANTHROPIC_API_KEY` | Fallback for autocomplete if not set in settings | - |

### `.taskyou.yml` Configuration

You can configure per-project settings by creating a `.taskyou.yml` file in your project root:

```yaml
worktree:
  init_script: bin/worktree-setup
```

**Supported filenames** (in order of precedence):
- `.taskyou.yml`
- `.taskyou.yaml`
- `taskyou.yml`
- `taskyou.yaml`

**Configuration options:**

| Field | Description | Example |
|-------|-------------|---------|
| `worktree.init_script` | Path to script that runs after worktree creation (relative or absolute) | `bin/worktree-setup` |

### Projects

Configure projects in Settings (`s`):

- **Name** - Project identifier (e.g., `myproject`)
- **Path** - Local filesystem path to git repo
- **Aliases** - Short names for quick reference
- **Instructions** - Project-specific AI instructions

### Worktrees

Tasks run in isolated git worktrees at `~/.local/share/task/worktrees/{project}/task-{id}`. This allows multiple tasks to run in parallel without conflicts. Press `o` to open a task's worktree.

#### Worktree Setup Script

You can configure a script to run automatically after each worktree is created. This is useful for:
- Installing dependencies
- Setting up databases
- Copying configuration files
- Running migrations

**Two ways to configure:**

1. **Conventional location** - Create an executable script at `bin/worktree-setup`:
```bash
#!/bin/bash
# Example: bin/worktree-setup
bundle install
cp config/database.yml.example config/database.yml
```

2. **Custom location** - Specify in `.taskyou.yml`:
```yaml
worktree:
  init_script: scripts/my-setup.sh
```

The script runs in the worktree directory and has access to all worktree environment variables (`WORKTREE_TASK_ID`, `WORKTREE_PORT`, `WORKTREE_PATH`).

### Running Applications in Worktrees

Each task provides environment variables that applications can use to run in isolation:

| Variable | Description | Example |
|----------|-------------|---------|
| `WORKTREE_TASK_ID` | Unique task identifier | `207` |
| `WORKTREE_PORT` | Unique port (3100-4099) | `3100` |
| `WORKTREE_PATH` | Path to the worktree | `/path/to/project/.task-worktrees/207-my-task` |

These variables allow multiple tasks to run simultaneously without conflicts on ports or databases.

#### Example: Rails Application

Configure your Rails app to use worktree variables for complete isolation:

**config/puma.rb:**
```ruby
port ENV.fetch("WORKTREE_PORT", 3000)
```

**config/database.yml:**
```yaml
development:
  database: myapp_dev<%= ENV['WORKTREE_TASK_ID'] ? "_task#{ENV['WORKTREE_TASK_ID']}" : "" %>
```

**Procfile.dev:**
```
web: bin/rails server -p ${WORKTREE_PORT:-3000}
```

**bin/worktree-setup:**
```bash
#!/bin/bash
set -e

# Install dependencies
bundle install

# Create isolated database for this task
bin/rails db:create db:migrate
```

Now the AI executor (Claude or Codex) can:
- Run your app with `bin/dev`
- Access it at `http://localhost:$WORKTREE_PORT`
- Work on multiple tasks in parallel without database or port conflicts

#### Example: Node.js Application

**package.json:**
```json
{
  "scripts": {
    "dev": "next dev -p ${WORKTREE_PORT:-3000}"
  }
}
```

**bin/worktree-setup:**
```bash
#!/bin/bash
npm install
cp .env.example .env.local
```

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

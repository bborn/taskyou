# Quick Start: Claude TUI Automation for Task You

This guide shows how to enable Claude Code to see and interact with the Task You TUI application.

## Prerequisites

- Node.js installed (for npx)
- Claude Code installed
- tmux installed (already required for Task You)

## Setup (5 minutes)

### 1. Add MCP Servers to Claude Code

Run these commands to add the TUI automation MCP servers:

```bash
# Add pty-mcp for TUI interaction (spawn, navigate, screenshot)
claude mcp add pty-debug -- npx -y @so2liu/pty-mcp-server@latest

# Add tmux-mcp for observing existing tmux sessions
claude mcp add tmux -- npx -y tmux-mcp
```

### 2. Verify Installation

After adding, restart Claude Code and verify the tools are available:

```
/mcp
```

You should see `pty-debug` and `tmux` listed as MCP servers.

## Usage Examples

### Testing the TUI App

When working on the Task You codebase, Claude can now test the TUI:

```
# Ask Claude to test the TUI
"Launch the task app and verify the Kanban board displays correctly"

# Claude will:
1. spawn_session({ command: "task", args: ["-l"] })
2. get_snapshot()  # Capture what's displayed
3. Analyze the output to verify correctness
```

### Navigating the Interface

```
# Claude can navigate using keyboard input
send_input({ input: "j" })           # Move down
send_input({ input: "k" })           # Move up
send_input({ specialKey: "enter" })  # Select
send_input({ input: "n" })           # New task
send_input({ input: "q" })           # Quit
send_input({ specialKey: "escape" }) # Cancel
send_input({ specialKey: "ctrl+c" }) # Interrupt
```

### Observing Running Tasks

Since Task You runs Claude tasks in tmux windows, you can observe them:

```
# List all tmux sessions
tmux.list-sessions()

# Find task windows
tmux.list-windows({ session: "task-daemon-123" })

# Capture what Claude is doing in a task
tmux.capture-pane({ paneId: "task-daemon-123:task-5" })
```

## Development Workflow

### Scenario: Fixing a UI Bug

1. **Reproduce the bug**:
   ```
   Claude spawns the TUI, navigates to trigger the bug
   ```

2. **Make the fix**:
   ```
   Claude edits the relevant Go/Bubble Tea code
   ```

3. **Rebuild**:
   ```bash
   go build -o task ./cmd/task
   ```

4. **Verify the fix**:
   ```
   Claude spawns a fresh TUI session, verifies the bug is fixed
   ```

### Scenario: Adding a New Feature

1. **Understand current behavior**:
   ```
   Claude spawns TUI, explores the interface
   ```

2. **Implement the feature**:
   ```
   Claude modifies the codebase
   ```

3. **Test the feature**:
   ```
   Claude rebuilds, spawns TUI, tests the new feature works
   ```

## Tips

### Session Management

Always close TUI sessions when done to avoid resource leaks:

```
close_session({ session_id: "..." })
```

### Screen Size

The default terminal size is usually 80x24. For a larger view:

```
resize_terminal({ session_id: "...", cols: 120, rows: 40 })
```

### Waiting for Updates

After sending input, wait a moment before capturing:

```
send_input({ input: "n" })  # Open new task form
# Wait for UI to update
get_snapshot()
```

### Debugging

If the TUI isn't responding as expected:
1. Capture a snapshot to see current state
2. Check if there's a modal/overlay blocking input
3. Try sending Escape to dismiss any dialogs

## Troubleshooting

### "pty-debug server not responding"
```bash
# Restart the MCP server
claude mcp remove pty-debug
claude mcp add pty-debug -- npx -y @so2liu/pty-mcp-server@latest
```

### "Cannot find tmux sessions"
Ensure tmux is running:
```bash
tmux list-sessions
```

### "Task app won't start"
Ensure the binary is built:
```bash
cd /path/to/workflow
go build -o task ./cmd/task
./task -l
```

## What's Next?

See [TUI_INTERACTION_RESEARCH.md](./TUI_INTERACTION_RESEARCH.md) for:
- Detailed comparison of available tools
- Advanced usage patterns
- Ideas for custom MCP server integration

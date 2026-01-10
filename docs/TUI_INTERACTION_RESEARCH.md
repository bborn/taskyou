# Research: Enabling Claude to Interact with TUI Applications

## Problem Statement

The Task You app is a TUI (Terminal User Interface) built with Go and Bubble Tea. When Claude Code works on this codebase, it cannot see or interact with the running application because:

1. Claude Code runs in its own process and cannot "see" what's displayed in other terminal windows
2. Unlike browser automation with `--chrome`, there's no built-in TUI automation for Claude
3. This makes it difficult to verify UI changes, debug issues, or understand application behavior

The goal is to enable Claude to recursively work on this task app by giving it the ability to observe and interact with the TUI.

## Research Findings

### Existing MCP Solutions for Terminal/TUI Interaction

#### 1. pty-mcp (Recommended for TUI Automation)
**Repository**: https://github.com/so2liu/pty-mcp
**Package**: `@so2liu/pty-mcp-server`

This MCP server is specifically designed for TUI debugging and allows Claude to:
- Spawn PTY sessions running any terminal program
- Send text and special keys (Ctrl+C, arrows, function keys, etc.)
- Capture screen snapshots as text
- Manage multiple concurrent sessions

**Tools provided**:
| Tool | Description |
|------|-------------|
| `spawn_session` | Start a TUI program in a PTY |
| `send_input` | Send text or special keys |
| `get_snapshot` | Capture current screen state |
| `resize_terminal` | Change terminal dimensions |
| `list_sessions` | List active sessions |
| `close_session` | Terminate a session |

**Installation**:
```bash
claude mcp add pty-debug -- npx -y @so2liu/pty-mcp-server@latest
```

**Demo use case** (Claude controlling Claude):
```
1. spawn_session({ command: "claude" })
2. send_input({ specialKey: "shift+tab" })  // cycle modes
3. send_input({ input: "what is 1+1?" })
4. send_input({ specialKey: "enter" })
5. get_snapshot()  // see Claude's response
```

**Pros**:
- Purpose-built for TUI automation
- Full keyboard support including modifiers
- Text-based screen capture (perfect for LLM consumption)
- Uses node-pty and @xterm/headless for proper terminal emulation

**Cons**:
- Node.js dependency
- Sessions are independent of existing terminal sessions

#### 2. mcp-tui-server (Rust-based, Feature-Rich)
**Repository**: https://github.com/michaellee8/mcp-tui-server

A Rust-based MCP server with 23+ tools for comprehensive TUI automation:

**Key Features**:
- Launch and manage multiple TUI sessions
- Read terminal content as plain text or accessibility-style snapshots
- Mouse interaction (click, double-click, right-click)
- Wait for screen content or idle state
- **Take PNG screenshots of terminal output**
- JavaScript scripting for complex automation
- **Session recording to asciicast format**

**Installation**:
```bash
cargo install --git https://github.com/michaellee8/mcp-tui-server
```

**Pros**:
- Most feature-rich solution
- PNG screenshots (could be useful for visual debugging)
- Asciicast recording for replay/debugging
- Native Rust performance

**Cons**:
- Requires Rust toolchain
- More complex setup

#### 3. tmux-mcp (Integrates with Existing Sessions)
**Package**: `tmux-mcp`
**Repository**: https://github.com/nickgnd/tmux-mcp

This integrates with existing tmux sessions, which is highly relevant since this app already uses tmux extensively.

**Features**:
- List and search tmux sessions
- View and navigate windows and panes
- Capture terminal content from any pane
- Execute commands in panes
- Create new sessions/windows
- Split panes

**Tools provided**:
- `list-sessions` - List all active tmux sessions
- `capture-pane` - Capture content from a tmux pane
- `execute-command` - Execute a command in a tmux pane
- `list-windows` / `list-panes` - Navigation

**Installation**:
```json
"mcpServers": {
  "tmux": {
    "command": "npx",
    "args": ["-y", "tmux-mcp"]
  }
}
```

**Pros**:
- Works with existing tmux sessions (this app already runs Claude in tmux!)
- Can observe what's happening in task-daemon sessions
- Lightweight and simple

**Cons**:
- Less comprehensive TUI automation (more observation than interaction)
- Relies on tmux being used

#### 4. mcpterm (Go-based, Simple)
**Repository**: https://github.com/dwrtz/mcpterm

A Go-based proof-of-concept with two simple tools:
- `run` - Run commands in stateful terminal sessions
- `runScreen` - Run commands and return screen output (for TUIs)

**Pros**:
- Simple and lightweight
- Written in Go (matches this codebase)

**Cons**:
- Minimal features
- Proof-of-concept quality

### Alternative Approaches

#### AppleScript + Screenshots (macOS Only)

Using AppleScript to automate Terminal.app and capture screenshots:

```applescript
tell application "Terminal"
    activate
    do script "task -l"
    delay 2
end tell

do shell script "screencapture -l $(osascript -e 'tell app \"Terminal\" to id of window 1') screenshot.png"
```

**Pros**:
- Works with any terminal emulator
- Visual screenshots (useful for UI verification)

**Cons**:
- macOS only
- Requires accessibility permissions
- Screenshots are images, not text (harder for LLM to process)
- Brittle and slow

#### tmux capture-pane (Already Available)

The app already runs Claude in tmux windows. We can capture pane content:

```bash
# Capture visible pane content
tmux capture-pane -t task-daemon-{sessionID}:task-{taskID} -p

# Capture entire scrollback
tmux capture-pane -t session:window -p -S -
```

**Pros**:
- Already integrated with the app's architecture
- Text output (LLM-friendly)
- No additional dependencies

**Cons**:
- Only captures text, no input sending
- Need to know the exact pane target

## Recommended Approach

### Primary Recommendation: pty-mcp + tmux-mcp Combination

For this specific use case, I recommend a **two-pronged approach**:

#### 1. Use `tmux-mcp` for Observing Existing Sessions
Since the app already runs Claude tasks in tmux windows (`task-daemon-*:task-*`), use tmux-mcp to:
- Observe what Claude is doing in task windows
- Capture pane output for debugging
- Monitor task execution progress

#### 2. Use `pty-mcp` for Fresh TUI Testing
When Claude needs to test the TUI app itself:
- Spawn a new PTY session running `task -l`
- Navigate the Kanban board
- Create/modify/view tasks
- Verify UI behavior

### Configuration for Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "pty-debug": {
      "command": "npx",
      "args": ["-y", "@so2liu/pty-mcp-server@latest"]
    },
    "tmux": {
      "command": "npx",
      "args": ["-y", "tmux-mcp"]
    }
  }
}
```

### Usage Workflow

1. **Claude starts working on a TUI feature**

2. **To test the feature**:
   ```
   # Spawn the task app
   pty-debug.spawn_session({ command: "task", args: ["-l"] })

   # Navigate to see the result
   pty-debug.send_input({ input: "j" })  # Move down
   pty-debug.send_input({ specialKey: "enter" })  # Select

   # Capture what's displayed
   pty-debug.get_snapshot()
   ```

3. **To observe task execution**:
   ```
   # List running task sessions
   tmux.list-sessions()

   # Capture output from a specific task
   tmux.capture-pane({ paneId: "task-daemon-123:task-5" })
   ```

## Alternative: Custom MCP Server for This App

A more integrated approach would be to build a custom MCP server specifically for this app that:

1. **Exposes the TUI state directly via the app's internal state**
   - Instead of screen scraping, expose the Bubble Tea model state
   - Read task list, selected item, current view, etc.

2. **Provides app-specific tools**:
   - `task_list` - Get all visible tasks
   - `task_select` - Select a task by ID
   - `task_create` - Create a new task
   - `view_change` - Switch views (kanban/detail/settings)
   - `get_ui_state` - Get current UI state (view, selection, etc.)

3. **Implementation approach**:
   - Add a secondary MCP server in `internal/mcp/` alongside the existing one
   - Expose Bubble Tea model state via MCP resources
   - Add tools for TUI interaction

This would be more reliable than screen scraping but requires modifying the app.

## Implementation Priority

### Phase 1: Quick Win (Recommended First Step)
1. Install `pty-mcp` and `tmux-mcp` in Claude Code configuration
2. Document usage patterns for developers
3. Test workflow with actual development tasks

### Phase 2: Enhanced Integration
1. Add tmux pane naming conventions for easier targeting
2. Create helper scripts for common operations
3. Document best practices for TUI testing

### Phase 3: Custom MCP Server (Optional)
1. Build app-specific MCP server exposing internal state
2. Add TUI interaction tools
3. Enable true recursive development capability

## Conclusion

The fastest path to enabling Claude to interact with this TUI app is:

1. **Install pty-mcp** for fresh TUI testing and interaction
2. **Install tmux-mcp** for observing existing task sessions

This requires no code changes and can be set up in minutes. The combination provides both:
- **Active testing**: Launch the TUI, navigate, interact, verify
- **Passive observation**: Monitor what Claude instances are doing in task windows

For a more robust long-term solution, consider building a custom MCP server that exposes the app's internal state directly.

## References

- [pty-mcp](https://github.com/so2liu/pty-mcp) - PTY/TUI automation MCP server
- [mcp-tui-server](https://github.com/michaellee8/mcp-tui-server) - Rust-based TUI automation
- [tmux-mcp](https://github.com/nickgnd/tmux-mcp) - tmux integration MCP server
- [mcpterm](https://github.com/dwrtz/mcpterm) - Go-based terminal MCP server
- [MCP Documentation](https://modelcontextprotocol.io/docs/develop/connect-local-servers) - Official MCP docs

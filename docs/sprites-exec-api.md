# Sprites Exec API: Opportunities for taskyou

## Context

PR #103 integrates Sprites as a cloud execution environment for Claude tasks.
PR #160 adds a web UI with per-user Sprites.

Both PRs use the `sprites-go` SDK's `CommandContext()` method, which wraps the
exec WebSocket API. However, they layer tmux on top and use file-tailing for
hook communication. The exec API has native features that eliminate both.

## Current Architecture (PR #103)

```
Local                                  Sprite VM
─────                                  ─────────
Executor                               tmux session "task-42"
  │                                      └── claude --dangerously-skip-permissions
  ├─ sprite.Command("tmux new-session")
  ├─ sprite.Command("tail -f hooks.jsonl")  ◄── hook script appends JSON
  └─ polls "tmux has-session" every 2s
```

Problems:
1. tmux is redundant — exec sessions already persist across disconnections
2. Polling tmux every 2s wastes resources and adds latency
3. File-based hook streaming is fragile (race conditions, no backpressure)
4. No reconnection support if the local machine restarts
5. Setup requires installing tmux, Node.js, Claude CLI on the sprite

## Exec API Capabilities

The exec API (WSS + HTTP POST at `/v1/sprites/{name}/exec`) provides:

| Feature | What it replaces |
|---------|-----------------|
| Persistent sessions | tmux sessions |
| `max_run_after_disconnect` | tmux detach behavior |
| Session listing (`GET /exec`) | `tmux has-session` polling |
| Session attach (`WSS /exec/{id}`) | `tmux attach` |
| Session kill (`POST /exec/{id}/kill`) | `tmux kill-session` |
| Port notifications | Manual port tracking |
| `ExitMessage` with exit code | Parsing tmux output |

Additional APIs that help:

| API | What it replaces |
|-----|-----------------|
| Filesystem (`/v1/fs/*`) | `sprite.Command("mkdir")`, `sprite.Command("cat")` |
| Services (`/v1/services/*`) | `nohup taskd &` + polling |
| FS Watch (`WS /v1/fs/watch`) | `tail -f /tmp/task-hooks.jsonl` |
| Network Policy (`/v1/policy`) | Manual iptables/allowlists |

## Recommended Changes

### 1. Replace tmux with native exec sessions

**Before (PR #103):**
```go
// Start Claude in tmux on sprite
cmd := sprite.CommandContext(ctx, "tmux", "new-session", "-d", "-s", sessionName,
    "sh", "-c", claudeCmd)
cmd.CombinedOutput()

// Poll for completion
for {
    checkCmd := sprite.CommandContext(ctx, "tmux", "has-session", "-t", sessionName)
    if err := checkCmd.Run(); err != nil {
        break // session ended
    }
    time.Sleep(2 * time.Second)
}
```

**After:**
```go
// Run Claude directly via exec API
cmd := sprite.CommandContext(ctx, "claude",
    "--dangerously-skip-permissions", "-p", prompt)
cmd.Dir = workDir
cmd.Env = []string{
    fmt.Sprintf("TASK_ID=%d", task.ID),
    fmt.Sprintf("WORKTREE_PORT=%d", task.Port),
}

// Session persists if we disconnect (key feature!)
// Set max_run_after_disconnect to keep Claude running
cmd.SetMaxRunAfterDisconnect(0) // 0 = run forever

// Stream stdout for log capture
stdout, _ := cmd.StdoutPipe()
cmd.Start()

// cmd.SessionID() gives us the session ID for reconnection
sessionID := cmd.SessionID()
db.SetTaskSessionID(task.ID, sessionID)

// Read output and wait for exit
scanner := bufio.NewScanner(stdout)
for scanner.Scan() {
    db.AppendTaskLog(task.ID, "claude", scanner.Text())
}
cmd.Wait()
```

Benefits:
- No tmux dependency on sprite
- No polling — `cmd.Wait()` blocks until exit
- Session ID stored for reconnection after crash
- `ExitMessage` gives exact exit code
- Stdout streamed directly, no file intermediary

### 2. Reconnect to running sessions after restart

The exec API's killer feature: sessions survive client disconnections.

```go
// On executor restart, check for existing sessions
sessions, _ := client.ListSessions(ctx, spriteName)
for _, session := range sessions {
    if isTaskSession(session) {
        // Reattach to running Claude session
        cmd := sprite.AttachSession(ctx, session.ID)
        stdout, _ := cmd.StdoutPipe()
        // Resume streaming logs...
    }
}
```

This solves the current problem where restarting the local `task` daemon
loses track of running sprite tasks.

### 3. Replace file-tailing hooks with FS Watch or direct streaming

**Option A: FS Watch (recommended)**
```go
// Watch hooks file for changes via WebSocket
watcher := sprite.Filesystem().Watch(ctx, "/tmp/task-hooks.jsonl")
for event := range watcher.Events() {
    // Process new hook events as they're written
}
```

**Option B: Direct stdout parsing**
Since Claude's output is now streamed directly through the exec WebSocket,
parse hook-like events from stdout instead of a sidecar file:
```go
// Claude's MCP server already writes structured output
// Parse it from the exec session's stdout stream
```

**Option C: Keep file-based hooks but use Filesystem API**
```go
// Read hooks file periodically via FS API (no exec needed)
data, _ := sprite.Filesystem().ReadFile("/tmp/task-hooks.jsonl")
```

### 4. Use Services API for the web API (PR #160)

PR #160 runs `taskd` inside the sprite with `nohup ... &` and polls for
readiness. The Services API manages this properly:

```go
// Create a service for the web API
service, _ := client.CreateService(ctx, spriteName, &sprites.ServiceConfig{
    Name:    "webapi",
    Command: "/usr/local/bin/taskd --web-api --addr :8080",
})

// Start it
client.StartService(ctx, spriteName, service.ID)

// Get logs
stream, _ := client.GetServiceLogs(ctx, spriteName, service.ID)
```

Benefits:
- Auto-restart on crash
- Proper log management
- Clean start/stop lifecycle
- No polling for readiness

### 5. Use Filesystem API for setup

**Before:**
```go
// Install hook script via exec
installCmd := fmt.Sprintf(`cat > /usr/local/bin/hook << 'EOF'\n%s\nEOF`, script)
sprite.CommandContext(ctx, "sh", "-c", installCmd).Run()
```

**After:**
```go
// Write files directly via filesystem API
fs := sprite.Filesystem()
fs.WriteFile("/usr/local/bin/task-sprite-hook", []byte(hookScript), 0755)
fs.WriteFile("/root/.claude/settings.json", []byte(claudeSettings), 0644)
fs.MkdirAll("/workspace", 0755)
```

### 6. Use Network Policy for security

```go
// Set network policy to restrict sprite access
client.SetPolicy(ctx, spriteName, &sprites.Policy{
    AllowedDomains: []string{
        "github.com",
        "api.anthropic.com",
        "api.openai.com",
        "rubygems.org",
        "registry.npmjs.org",
    },
})
```

### 7. Use Port Notifications for dev servers

When Claude starts a dev server inside the sprite, the exec API sends a
`PortNotificationMessage` with a proxy URL:

```go
cmd.TextMessageHandler = func(data []byte) {
    var notification sprites.PortNotificationMessage
    json.Unmarshal(data, &notification)
    if notification.Type == "port_opened" {
        // notification.ProxyURL is the public URL
        db.AppendTaskLog(task.ID, "system",
            fmt.Sprintf("Dev server at %s", notification.ProxyURL))
    }
}
```

This enables the TUI to show clickable URLs when Claude starts servers.

## Implementation Priority

1. **Replace tmux with exec sessions** — Biggest simplification, removes a
   dependency, enables reconnection. Affects `executor_sprite.go`.

2. **Session reconnection on restart** — Critical for reliability. Affects
   `executor.go` startup logic.

3. **Filesystem API for setup** — Cleaner, no shell escaping issues. Affects
   `sprite.go` setup code.

4. **Network Policy** — Security improvement. Small change.

5. **Port notifications** — Nice UX improvement. Small change.

6. **Services API for PR #160** — Only relevant for the web UI PR.

7. **FS Watch for hooks** — Nice to have, current file-tailing works.

## SDK Version Note

The `sprites-go` SDK at `v0.0.0-20260109202230-abba9310f931` (used in PR #103)
already has exec session support, filesystem operations, services, and session
management. The `Cmd` struct supports `SessionID`, `TextMessageHandler`, TTY,
and environment variables. No SDK upgrade needed — the features just need to
be used.

## Summary

The exec API eliminates the need for tmux on sprites entirely. Instead of
layering tmux → Claude with file-based hook streaming, we run Claude directly
as an exec session with native persistence, reconnection, and streaming. This
reduces setup complexity (no tmux/Node.js installation), improves reliability
(crash recovery via session reconnection), and simplifies the code
significantly.

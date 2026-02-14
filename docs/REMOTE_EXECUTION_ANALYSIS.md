# Remote Execution & Sandboxing Analysis

This document analyzes different approaches to running Claude Code securely for our Task TUI application, comparing Claude Code's native sandboxing, devcontainers, and cloud-based approaches.

## Current State

Our task TUI already has substantial cloud infrastructure:

- **`task cloud init`** - Interactive wizard for Hetzner/VPS setup
- **`task cloud status/logs/sync`** - Remote management commands
- **SSH access via Wish** - Connect to TUI from anywhere (`ssh -p 2222 server`)
- **Git worktrees** - File-level isolation between parallel tasks
- **Runner user** - Non-root execution on remote servers

What we lack: **kernel-level sandboxing** to prevent malicious code from affecting the host.

## Approaches Compared

### 1. Claude Code Native Sandboxing

Claude Code has built-in OS-level sandboxing using:
- **Linux**: bubblewrap (namespace-based isolation)
- **macOS**: Seatbelt sandbox

**Capabilities:**
| Feature | Description |
|---------|-------------|
| Filesystem isolation | R/W only to working directory, read-only elsewhere |
| Network isolation | Only approved domains accessible |
| Process isolation | All child processes inherit restrictions |
| Auto-allow mode | Commands run without permission prompts if within sandbox |

**Pros:**
- ✅ Zero additional infrastructure needed
- ✅ Works locally and on any Linux/macOS server
- ✅ Enables `--dangerously-skip-permissions` safely
- ✅ Configurable via `settings.json`
- ✅ Handles filesystem AND network restrictions

**Cons:**
- ❌ Doesn't isolate between concurrent tasks (shared kernel)
- ❌ No Windows support yet
- ❌ Broad domain allowlists can be bypassed (domain fronting)
- ❌ Unix socket access can break isolation (e.g., docker.sock)

**Integration with our app:**
```go
// Already supported in executor.go
args := []string{"claude"}
if dangerous {
    args = append(args, "--dangerously-skip-permissions")
}
// Claude Code's sandbox applies automatically
```

To enable, we could add to task execution:
```json
// .claude/settings.json in worktree
{
  "sandbox": {
    "permissions": {
      "fs": {
        "write": {"allow": ["$CWD/**"]}
      },
      "network": {
        "allowedDomains": ["github.com", "api.anthropic.com"]
      }
    }
  }
}
```

### 2. Devcontainers

Claude Code provides a [reference devcontainer implementation](https://github.com/anthropics/claude-code/tree/main/.devcontainer) with:

- Node.js 20 base image
- Custom firewall (iptables) restricting outbound traffic
- VS Code integration with Remote Containers extension
- Pre-configured shell environment (ZSH + fzf + git)

**Pros:**
- ✅ Strong isolation via Docker
- ✅ Consistent environment across machines
- ✅ Can run `--dangerously-skip-permissions` safely
- ✅ Network firewall blocks unauthorized connections
- ✅ Isolates between tasks (each task = separate container)

**Cons:**
- ❌ Docker daemon required on host
- ❌ Container startup overhead (~5-10 seconds)
- ❌ More complex than native sandboxing
- ❌ Credential exfiltration still possible within container
- ❌ Requires VS Code or compatible tooling

**Integration with our app:**

Instead of git worktrees, we'd spawn Docker containers:
```go
func executeInDevcontainer(task *db.Task) error {
    // Create ephemeral container from project's .devcontainer
    containerName := fmt.Sprintf("task-%d", task.ID)

    cmd := exec.Command("docker", "run",
        "--name", containerName,
        "--rm",
        "-v", fmt.Sprintf("%s:/workspace", worktreePath),
        "-e", fmt.Sprintf("TASK_ID=%d", task.ID),
        "--network=task-network", // Custom network with egress rules
        "task-devcontainer:latest",
        "claude", "--dangerously-skip-permissions", "-p", prompt)

    return cmd.Run()
}
```

### 3. Remote Hetzner/VPS (Current Approach)

What we have now:
- Dedicated Linux server with `runner` user
- Tasks run in git worktrees
- SSH access via Wish on port 2222
- Systemd service for auto-restart

**Pros:**
- ✅ Already implemented and working
- ✅ Full Linux environment
- ✅ Persistent state across sessions
- ✅ SSH access from anywhere
- ✅ Can be combined with native sandboxing

**Cons:**
- ❌ No isolation between tasks (shared filesystem)
- ❌ Compromised task can affect others
- ❌ Paying for idle server
- ❌ Single point of failure

**Enhancement**: Add Claude Code's native sandboxing:
```bash
# In systemd service
ExecStart=/home/runner/bin/taskd -addr :2222 -dangerous
# Claude runs with sandbox enabled by default
```

### 4. Fly.io Machines (Sprites)

Fly.io Machines are fast-starting VMs (~300ms) with:
- Per-invocation billing (pay only when running)
- Auto-suspend on idle
- Ephemeral or persistent volumes
- Global edge network

**Pros:**
- ✅ True VM isolation (not containers)
- ✅ Fast cold starts (~300ms vs ~30s for full VMs)
- ✅ Per-second billing, scale to zero
- ✅ Each task = separate machine (perfect isolation)
- ✅ Can persist state via volumes
- ✅ Global distribution

**Cons:**
- ❌ Not implemented yet
- ❌ More complex orchestration
- ❌ Network latency for remote ops
- ❌ Fly.io dependency
- ❌ Costs add up for many concurrent tasks

**Integration concept:**
```go
func executeOnFlyMachine(task *db.Task) error {
    // Create ephemeral Fly Machine
    machine, err := flyClient.CreateMachine(MachineConfig{
        Image: "task-worker:latest",
        Size:  "shared-cpu-1x",
        Env: map[string]string{
            "TASK_ID":        strconv.Itoa(task.ID),
            "ANTHROPIC_KEY":  os.Getenv("ANTHROPIC_API_KEY"),
            "PROJECT_REPO":   task.ProjectURL,
        },
        AutoStop: &AutoStop{
            IdleTimeout: 5 * time.Minute,
            Strategy:    "suspend", // or "stop" for full isolation
        },
    })

    // Machine clones repo, runs Claude, pushes results
    return machine.WaitForCompletion()
}
```

## Recommendation

**Hybrid approach** combining multiple layers:

### Phase 1: Enhance Current Setup (Low effort, immediate value)
1. **Enable Claude Code sandboxing** on our existing Hetzner setup
2. **Configure allowed domains** in `.claude/settings.json` per project
3. **Use `--dangerously-skip-permissions`** since sandbox provides protection

```go
// internal/executor/executor.go - add sandbox config to worktree setup
func (e *Executor) setupWorktreeSandboxConfig(worktreePath string) error {
    sandboxConfig := map[string]interface{}{
        "sandbox": map[string]interface{}{
            "permissions": map[string]interface{}{
                "fs": map[string]interface{}{
                    "write": map[string][]string{
                        "allow": []string{"$CWD/**", "/tmp/**"},
                    },
                },
                "network": map[string]interface{}{
                    "allowedDomains": []string{
                        "github.com",
                        "api.github.com",
                        "api.anthropic.com",
                        "registry.npmjs.org",
                        // Add project-specific domains
                    },
                },
            },
        },
    }
    // Write to .claude/settings.json in worktree
    return writeJSON(filepath.Join(worktreePath, ".claude", "settings.json"), sandboxConfig)
}
```

### Phase 2: Devcontainers for Full Isolation (Medium effort)
1. **Add project-level `.devcontainer/`** configs
2. **Run tasks in ephemeral containers** instead of worktrees
3. **Custom firewall rules** per project type
4. **VS Code integration** for developers who want GUI

### Phase 3: Fly.io for Scale (Higher effort, future)
1. **Task-per-machine model** for ultimate isolation
2. **Auto-scaling** based on queue depth
3. **Geographic distribution** for low latency
4. **Pay-per-use** economics at scale

## Comparison Matrix

| Feature | Native Sandbox | Devcontainer | Hetzner VPS | Fly.io |
|---------|----------------|--------------|-------------|--------|
| Setup complexity | ⭐ Low | ⭐⭐ Medium | ⭐⭐ Medium | ⭐⭐⭐ High |
| Task isolation | ⭐ Process | ⭐⭐⭐ Container | ⭐ Process | ⭐⭐⭐ VM |
| Startup time | ⭐⭐⭐ Instant | ⭐⭐ ~5s | ⭐⭐⭐ Instant | ⭐⭐ ~300ms |
| Cost at scale | ⭐⭐⭐ Free | ⭐⭐ Docker overhead | ⭐⭐ Fixed monthly | ⭐⭐⭐ Pay-per-use |
| Idle cost | N/A | N/A | ⭐ ~$5-20/mo | ⭐⭐⭐ $0 |
| Skip permissions | ✅ Yes | ✅ Yes | ⚠️ Risky | ✅ Yes |
| Already implemented | ⚠️ Partial | ❌ No | ✅ Yes | ❌ No |

## Implementation Priority

1. **Immediate**: Enable `sandbox` settings in executor.go for worktrees
2. **Short-term**: Add `--dangerously-skip-permissions` flag (already exists)
3. **Medium-term**: Create reference devcontainer for our task-worker
4. **Long-term**: Evaluate Fly.io if scaling beyond single server

## Security Considerations

With any approach, these remain concerns:

1. **Credential exfiltration** - Claude has access to API keys within its environment
2. **Allowed domains** - GitHub.com access means attacker could push to repos
3. **Prompt injection** - Malicious code in repo could manipulate Claude
4. **Resource exhaustion** - Tasks could consume excessive CPU/memory

Mitigations:
- Use read-only API tokens where possible
- Consider separate Claude API keys per project
- Review Claude's actions in task logs
- Set resource limits (already have suspension after idle)

## Conclusion

The best path forward combines **Claude Code's native sandboxing** (Phase 1) with our existing Hetzner infrastructure. This gives us:

- Immediate security improvements with minimal changes
- Ability to safely use `--dangerously-skip-permissions`
- Foundation for devcontainer/Fly.io expansion later

The native sandbox addresses most security concerns while keeping our current architecture intact. Devcontainers and Fly.io provide upgrade paths when we need stronger isolation or better scaling.

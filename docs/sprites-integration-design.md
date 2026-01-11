# Sprites Integration Design Document

## Overview

This document explores how [Sprites](https://sprites.dev) could be integrated into the `task` workflow system to provide isolated, cloud-based execution environments for Claude instances. Sprites are hardware-isolated Linux sandboxes with persistent filesystems, designed specifically for running AI agents like Claude Code.

## Current Architecture

### How Tasks Execute Today

```
Task Queue → Executor → tmux window → Claude CLI
                ↓
         Git Worktree (isolation)
                ↓
         Hooks → Status Updates → Database
```

The current system:
1. Creates a Git worktree for each task (file isolation)
2. Launches Claude in a tmux window within a `task-daemon` session
3. Uses Claude hooks to track status (PreToolUse, PostToolUse, Notification, Stop)
4. Runs on the same machine as the `task` daemon (local or remote server via `taskd`)

### Current Limitations

- **Resource contention**: All Claude instances share the same machine's resources
- **Security**: Claude runs with the same permissions as the user
- **Scalability**: Limited by single machine capacity
- **Isolation**: Only git worktrees provide isolation (no process/network isolation)
- **Cloud complexity**: Running `taskd` on a remote server requires full server setup

## Sprites Overview

### What Are Sprites?

Sprites are Firecracker-based VMs that provide:
- **Hardware isolation**: Each sprite is a separate VM
- **Persistent filesystem**: ext4 filesystem that persists between runs
- **Fast checkpoints**: ~300ms snapshot creation, <1s restore
- **Network policies**: DNS-based egress filtering
- **HTTP endpoints**: Each sprite gets a unique URL
- **Resource flexibility**: Up to 8 CPUs, 16GB RAM per sprite

### Pricing

- CPU: $0.07/CPU-hour (6.25% minimum per second)
- Memory: $0.04375/GB-hour (250MB minimum per second)
- Storage: $0.00068/GB-hour
- Example: 4-hour Claude Code session ≈ $0.46

### API Capabilities

```
POST /v1/sprites              - Create sprite
GET /v1/sprites               - List sprites
GET /v1/sprites/{name}        - Get sprite details
PUT /v1/sprites/{name}        - Update sprite
DELETE /v1/sprites/{name}     - Delete sprite
WebSocket /v1/sprites/{name}/exec - Execute commands with stdin/stdout streaming
```

## Integration Proposal

### Architecture with Sprites

```
Task Queue → Executor → Sprites API
                            ↓
                    Create Sprite Instance
                            ↓
                    Clone repo + setup worktree
                            ↓
                    Launch Claude in Sprite
                            ↓
                    Stream output via WebSocket
                            ↓
                    Hooks callback to task daemon
                            ↓
                    Cleanup: push branch, delete sprite
```

### Key Benefits

1. **True Isolation**: Each task runs in its own VM, not just a separate directory
2. **Security**: Network policies can restrict what Claude can access
3. **Scalability**: Spawn many sprites in parallel across Fly.io's infrastructure
4. **Simplified Cloud**: No need to maintain our own `taskd` server
5. **Cost Efficiency**: Pay only for actual usage, not idle server time
6. **Checkpoints**: Save sprite state for resumable tasks

### Execution Modes

#### Mode 1: Full Sprite Execution (Recommended)

Each task gets a dedicated sprite that:
1. Clones the repository
2. Sets up the worktree and branch
3. Runs Claude with full isolation
4. Pushes changes when complete
5. Gets deleted after task completion

```go
// Pseudocode for sprite-based execution
func (e *Executor) runClaudeSprite(ctx context.Context, task Task) execResult {
    // Create sprite
    sprite, err := sprites.Create(ctx, sprites.CreateParams{
        Name: fmt.Sprintf("task-%d-%s", task.ID, task.Slug),
    })
    defer sprites.Delete(ctx, sprite.Name)

    // Setup environment
    sprites.Exec(ctx, sprite.Name, fmt.Sprintf(`
        git clone %s /workspace
        cd /workspace
        git checkout -b %s
        # Install claude CLI
        npm install -g @anthropic-ai/claude-code
    `, task.RepoURL, task.BranchName))

    // Run Claude with hooks configured to call back to our server
    sprites.Exec(ctx, sprite.Name, fmt.Sprintf(`
        export TASK_ID=%d
        export TASK_CALLBACK_URL=%s
        claude --chrome "%s"
    `, task.ID, callbackURL, task.Prompt))

    // Push results
    sprites.Exec(ctx, sprite.Name, `
        git add -A
        git commit -m "Task completion"
        git push origin HEAD
    `)
}
```

#### Mode 2: Hybrid Execution

Use sprites only for untrusted or resource-intensive tasks:
- Keep local tmux execution for quick, trusted tasks
- Use sprites for tasks marked as "cloud" or "isolated"
- User can choose per-task or set project defaults

#### Mode 3: Persistent Sprite Pool

Maintain warm sprites with pre-configured environments:
- Create checkpoints with common tools installed
- Restore from checkpoint for faster startup
- Useful for projects with complex dependencies

### Hook Integration

Claude hooks need to communicate back to the task daemon. Options:

**Option A: HTTP Callbacks**
```go
// Sprite-side hook script
hooks:
  Stop:
    - command: "curl -X POST $TASK_CALLBACK_URL/hook -d '{\"event\":\"Stop\",\"task_id\":$TASK_ID}'"
```

**Option B: Sprite HTTP Endpoint**
- Each sprite has a unique URL
- Task daemon polls sprite endpoint for status
- Less real-time but simpler

**Option C: WebSocket Streaming**
- Maintain WebSocket connection to sprite's exec endpoint
- Parse Claude output in real-time
- Most responsive but more complex

### Database and State Management

The current SQLite database stays local. Sprites are ephemeral execution environments:

```
┌─────────────────┐         ┌──────────────────┐
│  Local Machine  │         │     Sprites      │
│                 │         │                  │
│  ┌───────────┐  │  HTTP   │  ┌────────────┐  │
│  │  task DB  │◄─┼─────────┼──│  Claude    │  │
│  │  (SQLite) │  │ Hooks   │  │  Instance  │  │
│  └───────────┘  │         │  └────────────┘  │
│                 │         │                  │
│  ┌───────────┐  │  API    │  ┌────────────┐  │
│  │ Executor  │──┼─────────┼─►│  Sprite    │  │
│  └───────────┘  │         │  │  (VM)      │  │
└─────────────────┘         └──────────────────┘
```

### Git Integration

Sprites need git access:

1. **SSH Keys**: Generate per-sprite keys or use deploy tokens
2. **HTTPS + Token**: Pass GitHub token as environment variable
3. **Sparse Checkout**: Only clone necessary files for large repos

### Network Policies

Sprites allow DNS-based egress filtering:

```go
// Restrict Claude to only necessary services
sprites.SetPolicy(ctx, sprite.Name, sprites.Policy{
    AllowDomains: []string{
        "api.anthropic.com",     // Claude API
        "github.com",            // Git operations
        "registry.npmjs.org",    // Package installation
        // Project-specific domains
    },
})
```

This is a significant security improvement over the current model.

## Implementation Phases

### Phase 1: Proof of Concept

1. Add Sprites Go SDK dependency
2. Create `runClaudeSprite()` function alongside existing `runClaude()`
3. Add `--sprite` flag to `task execute` command
4. Test with simple tasks

### Phase 2: Core Integration

1. Add sprite configuration to database (API token, default settings)
2. Implement hook callbacks over HTTP
3. Add sprite status to task detail view
4. Handle sprite failures gracefully (fallback to local)

### Phase 3: Advanced Features

1. Checkpoint support for resumable tasks
2. Network policy configuration per project
3. Warm sprite pools for faster startup
4. Cost tracking and reporting

### Phase 4: Full Cloud Mode

1. Remove need for `taskd` server entirely
2. All execution happens on sprites
3. Database could be hosted (e.g., Turso) or synced
4. Truly serverless task execution

## Configuration

```yaml
# ~/.config/task/config.yaml
sprites:
  enabled: true
  token: "${SPRITES_TOKEN}"
  default_mode: "hybrid"  # local, sprite, hybrid

  # Resource defaults
  resources:
    cpus: 2
    memory_gb: 4

  # Network policies
  policies:
    default:
      - "api.anthropic.com"
      - "github.com"

  # Per-project overrides
  projects:
    my-project:
      mode: "sprite"
      policies:
        - "api.openai.com"  # Additional allowed domain
```

## Trade-offs

### Advantages

| Aspect | Current (tmux) | With Sprites |
|--------|----------------|--------------|
| Isolation | Git worktree only | Full VM isolation |
| Security | User permissions | Network policies |
| Scalability | Single machine | Cloud infrastructure |
| Cost | Server uptime | Pay-per-use |
| Setup | Complex for cloud | API key only |
| Startup | Instant | ~2-5 seconds |

### Disadvantages

1. **Latency**: Sprite creation adds startup time (~2-5s)
2. **Connectivity**: Requires internet access
3. **Cost for heavy usage**: Many long-running tasks add up
4. **Complexity**: Another external dependency
5. **Debugging**: Harder to attach/debug remote sprites

### When to Use Each

**Use Local tmux when:**
- Quick iterations during development
- Tasks that need real-time user interaction
- No internet connectivity
- Cost optimization for heavy usage

**Use Sprites when:**
- Running untrusted code
- Need strong isolation guarantees
- Scaling beyond single machine
- Simplified cloud deployment

## Alternative Approaches Considered

### 1. Keep Current Architecture

Pros: Already works, no new dependencies
Cons: Limited isolation, scaling challenges

### 2. Docker/Podman Containers

Pros: Industry standard, local execution
Cons: Not as isolated as VMs, more setup required

### 3. Other Cloud VMs (EC2, GCE)

Pros: More control
Cons: More expensive, slower startup, more to manage

### 4. Firecracker Directly

Pros: Same tech as Sprites, more control
Cons: Significant infrastructure to build

**Conclusion**: Sprites provides the right abstraction - VM-level isolation with minimal operational overhead, specifically designed for AI agent workloads.

## Next Steps

1. [ ] Set up Sprites account and get API token
2. [ ] Experiment with SDK in isolated branch
3. [ ] Prototype `runClaudeSprite()` function
4. [ ] Test hook callbacks over HTTP
5. [ ] Measure startup latency and costs
6. [ ] Document findings and refine approach

## References

- [Sprites API Documentation](https://sprites.dev/api)
- [Sprites Go SDK](https://github.com/superfly/sprites-go)
- [Current Executor Implementation](../internal/executor/executor.go)

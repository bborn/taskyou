# Sprites Integration Design

## Summary

Use [Sprites](https://sprites.dev) (Fly.io's managed sandbox VMs) as isolated cloud execution environments for Claude. One sprite per project, persistent dev environment, dangerous mode enabled safely.

## The Model

**One sprite per project, not per task.**

```
┌─────────────────────────────────────────────────────────────┐
│  Sprite: my-rails-app                                       │
│                                                             │
│  /workspace/                      Persistent filesystem     │
│    ├── app/                       (deps installed once)     │
│    ├── Gemfile.lock                                         │
│    └── .task-worktrees/                                     │
│          ├── 42-fix-auth/         ← Task isolation          │
│          └── 43-add-feature/                                │
│                                                             │
│  tmux: task-daemon                Same as local model       │
│    ├── task-42 (Claude, dangerous mode)                     │
│    └── task-43 (Claude, dangerous mode)                     │
│                                                             │
│  Network policy: github.com, api.anthropic.com, rubygems.org│
└─────────────────────────────────────────────────────────────┘
```

This mirrors the local architecture exactly. Same tmux session, same worktree isolation, same hooks. Just running remotely.

## Why This Matters

### Dangerous Mode Becomes Safe

Currently `--dangerously-skip-permissions` is too risky—Claude has access to everything on your machine. In a sprite:

- Claude can do anything... inside the sandbox
- Network restricted to whitelisted domains
- Can't touch other projects or your local files
- Worst case: destroy sprite, restore from checkpoint

**Result:** Tasks execute without permission prompts. Much faster.

### Simpler Than taskd

Current cloud setup requires provisioning a VPS, configuring SSH/systemd, installing deps, keeping it updated, paying for idle time.

Sprites setup:
```bash
$ task sprite init my-project
# Creates sprite, clones repo, installs deps, checkpoints
# Done.
```

### Persistent Dev Environment

Setup happens once per project:
- `bundle install` runs during init
- Gems persist across tasks
- Checkpoint when idle (~$0.01/day storage)
- Restore in <1 second when needed

## Architecture

```
┌─────────────────────────┐         ┌─────────────────────────┐
│  Local Machine          │         │  Sprite                 │
│                         │         │                         │
│  task daemon            │         │  tmux + Claude          │
│    ├── orchestrates     │         │    ├── runs tasks       │
│    ├── stores DB        │         │    ├── dangerous mode   │
│    └── serves TUI       │         │    └── writes hooks     │
│                         │         │                         │
│  TUI ◄── DB updates     │         │  /tmp/task-hooks.log    │
│                         │         │         │               │
└────────────┬────────────┘         └─────────┼───────────────┘
             │                                │
             │  sprites.Exec("tail -f")       │
             └────────────────────────────────┘
                     WebSocket stream
```

## Hook Communication

**Problem:** Claude runs on sprite, but database is local. How do hooks sync?

**Solution:** `tail -f` via Sprites exec WebSocket.

```
Sprite                              Local
──────                              ─────
Claude hook fires
  │
  ▼
echo '{"event":"Stop"}' >>
  /tmp/task-hooks.log               sprites.Exec("tail -f hooks.log")
                                      │
                                      ▼ (~30ms latency)
                                    Parse JSON
                                    Update database
                                    TUI refreshes
```

Hooks just append JSON to a file. Local daemon tails it over the existing WebSocket. Real-time, no extra infrastructure.

### User Input (Reverse Direction)

When Claude needs input, user responds in TUI:

```
TUI                                 Sprite
───                                 ──────
User types "yes"
  │
  ▼
sprites.Exec("tmux send-keys
  -t task-42 'yes' Enter")          Claude receives keystroke
                                    Continues working
```

Same mechanism we use locally—just remote tmux.

**Round-trip latency:** ~100-150ms. Feels instant.

## CLI Commands

```bash
# Lifecycle
task sprite init <project>          # Create sprite, clone, setup, checkpoint
task sprite status [project]        # Check sprite state
task sprite sync <project>          # Pull latest code, update deps if needed
task sprite attach <project>        # SSH + tmux attach (interactive)
task sprite checkpoint <project>    # Manual checkpoint
task sprite destroy <project>       # Delete sprite

# Execution
task execute <id> --sprite          # Run task on sprite
task project edit <project> --execution sprite  # Make sprite the default
```

## Cost

| Resource | Price |
|----------|-------|
| CPU | $0.07/CPU-hour |
| Memory | $0.04/GB-hour |
| Storage | $0.00068/GB-hour |

**Medium sprite (2 CPU, 4GB):** ~$0.32/hour active

| Usage Pattern | Monthly Cost |
|---------------|--------------|
| Light (2 hrs/day) | ~$19 |
| Moderate (4 hrs/day) | ~$35 |
| Heavy (6 hrs/day) | ~$63 |
| Idle (checkpoint only) | ~$5 |

Comparable to a VPS for light/moderate use, more expensive for heavy use—but managed and isolated.

## Trade-offs

| Aspect | Local/taskd | Sprites |
|--------|-------------|---------|
| Isolation | Worktrees only | Full VM |
| Dangerous mode | Risky | Safe |
| Server management | You do it | Managed |
| Offline work | ✓ Works | ✗ Needs internet |
| Startup latency | Instant | ~1s (restore) |
| Cost model | Fixed | Pay-per-use |
| Vendor dependency | None | Fly.io |

## Open Questions

1. **Fly.io dependency acceptable?** It's opt-in, but still a vendor lock-in for that feature.

2. **Git credentials in sprite?** SSH key per sprite? GitHub token passed at runtime?

3. **Claude auth?** How does Claude authenticate inside the sprite?

4. **Multi-user?** Shared sprite per project, or one per user?

## Recommendation

Build as an **experimental opt-in feature**:
- Keep local execution as default
- Keep taskd for users who prefer it
- Add `task sprite` commands for those who want managed cloud + isolation
- Gather feedback, iterate

The per-project model reuses existing architecture (tmux, worktrees, hooks) while solving the "cloud without server ops" and "safe dangerous mode" problems.

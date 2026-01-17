# Worktrunk Worktree Management Analysis

**Repository**: https://github.com/max-sixty/worktrunk
**Language**: Rust
**Focus**: Git worktree management for AI agent parallelization

## Executive Summary

Worktrunk is a mature CLI tool designed specifically for managing git worktrees in AI-assisted development workflows. It provides a higher-level abstraction over native git worktree commands, with features tailored for running multiple AI agents (like Claude Code) in parallel.

## Architecture Overview

### Core Module Structure

```
src/
├── commands/
│   ├── worktree/
│   │   ├── switch.rs      # Worktree switching (21.5KB - largest)
│   │   ├── types.rs       # Data structures (13.1KB)
│   │   ├── resolve.rs     # Conflict resolution (10.4KB)
│   │   ├── push.rs        # Push functionality (7.3KB)
│   │   ├── remove.rs      # Worktree removal (1.5KB)
│   │   ├── hooks.rs       # Lifecycle hooks (3.5KB)
│   │   └── mod.rs         # Module definitions (3.7KB)
│   ├── merge.rs           # Merge workflow
│   ├── hooks.rs           # Hook execution system
│   └── list/              # Advanced listing with progressive rendering
├── git/
│   └── repository/
│       ├── worktrees.rs   # Core worktree operations (9.6KB)
│       ├── branches.rs    # Branch management (12.4KB)
│       └── ...
└── config/                # User and project configuration
```

## Key Features & Patterns

### 1. Two-Phase Switch Operation

Worktrunk separates worktree switching into planning and execution phases:

**Planning Phase (`plan_switch`)**:
1. Target Resolution - Handles `pr:` syntax, validates flags
2. Path Computation - Determines worktree location
3. Existence Check - Verifies if worktree exists for branch
4. Creation Validation - Ensures path available, handles `--clobber`
5. Plan Generation - Returns structured `SwitchPlan`

**Execution Phase (`execute_switch`)**:
- Takes validated plan and executes it
- Handles existing worktree vs. new creation
- Runs post-create hooks with context variables

**Benefit**: This fail-fast approach validates all constraints upfront before prompting users for hook approvals.

### 2. Smart Resolution Symbols

```
wt switch @    # Current worktree
wt switch -    # Previous worktree (from history)
wt switch ^    # Default branch worktree
```

The `resolve_worktree_name()` function expands these symbols before any operation.

### 3. Typed Branch Deletion Modes

```rust
enum BranchDeletionMode {
    Keep,       // Preserve branch regardless of merge status
    SafeDelete, // Delete only if fully merged (default)
    ForceDelete // Delete unconditionally
}
```

This replaces boolean flags with explicit intent, making operations safer and more readable.

### 4. Dual Configuration System

- **User config** (`~/.config/worktrunk/config.toml`) - Personal preferences
- **Project config** (`.config/wt.toml`) - Lifecycle hooks, stored in git

These configs operate completely separately with no overlap or precedence rules.

### 5. Lifecycle Hooks

| Hook          | When                    | Execution Mode |
|---------------|-------------------------|----------------|
| post-create   | After worktree created  | Foreground     |
| post-start    | After switching in      | Background     |
| post-switch   | After any switch        | Background     |
| pre-merge     | Before merge starts     | Foreground     |
| post-merge    | After merge completes   | Foreground     |
| pre-remove    | Before worktree removed | Foreground     |

Hooks support template variables: `{{ branch }}`, `{{ main_worktree }}`, `{{ repo }}`

### 6. Unified Merge Workflow

The merge command combines multiple operations:
1. Commit staged changes (optional)
2. Squash commits (optional)
3. Rebase onto target (optional)
4. Run pre-merge hooks
5. Push/fast-forward to target
6. Remove worktree (optional)
7. Run post-merge hooks

All commands collected upfront for single batch approval.

### 7. Integration Detection System

Five-tier priority-ordered integration checking:
1. Commit comparison (~1ms)
2. ... (escalating checks)
3. Merge simulation (~500ms-2s)

Stops as soon as integration is confirmed, avoiding expensive operations.

### 8. Concurrency Management

Global semaphore limits concurrent heavy git operations to 4 permits to balance parallelism against memory-mapped file contention.

## Comparison with Current Implementation

### Our Current Approach (workflow/internal/executor)

```go
func (e *Executor) setupWorktree(task *db.Task) (string, error) {
    // 1. Get project directory
    // 2. Initialize git if needed (legacy)
    // 3. Reuse existing worktree if path set
    // 4. Create worktree with branch naming: task/{id}-{slug}
    // 5. Symlink .claude config
    // 6. Copy MCP config
    // 7. Run init script
}
```

### Key Differences

| Feature | Worktrunk | Our Implementation |
|---------|-----------|-------------------|
| Branch naming | User-driven | Auto: `task/{id}-{slug}` |
| Worktree location | Configurable templates | Fixed: `.task-worktrees/` |
| Lifecycle hooks | 6 hook types with templates | Single init script |
| Merge workflow | Integrated squash/rebase/merge | Manual |
| Switch command | First-class citizen | N/A (executor creates) |
| History tracking | Previous worktree (`-`) | N/A |
| PR support | `pr:` syntax, fork handling | PR info fetching only |
| Configuration | User + Project configs | Per-project in `.taskyou.yml` |

### Patterns Worth Adopting

1. **Planning/Execution Separation**: Validate all constraints before executing, especially before user prompts.

2. **Typed Deletion Modes**: Replace boolean `force` flags with explicit `SafeDelete | ForceDelete | Keep`.

3. **Home Path Navigation**: When removing current worktree, automatically navigate to a sensible destination.

4. **Template Variables in Hooks**: Allow `{{ branch }}`, `{{ worktree }}`, etc. in init scripts.

5. **Progressive Rendering**: For list commands with heavy operations, show results as they complete.

6. **Path Canonicalization**: Handle symlinks properly (especially important on macOS).

## Recommendations

### Short-term (Low Effort)

1. Add `--force-delete` and `--keep-branch` flags with typed modes
2. Implement worktree navigation history (previous worktree tracking)
3. Add template variables to init scripts

### Medium-term

1. Separate worktree planning from execution for better error handling
2. Add more lifecycle hooks (pre-remove, post-switch)
3. Implement proper "home" navigation when removing current worktree

### Long-term (If Needed)

1. Consider user-level configuration for worktree defaults
2. PR-based worktree creation (`pr:123` syntax)
3. Integration status checking before operations

## References

- [Worktrunk GitHub](https://github.com/max-sixty/worktrunk)
- [Worktrunk CLAUDE.md](https://github.com/max-sixty/worktrunk/blob/main/CLAUDE.md) - Development guidelines
- [src/commands/worktree/switch.rs](https://github.com/max-sixty/worktrunk/blob/main/src/commands/worktree/switch.rs) - Core switching logic

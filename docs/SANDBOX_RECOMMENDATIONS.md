# Claude Code Sandboxing Implementation for Task TUI

## Executive Summary

After reviewing Claude Code's native sandboxing and devcontainer features, **the best approach is to implement Claude Code's native sandboxing immediately** as Phase 1, with devcontainers and Fly.io as optional future enhancements.

The existing REMOTE_EXECUTION_ANALYSIS.md document provides an excellent foundation. This document adds implementation details and final recommendations based on the actual Claude Code capabilities.

## Key Findings

### Claude Code Native Sandboxing (Ready to Use Now)

Claude Code already has OS-level sandboxing built-in using:
- **Linux**: bubblewrap (namespace-based isolation)
- **macOS**: Seatbelt sandbox

**How it works:**
1. Filesystem restrictions - R/W only to working directory, read elsewhere
2. Network restrictions - only approved domains accessible
3. Process isolation - all subprocesses inherit restrictions
4. Auto-allow mode - commands run without permission prompts if within sandbox

**Critical insight**: Your executor already runs Claude Code (executor.go:1082), so you get sandboxing **for free** by simply configuring it via `.claude/settings.json` files.

### What This Means for Your Task TUI

Your current architecture at executor.go:1029-1109 runs Claude like this:
```go
script := fmt.Sprintf(`TASK_ID=%d TASK_SESSION_ID=%s claude %s--chrome "$(cat %q)"`,
    taskID, sessionID, dangerousFlag, promptFile.Name())
```

The sandboxing is already happening! You just need to configure it properly.

## Implementation Recommendation

### Phase 1: Enable Sandboxing (Immediate, Low Effort)

**Goal**: Add sandbox configuration to each task's worktree.

**Changes needed**:

1. **Modify setupWorktree() in executor.go** to create sandbox config:

```go
// After line 2005, add:
if err := e.setupSandboxConfig(worktreePath, task.Project); err != nil {
    e.logger.Warn("could not setup sandbox config", "error", err)
}
```

2. **Add new method to Executor**:

```go
// setupSandboxConfig creates a .claude/settings.json with sandbox configuration
func (e *Executor) setupSandboxConfig(worktreePath, project string) error {
    claudeDir := filepath.Join(worktreePath, ".claude")
    if err := os.MkdirAll(claudeDir, 0755); err != nil {
        return fmt.Errorf("create .claude dir: %w", err)
    }

    settingsPath := filepath.Join(claudeDir, "settings.json")

    // Get project-specific allowed domains
    allowedDomains := e.getProjectAllowedDomains(project)

    sandboxConfig := map[string]interface{}{
        "sandbox": map[string]interface{}{
            "permissions": map[string]interface{}{
                "fs": map[string]interface{}{
                    "write": map[string][]string{
                        "allow": []string{"$CWD/**", "/tmp/**"},
                    },
                },
                "network": map[string]interface{}{
                    "allowedDomains": allowedDomains,
                },
            },
        },
    }

    data, err := json.MarshalIndent(sandboxConfig, "", "  ")
    if err != nil {
        return err
    }

    return os.WriteFile(settingsPath, data, 0644)
}

// getProjectAllowedDomains returns network domains allowed for a project
func (e *Executor) getProjectAllowedDomains(project string) []string {
    // Base domains needed for Claude to function
    base := []string{
        "api.anthropic.com",
        "api.github.com",
        "github.com",
        "registry.npmjs.org",
        "pypi.org",
    }

    // Check if project has custom allowed domains
    proj, err := e.db.GetProjectByName(project)
    if err == nil && proj != nil {
        // You could add a new field to projects: allowed_domains
        // For now, return base + common dev tools
    }

    return base
}
```

3. **Update TASK_DANGEROUS_MODE usage**:

Your current code at executor.go:1078-1081 only uses `--dangerously-skip-permissions` when TASK_DANGEROUS_MODE=1. With sandboxing configured, you can safely enable this by default:

```go
// Instead of checking TASK_DANGEROUS_MODE, enable by default when sandbox is configured
dangerousFlag := "--dangerously-skip-permissions "
```

**Benefits of this approach**:
- ✅ Zero infrastructure changes needed
- ✅ Works on your existing Hetzner VPS immediately
- ✅ Enables automatic command execution (no permission prompts)
- ✅ Protects against filesystem and network abuse
- ✅ Each task gets isolated permissions via worktrees
- ✅ ~50 lines of code, can be done in 1 hour

**Security gains**:
- Tasks can't modify files outside their worktree
- Tasks can't connect to arbitrary servers
- Malicious code/dependencies are contained
- Prompt injection attacks are mitigated

### Phase 2: Devcontainers (Medium Term, If Needed)

**When to consider**: If you need:
- Stronger isolation between concurrent tasks
- Per-task resource limits
- Reproducible environments across machines
- Team collaboration features

**Implementation**: Replace git worktrees with Docker containers in executor.go:executeTask()

**Effort**: ~2-3 days of development + testing

### Phase 3: Fly.io Machines (Future, If Scaling)

**When to consider**: If you:
- Run >10 concurrent tasks regularly
- Need geographic distribution
- Want to stop paying for idle VPS
- Need true VM-level isolation

**Effort**: ~1-2 weeks of development + migration

## Comparison Matrix Update

| Feature | Native Sandbox (Phase 1) | Devcontainer (Phase 2) | Fly.io (Phase 3) |
|---------|-------------------------|------------------------|------------------|
| Implementation time | 1 hour | 2-3 days | 1-2 weeks |
| Works with current code | ✅ Minimal changes | ⚠️ Moderate changes | ❌ Major refactor |
| Filesystem isolation | ⭐⭐ Worktree-level | ⭐⭐⭐ Container-level | ⭐⭐⭐ VM-level |
| Network isolation | ✅ Domain allowlist | ✅ iptables firewall | ✅ VPC isolation |
| Auto-execute (skip perms) | ✅ Yes | ✅ Yes | ✅ Yes |
| Cost at scale | ⭐⭐⭐ Free | ⭐⭐ Docker overhead | ⭐⭐⭐ Pay-per-use |
| Idle cost | N/A | N/A | ⭐⭐⭐ $0 |

## Recommended Action Plan

### This Week
1. ✅ Read documentation (done)
2. Add `setupSandboxConfig()` method to executor.go
3. Call it from `setupWorktree()` after line 2005
4. Test with a sample task
5. Deploy to Hetzner VPS

### This Month
1. Monitor sandbox violations in logs
2. Tune allowed domains per project type
3. Add project-level `allowed_domains` field to database
4. Document for team usage

### Future (If Needed)
1. Evaluate devcontainers if task isolation becomes an issue
2. Consider Fly.io if costs or scaling become concerns

## Code Changes Summary

**Files to modify**:
- `internal/executor/executor.go` - Add sandbox configuration (~50 lines)
- `internal/db/projects.go` - Add `allowed_domains` field (optional, ~10 lines)

**No changes needed**:
- SSH server, TUI, task lifecycle
- Worktree management
- Claude hooks system
- Database schema (except optional domains field)

## Security Considerations

Even with sandboxing, these risks remain:

1. **Credential exfiltration** - Claude has access to API keys in its environment
   - Mitigation: Use read-only tokens where possible

2. **Allowed domain bypass** - GitHub access means attacker could push to repos
   - Mitigation: Use separate git credentials per project

3. **Prompt injection** - Malicious code in repo could manipulate Claude
   - Mitigation: Review Claude's actions, use hooks for suspicious activity

4. **Resource exhaustion** - Tasks could consume excessive CPU/memory
   - Mitigation: Current suspension system already handles this

## Conclusion

**The path forward is clear**: Implement Claude Code's native sandboxing (Phase 1) immediately. It's:
- Already built into the tool you're using
- Requires minimal code changes (~50 lines)
- Works with your current architecture
- Provides substantial security improvements
- Enables `--dangerously-skip-permissions` safely

Devcontainers and Fly.io remain excellent options for future enhancement, but aren't necessary to get significant security and UX benefits right now.

The existing REMOTE_EXECUTION_ANALYSIS.md document correctly identified this as the best approach. This document confirms that assessment and provides concrete implementation details.

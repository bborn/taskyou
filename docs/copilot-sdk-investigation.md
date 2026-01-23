# GitHub Copilot SDK Investigation

## Overview

This document analyzes the [GitHub Copilot SDK](https://github.com/github/copilot-sdk) and evaluates its potential value as an addition to Task You for AI-assisted task execution.

## What is the Copilot SDK?

The GitHub Copilot SDK is a programmatic interface for embedding Copilot's agentic workflows in applications. It exposes the same engine behind the Copilot CLI as a programmable SDK.

### Key Features

| Feature | Description |
|---------|-------------|
| **Language Support** | Go, Python, TypeScript, .NET |
| **Architecture** | JSON-RPC communication with Copilot CLI in server mode |
| **Session Management** | Create, resume, and manage conversation sessions |
| **Streaming** | Real-time response streaming |
| **Custom Tools** | Define tools that Copilot can invoke |
| **Custom Agents** | Create specialized AI personas |
| **MCP Integration** | Connect to Model Context Protocol servers (GitHub, etc.) |

### Requirements

- **GitHub Copilot subscription** (required, uses premium request quota)
- **Copilot CLI** must be installed separately
- Go 1.21+, Python 3.8+, Node.js 18+, or .NET 8.0+

### Current Status

**Technical Preview** - Not production-ready. Suitable for development and testing only.

## Task You Current AI Architecture

Task You currently supports pluggable AI executors through the `TaskExecutor` interface:

```go
type TaskExecutor interface {
    Name() string
    Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult
    Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult
    BuildCommand(task *db.Task, sessionID, prompt string) string
    IsAvailable() bool
    GetProcessID(taskID int64) int
    Kill(taskID int64) bool
    Suspend(taskID int64) bool
    IsSuspended(taskID int64) bool
}
```

### Current Executors

| Executor | CLI | Session Resumption | Notes |
|----------|-----|-------------------|-------|
| **Claude Code** | `claude` | Yes | Primary, Anthropic's coding agent |
| **Codex** | `codex` | No | OpenAI's coding assistant |

### Other AI Integrations

- **Ghost Text Autocomplete**: Uses Anthropic API (Claude Haiku) for task title/description suggestions

## Fit Analysis

### Could Copilot SDK Work as a New Executor?

**Technically possible**, but with significant architectural differences:

| Aspect | Current Approach | Copilot SDK Approach |
|--------|-----------------|---------------------|
| Process Model | Tmux windows users can attach to | JSON-RPC server (programmatic) |
| User Interaction | Real-time terminal access | Programmatic API only |
| Session Display | Direct terminal output | Event callbacks |
| Tool Execution | CLI handles everything | Custom tool handlers in code |

The fundamental difference is that Task You's architecture is designed around **interactive terminal sessions** that users can attach to and observe. The Copilot SDK is designed for **programmatic integration** where the application handles all I/O.

### Potential Use Cases

Where Copilot SDK could add value:

1. **Automated Background Tasks**: Tasks that don't need user observation
2. **Custom Tool Integration**: Exposing taskyou-specific tools (git operations, worktree management)
3. **GitHub-Centric Workflows**: Via MCP server integration for issues, PRs, etc.

### Reasons Against Adoption

1. **Technical Preview Status**: Not production-ready
2. **Subscription Dependency**: Adds GitHub Copilot subscription requirement
3. **Architectural Mismatch**: Programmatic API vs. interactive terminal sessions
4. **Limited Differentiation**: Claude Code and Codex already cover agentic coding tasks
5. **Complexity**: Would require significant architecture changes to support the different interaction model
6. **Model Lock-in**: Uses GPT models vs. current Claude/GPT choice

## Recommendation

**Do not adopt the Copilot SDK at this time.**

### Primary Reasons

1. **Technical Preview**: The SDK explicitly states it may not be suitable for production use
2. **Architectural Mismatch**: Task You's interactive tmux-based execution model is fundamentally different from Copilot SDK's programmatic approach
3. **Marginal Value**: The existing Claude Code and Codex executors already provide robust AI-assisted coding capabilities
4. **Additional Subscription Cost**: Would require users to have a GitHub Copilot subscription

### Future Considerations

If the Copilot SDK reaches production status and Task You's needs evolve, reconsider adoption for:

- **Headless task execution mode** (no user attachment needed)
- **Deeper GitHub integration** via MCP server
- **Enterprise deployments** where GitHub Copilot is already standard

## Comparison Matrix

| Capability | Claude Code | Codex | Copilot SDK (potential) |
|------------|-------------|-------|------------------------|
| Interactive Terminal | ✅ | ✅ | ❌ |
| Session Resumption | ✅ | ❌ | ✅ |
| User Attachment | ✅ | ✅ | ❌ |
| Streaming Output | ✅ | ✅ | ✅ |
| Custom Tools | Via MCP | Limited | ✅ |
| Production Ready | ✅ | ✅ | ❌ |
| Subscription Required | API key | API key | Copilot Sub |
| Model Flexibility | Claude | GPT | GPT |

## Conclusion

The Copilot SDK is an interesting technology for building programmatic AI integrations, but it doesn't align well with Task You's current architecture and use cases. The existing executor system with Claude Code and Codex provides better user experience for interactive task execution.

**Action Items**: None. Continue with current executor architecture.

---

*Investigation completed: January 2026*

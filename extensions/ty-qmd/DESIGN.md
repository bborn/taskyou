# ty-qmd Design Document

## Problem Statement

When Claude works on tasks, it lacks access to historical context:
- What similar tasks were done before?
- What patterns/solutions worked?
- What's documented in project READMEs, meeting notes, etc.?

Currently, Claude must re-explore codebases and rediscover patterns each time.

## Solution: QMD Integration

[QMD](https://github.com/tobi/qmd) is an on-device search engine that:
- Indexes markdown, notes, documentation
- Provides hybrid search (BM25 + semantic vectors + LLM re-ranking)
- Exposes tools via MCP for Claude integration
- Runs entirely locally (no data leaves the machine)

By integrating QMD with TaskYou, we enable:
1. **Task memory** - Search past completed tasks for relevant context
2. **Project knowledge** - Search project docs, READMEs, architecture notes
3. **Pattern learning** - Find similar past solutions to inform current work

## Integration Strategies

### Strategy 1: MCP Sidecar (Recommended for MVP)

Run QMD's MCP server alongside task execution.

```
┌─────────────────────────────────────────────────────────┐
│                  Task Execution Environment             │
│                                                         │
│  ┌──────────┐    ┌────────────┐    ┌──────────────┐    │
│  │  Claude  │───▶│ taskyou-mcp│    │   qmd mcp    │    │
│  │  Code    │    │  (tools)   │    │   (search)   │    │
│  └──────────┘    └────────────┘    └──────────────┘    │
│       │                                    │           │
│       └────────────────────────────────────┘           │
│         Claude uses both MCP servers                   │
└─────────────────────────────────────────────────────────┘
```

**Implementation:**
1. Configure Claude Code to use qmd as an MCP server
2. Agent has access to `qmd_search`, `qmd_query`, `qmd_get` tools
3. Before starting work, agent can search for relevant past tasks

**Pros:**
- Simple - uses existing QMD MCP server
- No changes to taskyou-mcp needed
- User configures once in Claude Code settings

**Cons:**
- Requires user to set up QMD separately
- Two MCP servers to manage

### Strategy 2: Proxy Mode

TaskYou's MCP server proxies requests to QMD.

```
Claude ──▶ taskyou-mcp ──▶ qmd (subprocess)
              │
              ▼
         taskyou tools
```

**Implementation:**
1. Add qmd_* tools to taskyou-mcp's tool list
2. When qmd_* tools are called, spawn `qmd` subprocess
3. Forward requests and responses

**Pros:**
- Single MCP server for Claude
- Taskyou controls QMD lifecycle

**Cons:**
- More complex implementation
- Tighter coupling

### Strategy 3: Pre-Execution Context Injection

Before task execution, search QMD and inject context.

```
┌─────────────────────────────────────────────────────────┐
│                    Task Start Hook                      │
│                                                         │
│  1. Read task title/body                                │
│  2. Query QMD for related past tasks                    │
│  3. Append relevant context to task prompt              │
│  4. Start Claude with enriched context                  │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**Implementation:**
1. In task executor, before spawning Claude:
   - Query `qmd query "task title" -n 3`
   - Format results as "Related Past Tasks" section
   - Append to system prompt or task context

**Pros:**
- Automatic - no agent action needed
- Context injected upfront
- Works without MCP

**Cons:**
- Static context (can't search dynamically during task)
- May include irrelevant results

### Strategy 4: Hybrid Approach (Recommended for Full Implementation)

Combine strategies for maximum utility:

1. **Pre-execution injection** for immediate context
2. **MCP sidecar** for dynamic search during execution
3. **Post-completion sync** to update index

```
┌───────────────────────────────────────────────────────────────┐
│                      Task Lifecycle                            │
│                                                                │
│  ┌────────────┐     ┌────────────┐     ┌────────────────┐     │
│  │ Pre-Start  │────▶│  Running   │────▶│ Post-Complete  │     │
│  │            │     │            │     │                │     │
│  │ QMD query  │     │ MCP access │     │ Export to QMD  │     │
│  │ + inject   │     │ to qmd_*   │     │ index new task │     │
│  └────────────┘     └────────────┘     └────────────────┘     │
│                                                                │
└───────────────────────────────────────────────────────────────┘
```

## Data Model

### Task Export Format

Each completed task is exported as markdown:

```markdown
---
task_id: 42
project: workflow
status: done
type: code
completed: 2024-01-15
tags: [auth, oauth]
---

# Add Google OAuth

Implement Google OAuth 2.0 login flow for the web app.

## Summary

Added OAuth with passport.js. Tokens stored in Redis with 1h expiry.
Implemented refresh token rotation.

## Key Changes

Files modified:
- src/auth/oauth.ts (new)
- src/auth/strategies/google.ts (new)
- src/middleware/session.ts (modified)
```

### Collection Organization

```
QMD Index
├── ty-tasks           # All completed tasks
├── project-workflow   # workflow project docs
├── project-webapp     # webapp project docs
└── notes             # General notes/meeting transcripts
```

## Implementation Phases

### Phase 1: Basic Sync (This PR)
- [x] ty-qmd extension scaffold
- [x] Export completed tasks to markdown
- [x] Index with QMD
- [x] CLI commands: sync, search, status

### Phase 2: MCP Integration
- [ ] Add qmd MCP server config to Claude Code
- [ ] Document setup for users
- [ ] Test dynamic search during task execution

### Phase 3: Pre-Execution Context
- [ ] Add hook in task executor
- [ ] Query QMD before spawning Claude
- [ ] Format and inject context into prompt

### Phase 4: Advanced Features
- [ ] Auto-sync on task completion (daemon mode)
- [ ] Project-specific collections
- [ ] Search result quality tuning
- [ ] UI integration (show related tasks in TUI)

## Configuration

### QMD Setup

```bash
# Install QMD
bun install -g github:tobi/qmd

# Add tasks collection
qmd collection add ~/.local/share/ty-qmd/tasks --name ty-tasks --mask "*.md"

# Generate embeddings
qmd embed
```

### Claude Code Integration

Add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "qmd": {
      "command": "qmd",
      "args": ["mcp"]
    }
  }
}
```

### ty-qmd Configuration

```yaml
# ~/.config/ty-qmd/config.yaml
qmd:
  binary: qmd

sync:
  auto: true
  interval: 5m
  statuses: [done, archived]

collections:
  tasks: ty-tasks
```

## Security Considerations

1. **Local-only** - QMD runs entirely on-device
2. **Read-only DB access** - ty-qmd opens TaskYou DB read-only
3. **No external data** - Task content stays local
4. **Model downloads** - QMD downloads ~2GB of GGUF models on first use

## Open Questions

1. **Embedding quality** - Are QMD's embeddings good enough for task similarity?
2. **Index size** - How big does the index get with thousands of tasks?
3. **Search latency** - Is hybrid search fast enough for interactive use?
4. **Collection granularity** - Per-project collections vs single global collection?

## References

- [QMD README](https://github.com/tobi/qmd)
- [MCP Specification](https://modelcontextprotocol.io/)
- [TaskYou Architecture](../../DEVELOPMENT.md)

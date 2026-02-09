# ty-qmd

QMD integration for TaskYou. Provides semantic search over task history, project documentation, and knowledge bases during task execution.

## Overview

[QMD](https://github.com/tobi/qmd) is an on-device search engine that combines BM25 full-text search, vector semantic search, and LLM re-ranking. This extension integrates QMD with TaskYou to:

1. **Index task history** - Completed tasks become searchable knowledge
2. **Search during execution** - Claude can query past work for context
3. **Project documentation** - Index markdown, meeting notes, docs alongside tasks

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    TaskYou Task Execution                    │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│   Claude ──MCP──▶ taskyou-mcp ──proxy──▶ qmd mcp server     │
│                        │                       │             │
│                        │                       ▼             │
│                        │              ┌───────────────┐      │
│                        │              │  QMD Index    │      │
│                        │              │  - tasks      │      │
│                        │              │  - docs       │      │
│                        │              │  - notes      │      │
│                        ▼              └───────────────┘      │
│                 ┌─────────────┐                              │
│                 │ TaskYou DB  │◀──────── ty-qmd sync        │
│                 └─────────────┘                              │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## Features

### Task History Search

When a task completes, ty-qmd exports it to a searchable collection:

```bash
# Index completed tasks
ty-qmd sync

# Search past tasks
ty-qmd search "authentication implementation"
```

Each task becomes a document containing:
- Title and description
- Completion summary
- Key files modified
- Project context

### MCP Sidecar Mode

Run QMD as an MCP sidecar during task execution:

```bash
# Start qmd MCP server for Claude to use
ty-qmd serve
```

This exposes QMD tools to Claude:
- `qmd_search` - Fast keyword search
- `qmd_vsearch` - Semantic similarity search
- `qmd_query` - Hybrid search with re-ranking
- `qmd_get` - Retrieve full documents

### Project Documentation Indexing

Index project docs alongside tasks:

```bash
# Add project docs to qmd
ty-qmd index-project ~/Projects/myapp --mask "**/*.md"

# Add meeting notes
ty-qmd index-project ~/Notes/meetings --name meetings
```

## Installation

### Prerequisites

1. Install QMD:
   ```bash
   bun install -g github:tobi/qmd
   ```

2. Build ty-qmd:
   ```bash
   cd extensions/ty-qmd
   go build -o ty-qmd ./cmd
   ```

### Configuration

Config at `~/.config/ty-qmd/config.yaml`:

```yaml
qmd:
  binary: qmd
  index: ~/.cache/qmd/index.sqlite

sync:
  # Auto-sync completed tasks
  auto: true
  # Sync interval when running as daemon
  interval: 5m
  # Task statuses to index
  statuses:
    - done
    - archived
  # Export format for task documents
  format: markdown

collections:
  # Default collection for tasks
  tasks: ty-tasks
  # Project-specific collections
  projects:
    workflow: workflow-docs
```

## Commands

```bash
# Sync completed tasks to QMD
ty-qmd sync [--all] [--project <name>]

# Search across all indexed content
ty-qmd search <query> [-n <count>]

# Run MCP server for Claude integration
ty-qmd serve

# Index project documentation
ty-qmd index-project <path> [--name <collection>] [--mask <glob>]

# Show sync status
ty-qmd status
```

## Integration with TaskYou

### Option 1: MCP Server Chaining

Configure taskyou to spawn qmd MCP server alongside Claude:

```yaml
# In taskyou config
execution:
  mcp_servers:
    - name: qmd
      command: qmd
      args: [mcp]
```

### Option 2: Proxy Mode

The taskyou MCP server can proxy to qmd, exposing its tools:

```go
// In internal/mcp/server.go
// Add qmd tools to tools/list
// Forward qmd_* calls to qmd process
```

### Option 3: Pre-Execution Context

Before executing a task, search for relevant past tasks:

```bash
# In task execution hook
ty-qmd search "$TASK_TITLE" --json | head -5 >> context.md
```

## Use Cases

### 1. Learning from Past Work

```
Task: "Implement OAuth login"

Claude searches: "OAuth authentication implementation"
Finds: Task #42 "Add Google OAuth" - completed 2 weeks ago
Context: Used passport.js, stored tokens in Redis, added refresh logic
```

### 2. Project-Specific Knowledge

```
Task: "Add caching to API"

Claude searches in project docs: "caching architecture"
Finds: architecture.md, redis-patterns.md
Context: Project uses Redis, 15-minute TTL standard, cache-aside pattern
```

### 3. Similar Bug Fixes

```
Task: "Fix race condition in worker"

Claude searches: "race condition fix worker"
Finds: Task #128 "Fix concurrent map access"
Context: Used sync.Mutex, added goroutine-safe map wrapper
```

## Task Export Format

When syncing tasks to QMD, each task is exported as markdown:

```markdown
---
task_id: 42
project: workflow
status: done
completed: 2024-01-15
tags: [auth, oauth]
---

# Add Google OAuth

Implement Google OAuth 2.0 login flow for the web app.

## Summary

Added OAuth with passport.js. Tokens stored in Redis with 1h expiry.
Implemented refresh token rotation.

## Key Files

- src/auth/oauth.ts
- src/auth/strategies/google.ts
- src/middleware/session.ts

## Related Tasks

- #38: Add session management
- #45: Add OAuth scopes
```

## Development

### Building

```bash
cd extensions/ty-qmd
go build -o ty-qmd ./cmd
```

### Testing

```bash
go test ./...
```

## Future Ideas

- **Automatic tagging** - Use QMD's LLM to auto-tag tasks
- **Related task suggestions** - Show similar past tasks when creating new ones
- **Knowledge graph** - Build task relationship graph from semantic similarity
- **Cross-project search** - Search across all indexed projects
- **Embedding visualization** - Visualize task clusters in embedding space

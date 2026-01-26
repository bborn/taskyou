# Linear Backend Implementation Notes

> **Status**: Research complete, implementation deferred
> **Date**: 2026-01-26
> **Branch**: task/620-could-we-shift-from-using-our-own-local

This document captures the research and architecture design for migrating the task management system from local SQLite storage to using Linear as the backend.

## Summary

The proposed approach introduces a **sync layer** between existing SQLite and Linear, treating SQLite as a local cache and execution state store while Linear becomes the source of truth for core task data. This avoids massive refactoring of the 33 files that use `*db.DB` directly.

## Architecture

```
┌─────────────────┐
│     Linear      │  (Source of Truth for Core Data)
│   GraphQL API   │
└────────┬────────┘
         │
┌────────▼────────┐
│  Linear Sync    │  (NEW: Background sync service)
│    Service      │
└────────┬────────┘
         │ Sync Hooks
┌────────▼────────┐
│   SQLite DB     │  (Cache + Execution State)
│  (Unchanged)    │
└────────┬────────┘
         │ *db.DB (unchanged interface)
┌────────▼────────┐
│ TUI / Executor  │  (No changes needed)
└─────────────────┘
```

## Why This Approach?

### Current State Analysis

- **33 files** directly import and use the concrete `*db.DB` type
- **No interface abstraction** exists between consumers and database
- A full interface abstraction would require refactoring 50+ methods across all files
- Estimated effort for full abstraction: 50+ hours

### The Sync Layer Approach

Instead of abstracting the database, we add a sync service that:
1. Listens for local changes via notification hooks
2. Pushes changes to Linear's GraphQL API
3. Polls Linear for remote changes (every 30s)
4. Resolves conflicts (Linear wins for core data)

This requires ~100 lines of changes to existing code vs thousands for full abstraction.

## Field Partitioning

### Core Data (Sync to Linear)

These fields represent the "what" of a task and should live in Linear:

| Field | Linear Mapping |
|-------|----------------|
| ID | linear_id (UUID) |
| Title | Issue title |
| Body | Issue description (markdown) |
| Status | Workflow state |
| Project | Team/Project |
| Type | Label |
| Tags | Labels |
| Summary | Custom field or description suffix |
| PRURL, PRNumber | Attachments or custom fields |
| DangerousMode, Pinned | Custom fields |
| CreatedAt, UpdatedAt | Issue timestamps |
| StartedAt, CompletedAt | Workflow state timestamps |

### Execution State (Local Only)

These fields are machine-specific and transient - they should NEVER sync:

| Field | Why Local Only |
|-------|----------------|
| DaemonSession | tmux session name, regenerated per execution |
| TmuxWindowID | tmux window ID (e.g., @1234), machine-specific |
| ClaudePaneID | tmux pane ID, machine-specific |
| ShellPaneID | tmux pane ID, machine-specific |
| Port | Dynamically allocated from local pool |
| WorktreePath | Local filesystem path |
| BranchName | Can be derived from Linear, but local git state |
| ClaudeSessionID | Claude CLI session, machine-specific |
| Executor | Local execution preference |
| ScheduledAt, LastRunAt | Local scheduling (Linear has own scheduling) |
| LastDistilledAt | Local distillation tracking |

### Related Data

| Data Type | Sync Strategy |
|-----------|---------------|
| task_logs | Keep local - transient execution output |
| task_attachments | Sync to Linear attachments |
| project_memories | Keep local - project knowledge base |
| task_compaction_summaries | Keep local - transcript archives |
| task_search | Rebuild locally from synced data |

## Status Mapping

| Local Status | Linear Workflow State |
|--------------|----------------------|
| backlog | Backlog |
| queued | Todo |
| processing | In Progress |
| blocked | Blocked (custom state or label) |
| done | Done |
| archived | Canceled |

## Database Schema Extensions

### New Tables

```sql
-- Sync metadata linking local tasks to Linear issues
CREATE TABLE IF NOT EXISTS linear_sync (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    linear_id TEXT NOT NULL,          -- Linear issue UUID
    linear_identifier TEXT,           -- e.g., "PROJ-123"
    sync_status TEXT DEFAULT 'synced', -- 'synced', 'pending_push', 'conflict'
    last_synced_at DATETIME,
    remote_updated_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_linear_sync_linear_id ON linear_sync(linear_id);
CREATE INDEX idx_linear_sync_status ON linear_sync(sync_status);

-- Linear configuration
CREATE TABLE IF NOT EXISTS linear_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Keys: api_key, team_id, sync_enabled, last_full_sync
```

### Schema Modifications

```sql
-- Add Linear team mapping to projects
ALTER TABLE projects ADD COLUMN linear_team_id TEXT DEFAULT '';
ALTER TABLE projects ADD COLUMN linear_team_key TEXT DEFAULT '';
```

## Linear API Details

### Endpoint
```
https://api.linear.app/graphql
```

### Authentication
```
Authorization: Bearer <LINEAR_API_KEY>
```

### Key Mutations

```graphql
# Create issue
mutation IssueCreate($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue {
      id
      identifier
      title
      url
    }
  }
}

# Update issue
mutation IssueUpdate($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) {
    success
  }
}
```

### Key Queries

```graphql
# Get issues updated since timestamp
query IssuesUpdatedSince($since: DateTime!) {
  issues(filter: { updatedAt: { gt: $since } }) {
    nodes {
      id
      identifier
      title
      description
      state { id name }
      labels { nodes { id name } }
      updatedAt
    }
  }
}
```

### Rate Limits
- API key: 1,500 requests/hour/user
- Complexity: 250,000 points/hour/user

## Implementation Phases

### Phase 1: Linear Client & Schema
1. Create `internal/linear/client.go` - GraphQL client using `github.com/hasura/go-graphql-client`
2. Create `internal/linear/types.go` - Data types and mapping functions
3. Add migrations to `internal/db/sqlite.go` for linear_sync tables

### Phase 2: Sync Service
4. Create `internal/linear/sync.go` - Background sync service with:
   - Local change detection via notification hooks
   - Remote polling (30s intervals)
   - Conflict resolution (Linear wins)
   - Offline queue with retry

5. Add sync hooks to `internal/db/tasks.go`:
   - Optional `SyncNotifier` interface
   - Call notifier after CreateTask, UpdateTask, DeleteTask

### Phase 3: Integration
6. Modify `cmd/task/main.go` and `cmd/taskd/main.go` - Initialize sync service
7. Modify `internal/ui/settings.go` - Linear configuration UI
8. Create migration tools for bulk import/export

### Phase 4: Polish
9. Add sync status indicator to UI
10. Write tests

## Key Design Decisions

1. **SQLite remains primary interface** - No changes to TUI/executor code
2. **Polling sync (30s intervals)** - Simpler than webhooks, acceptable delay
3. **Linear wins conflicts** - For core data, remote takes precedence
4. **Attachments sync to Linear** - Screenshots and files upload to Linear issues
5. **Local execution state never syncs** - Process IDs are machine-specific
6. **Offline-first** - Changes queue locally, sync when connected

## Files to Create/Modify

### New Files
- `internal/linear/client.go` - GraphQL client
- `internal/linear/types.go` - Data types and mapping
- `internal/linear/sync.go` - Sync service
- `internal/linear/migration.go` - Migration tools
- `internal/linear/sync_test.go` - Tests

### Modified Files
- `internal/db/sqlite.go` - Add linear_sync table migration
- `internal/db/tasks.go` - Add sync notification hooks (~50 lines)
- `cmd/task/main.go` - Initialize sync service
- `cmd/taskd/main.go` - Initialize sync service for daemon
- `internal/ui/settings.go` - Linear configuration UI

## Dependencies

```go
require (
    github.com/hasura/go-graphql-client v0.10.0
)
```

## Estimated Scope

- ~1500 lines of new code
- ~100 lines of modifications to existing code
- No breaking changes to existing functionality

## Alternative Approaches Considered

### Full Interface Abstraction
Create a `TaskRepository` interface and implement both `SQLiteTaskRepository` and `LinearTaskRepository`.

**Pros**: Clean separation, easy to swap backends
**Cons**: Requires refactoring 33 files, 50+ hours of work, high risk of bugs

### Linear as Primary (No Local Cache)
Remove SQLite entirely, use Linear API directly.

**Pros**: Single source of truth, no sync complexity
**Cons**: No offline capability, slow UI (API latency), execution state needs storage somewhere

### Webhook-Based Sync
Use Linear webhooks for real-time sync instead of polling.

**Pros**: Instant sync from Linear
**Cons**: Requires webhook server, more complex setup, may not be needed for single-user tool

## References

- [Linear API Documentation](https://linear.app/developers)
- [Linear GraphQL Schema (Apollo Studio)](https://studio.apollographql.com/public/Linear-API/schema/reference)
- [hasura/go-graphql-client](https://github.com/hasura/go-graphql-client)

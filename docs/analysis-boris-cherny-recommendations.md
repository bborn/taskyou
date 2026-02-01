# Analysis: Boris Cherny's Claude Code Improvements vs TaskYou

**Source:** [Tweet thread by Boris Cherny](https://x.com/bcherny/status/2017742741636321619)  
**Date:** January 31, 2026

## Summary of Recommendations

Boris Cherny's tweet thread outlines several improvements to make Claude Code more effective:

1. **Memory Files** - Store context in `.claude/memory/` files that get tokenized at startup
2. **Skills System** - Reusable templates/skills that Claude can load when needed
3. **Background Agents** - Parallel exploration agents for codebase understanding
4. **State Management** - Better tracking of what Claude has seen and done
5. **I/O Management** - Improved handling of conversation history and resumption

## Current TaskYou Implementation

### ✅ What TaskYou Already Has

#### 1. Project Context (Memory Files) - **FULLY IMPLEMENTED**

TaskYou already implements the "memory files" concept via:

- **MCP Tools:**
  - `workflow_get_project_context` - Retrieves cached project context
  - `workflow_set_project_context` - Saves auto-generated project context
  
- **Database Storage:** `projects.context` column stores the cached exploration results

- **Usage Pattern:**
  ```
  1. Agent calls workflow_get_project_context at task start
  2. If context exists, uses it to skip exploration
  3. If empty, explores codebase once
  4. Saves summary via workflow_set_project_context for future tasks
  ```

**Location in code:**
- MCP implementation: `internal/mcp/server.go`
- Database functions: `internal/db/tasks.go` (`GetProjectContext`, `SetProjectContext`)
- Schema: `ALTER TABLE projects ADD COLUMN context TEXT DEFAULT ''`

**Documentation:** This feature is mentioned in task guidance but could be more prominent in user docs.

#### 2. State Management - **FULLY IMPLEMENTED**

TaskYou tracks comprehensive task state:

- **Task Lifecycle:** backlog → queued → processing → blocked → done
- **Real-time Hooks:** Pre/post tool use, notifications, stop events
- **Session Tracking:** `claude_session_id` for conversation resumption
- **Process Management:** tmux session IDs, pane IDs, PIDs
- **Worktree Isolation:** Each task gets isolated git worktree

**Location in code:**
- Hooks: `internal/hooks/hooks.go`
- Executor: `internal/executor/executor.go`
- Database schema: Multiple columns in `tasks` table

#### 3. Session Resume - **FULLY IMPLEMENTED**

TaskYou supports resuming interrupted work:

- **Claude session ID tracking:** `tasks.claude_session_id`
- **Resume with feedback:** Retry mechanism with user input
- **Conversation history:** Stored in task logs

**Location in code:**
- `internal/executor/claude_executor.go` - `Resume()` method
- MCP retry tools for providing feedback

#### 4. Parallel Task Execution - **PARTIALLY IMPLEMENTED**

TaskYou supports parallel execution via:

- **Git worktrees:** Each task runs in isolated worktree
- **Multiple executors:** Can run multiple tasks simultaneously
- **Port allocation:** Each task gets unique port (3100-4099)

**What's different from Boris's vision:**
- No explicit "background exploration agent" that runs in parallel
- Parallel execution is task-based, not agent-based

### ⚠️ Gaps and Opportunities

#### 1. Skills System - **NOT IMPLEMENTED (but task_types exists)**

**What Boris envisions:**
- Reusable templates/skills Claude can load
- Context for specific patterns (e.g., "how to write tests in this codebase")

**What TaskYou has:**
- `task_types` table with type-specific instructions
- Project-level instructions
- Not quite the same as reusable "skills"

**Opportunity:**
We could enhance task types to be more like reusable skills:
- Add a `skills` table with reusable patterns/templates
- Allow tasks to reference multiple skills
- Store common patterns (testing, API design, error handling, etc.)

#### 2. Background Exploration Agents - **NOT IMPLEMENTED**

**What Boris envisions:**
- Separate agent that explores codebase in parallel
- Runs in background while main agent works
- Finds relevant files/patterns proactively

**What TaskYou could do:**
- Add a "explore codebase" task type that runs in background
- Use project context as the output
- Could trigger automatically when new files are detected

#### 3. Better Memory File Documentation - **NEEDS IMPROVEMENT**

**Current state:**
- Feature exists and works well
- Documented in task guidance (AGENTS.md)
- Not prominently mentioned in user-facing docs

**Opportunity:**
- Add section to README about project context caching
- Document best practices for using context
- Show examples of good context summaries

## Recommended Enhancements

### Priority 1: Document Existing Features Better

1. **Update README** to highlight project context feature
2. **Add examples** of effective context summaries
3. **Create user guide** for MCP tools

### Priority 2: Enhance Skills System

Create a proper skills/templates system:

```sql
CREATE TABLE skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    category TEXT DEFAULT '',  -- testing, api-design, frontend, etc.
    content TEXT NOT NULL,      -- The actual skill/template content
    usage_count INTEGER DEFAULT 0,
    created_at DATETIME
);

CREATE TABLE task_skills (
    task_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    skill_id INTEGER REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, skill_id)
);
```

**MCP Tools:**
- `workflow_list_skills` - Browse available skills
- `workflow_load_skill` - Load a skill into context
- `workflow_create_skill` - Save a new reusable skill

### Priority 3: Background Exploration Agent

Add task type or feature for background exploration:

- **Task type:** "exploration" that runs in parallel
- **Output:** Saves to project context automatically
- **Trigger:** Manual or automatic on file changes

### Priority 4: Enhanced Context Management

Improve project context features:

- **Versioning:** Track when context was last updated
- **Auto-refresh:** Detect when context is stale (e.g., many new files)
- **Context templates:** Provide structure for good context
- **Partial updates:** Update specific sections without re-exploring everything

## Implementation Plan

### Phase 1: Documentation (Quick Win)
- [ ] Add "Project Context" section to README
- [ ] Document MCP tools in user guide
- [ ] Add examples to AGENTS.md
- [ ] Update task guidance template

### Phase 2: Skills System
- [ ] Design skills schema
- [ ] Implement database functions
- [ ] Add MCP tools for skills
- [ ] Create UI for managing skills
- [ ] Seed with common skills (testing, API design, etc.)

### Phase 3: Enhanced Context
- [ ] Add context versioning
- [ ] Implement staleness detection
- [ ] Create context templates
- [ ] Add UI for viewing/editing context

### Phase 4: Background Agents (Future)
- [ ] Design background agent architecture
- [ ] Implement exploration agent
- [ ] Add UI indicators for background tasks
- [ ] Create auto-trigger mechanisms

## Conclusion

TaskYou already implements the core of Boris Cherny's recommendations, particularly around **memory files (project context)** and **state management**. The main gaps are:

1. **Skills system** - Could be enhanced from task_types
2. **Background agents** - Not currently implemented
3. **Documentation** - Existing features need better visibility

The quickest wins are improving documentation and enhancing the skills/task_types system to be more like reusable templates.

## References

- [Boris Cherny's Tweet Thread](https://x.com/bcherny/status/2017742741636321619)
- [TaskYou MCP Implementation](../internal/mcp/server.go)
- [TaskYou Database Schema](../internal/db/sqlite.go)
- [TaskYou Executor](../internal/executor/executor.go)

# OpenAI Symphony Spec Review for TaskYou

## Executive Summary

The [OpenAI Symphony SPEC](https://github.com/openai/symphony/blob/main/SPEC.md) defines a service that orchestrates coding agents to work on issues from a tracker (Linear). TaskYou already covers most of Symphony's core functionality and in several areas goes significantly beyond it. Full "compliance" isn't the right framing -- Symphony is an opinionated spec for a specific architecture (Linear-driven, Codex agent, WORKFLOW.md config). Instead, this document maps the concepts, identifies gaps worth closing, and highlights areas where TaskYou is already ahead.

## Architecture Comparison

| Concept | Symphony | TaskYou | Notes |
|---------|----------|---------|-------|
| **Core model** | Daemon that polls Linear for issues and dispatches Codex agents | Daemon/TUI that manages tasks from local DB and dispatches pluggable AI agents | Symphony is tracker-driven; TaskYou is self-contained with optional integrations |
| **Issue source** | Linear (with adapter abstraction) | Local SQLite + MCP tools for creation | TaskYou could add tracker adapters as an input source |
| **Agent runtime** | Codex app-server (JSON-RPC over stdio) | Claude, Codex, Gemini, Pi, OpenClaw, OpenCode (pluggable factory) | TaskYou is more flexible |
| **Workspace isolation** | Per-issue directories under configurable root | Git worktrees per task (`.task-worktrees/`) | TaskYou's approach is more sophisticated |
| **Config** | `WORKFLOW.md` with YAML front matter + prompt template | SQLite settings + per-project DB config | Different approach, both effective |
| **State machine** | Unclaimed → Claimed → Running → RetryQueued → Released | backlog → queued → processing → blocked → done → archived | TaskYou has richer human-in-the-loop states |
| **Concurrency** | Configurable limits (global + per-state) | Unbounded (all queued tasks run in parallel) | Gap: TaskYou lacks concurrency controls |
| **Retry** | Exponential backoff with continuation retries | Manual retry with feedback, session resume | Different philosophy: Symphony auto-retries, TaskYou is human-driven |
| **Observability** | Structured logs + optional HTTP dashboard + JSON API | TUI Kanban + detail view + task logs + `ty tail` | Different mediums, similar goals |
| **Hooks** | `after_create`, `before_run`, `after_run`, `before_remove` (in WORKFLOW.md) | `task.started`, `task.blocked`, `task.done`, `task.failed` (script-based) | Both have hooks but at different lifecycle points |

## What Symphony Has That TaskYou Doesn't

### 1. Concurrency Control (Priority: High)
Symphony has `agent.max_concurrent_agents` (global limit, default 10) and `agent.max_concurrent_agents_by_state` (per-state limits). TaskYou runs all queued tasks in parallel with no limit.

**Recommendation**: Add a `max_concurrent_tasks` setting. This is valuable for resource management, especially when running multiple expensive AI agents.

### 2. External Issue Tracker Integration (Priority: Medium)
Symphony is fundamentally driven by Linear issues. TaskYou is self-contained with its own task database. Symphony's tracker adapter abstraction (fetch candidates, refresh states, fetch terminal) is a clean pattern.

**Recommendation**: Consider adding an optional "tracker sync" feature that can pull tasks from Linear/GitHub Issues. This would make TaskYou usable in team workflows where work is tracked externally. The adapter pattern from Symphony (Section 11) is worth modeling.

### 3. Automatic Retry with Exponential Backoff (Priority: Medium)
Symphony auto-retries failed tasks with `delay = min(10000 * 2^(attempt - 1), max_retry_backoff_ms)`. TaskYou requires manual retry with human feedback.

**Recommendation**: Add optional auto-retry for transient failures (agent crashes, timeouts) while keeping the current human-driven retry for semantic failures (needs input, wrong approach). A `max_retries` setting per project/task would be useful.

### 4. Stall Detection (Priority: Medium)
Symphony detects stalled agent sessions via `codex.stall_timeout_ms` (default 5 min) -- if no events for that duration, it kills and retries.

**Recommendation**: Add stall detection to the executor. If a task's agent hasn't produced output for a configurable duration, mark it as blocked or auto-retry.

### 5. WORKFLOW.md / Prompt Template System (Priority: Low-Medium)
Symphony uses a repo-level `WORKFLOW.md` with YAML front matter for config and a Liquid template for the prompt. Variables like `{{ issue.title }}` and `{{ attempt }}` are rendered per-task.

**Recommendation**: TaskYou already has per-project `instructions` and task types with prompt templates. The WORKFLOW.md pattern is interesting for version-controlled workflow config, but TaskYou's DB-driven approach is more dynamic. Could add support for reading a `WORKFLOW.md` or `.taskyou.yml` from project root as an alternative config source.

### 6. Active Run Reconciliation (Priority: Low-Medium)
Symphony reconciles running tasks against the tracker every poll tick -- if an issue was closed/cancelled externally, the running agent is stopped.

**Recommendation**: For tasks synced from external trackers, reconciliation would be essential. For local tasks, the current interrupt mechanism is sufficient.

### 7. Token/Usage Tracking (Priority: Low)
Symphony tracks `input_tokens`, `output_tokens`, `total_tokens`, and `seconds_running` per session and aggregates them.

**Recommendation**: Add token usage tracking from executor output. Store in a new `task_metrics` table. Display in TUI and expose via API.

### 8. HTTP API / Dashboard (Priority: Low)
Symphony has an optional HTTP server with `GET /api/v1/state`, `GET /api/v1/<issue>`, and `POST /api/v1/refresh` endpoints plus an HTML dashboard.

**Recommendation**: TaskYou already has an SSH server for remote TUI access, which is arguably better for individual developers. An HTTP API could be useful for integrations but isn't urgent.

### 9. Dynamic Config Reload via File Watching (Priority: Low)
Symphony watches `WORKFLOW.md` for changes and hot-reloads config without restart.

**Recommendation**: TaskYou's DB-driven config is already live (changes apply immediately). If a file-based config is added, file watching would be needed.

### 10. Startup Terminal Workspace Cleanup (Priority: Low)
Symphony cleans up workspaces for terminal-state issues on startup.

**Recommendation**: TaskYou already has `cleanupStaleWorktrees()` on an hourly schedule. Could add a startup sweep as well.

## What TaskYou Has That Symphony Doesn't

### 1. Human-in-the-Loop Completion
TaskYou's design where agents can't mark tasks as `done` (only humans can) is a significant safety feature. Symphony lets agents run to completion and considers a "successful exit" as done.

### 2. Pluggable AI Executors
TaskYou supports Claude, Codex, Gemini, Pi, OpenClaw, and OpenCode. Symphony is tied to the Codex app-server protocol.

### 3. Interactive TUI (Kanban Board)
TaskYou has a rich terminal UI with Kanban views, detail panels, tmux integration, and real-time log streaming. Symphony's status surface is optional and minimal.

### 4. Git Worktree Integration
TaskYou creates real git worktrees with branch management, `.claude/` config symlinking, and the Spotlight sync feature. Symphony just creates plain directories.

### 5. MCP Server Per Task
TaskYou gives each task an MCP server with tools for completion, input requests, screenshots, task creation, and context caching. Symphony has no equivalent.

### 6. Session Resume
TaskYou can resume Claude sessions across retries via `--resume <session-id>`. Symphony creates fresh threads for continuation.

### 7. Task Dependencies
TaskYou has a dependency system where tasks can block other tasks, with automatic unblocking when blockers complete.

### 8. Context Caching
Project-level codebase context that persists across tasks, eliminating redundant exploration.

### 9. Tmux Integration
Tasks run in tmux windows for interactive shell access during execution. Developers can attach to running agent sessions.

### 10. Attachments
Tasks support file attachments (screenshots, documents) stored in the database and accessible via MCP.

## Could TaskYou Be "Symphony Compliant"?

**Short answer: It could implement the core Symphony behaviors, but full compliance wouldn't make sense.**

Symphony compliance requires:
1. Linear as the issue source -- TaskYou uses its own DB
2. Codex app-server protocol -- TaskYou uses pluggable executors
3. `WORKFLOW.md` as the config format -- TaskYou uses SQLite
4. Specific JSON-RPC handshake protocol -- TaskYou wraps CLI executors

Instead of compliance, a better approach is **Symphony-compatible mode**:

### Proposed: Symphony Adapter Layer

Add an optional `symphony` mode that:
1. **Reads `WORKFLOW.md`** from a project directory (parses YAML front matter + prompt template)
2. **Syncs tasks from Linear** using the adapter pattern (fetch candidates, refresh states)
3. **Applies concurrency limits** from the workflow config
4. **Uses the prompt template** for task execution (rendering issue variables)
5. **Implements retry backoff** matching Symphony's formula
6. **Exposes the HTTP API** (`/api/v1/state`, etc.) for interop

This would let TaskYou participate in Symphony-compatible workflows while keeping all its unique features.

## Prioritized Recommendations

### Immediate Value (implement now)
1. **Add `max_concurrent_tasks` setting** -- simple, high-value guardrail
2. **Add stall detection** -- kill/retry tasks with no output for N minutes
3. **Add auto-retry for transient failures** -- with configurable `max_retries`

### Medium-Term (next quarter)
4. **Add token/usage tracking** -- capture from executor output, store in DB
5. **Add optional Linear/GitHub Issues sync** -- pull external issues as tasks
6. **Add `WORKFLOW.md` support** -- read project config from file as alternative to DB

### Long-Term (future consideration)
7. **Symphony-compatible HTTP API** -- for dashboard integrations
8. **Active run reconciliation** -- for externally-synced tasks
9. **Per-state concurrency limits** -- fine-grained control

## Conclusion

TaskYou and Symphony solve the same fundamental problem (orchestrating AI coding agents against a task queue) but from different perspectives:

- **Symphony** is a **team-oriented daemon** designed for CI-like automated workflows driven by an external issue tracker. It's headless, auto-retrying, and optimized for throughput.
- **TaskYou** is a **developer-oriented tool** designed for interactive workflows with human oversight. It's TUI-first, human-in-the-loop, and optimized for developer experience.

The most valuable takeaways from Symphony are the operational hardening features: concurrency limits, stall detection, auto-retry, and usage tracking. These can be adopted without changing TaskYou's core philosophy.

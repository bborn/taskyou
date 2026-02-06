# ADR: Cloudflare Code Mode MCP Review

**Status:** Review
**Date:** 2025-02-06
**Source:** https://blog.cloudflare.com/code-mode/

## Context

Cloudflare released "Code Mode" — an approach that changes how AI agents interact with MCP servers. Instead of traditional tool-calling (LLM generates tool-call tokens one at a time), Code Mode converts MCP tool schemas into TypeScript APIs and has the LLM write code that calls those APIs. The code executes in sandboxed V8 isolates (Cloudflare Workers).

### How Code Mode Works

1. MCP server exposes tool schemas
2. Schemas are converted to TypeScript function signatures with JSDoc
3. LLM receives the TypeScript API as context
4. LLM generates code that chains multiple tool calls together
5. Code executes in a V8 isolate sandbox
6. Results return via console output

### Key Benefits Claimed

- Agents handle significantly more tools and greater complexity
- Multi-step operations run efficiently (no LLM round-trip between steps)
- Leverages LLMs' strong code generation training vs limited tool-calling training
- API keys stay hidden in bindings (never exposed to generated code)
- Lightweight sandboxing via V8 isolates vs containers

## Analysis for TaskYou

### Current State

TaskYou exposes 8 MCP tools via JSON-RPC 2.0 over stdio:

- `taskyou_complete` — mark task done
- `taskyou_needs_input` — request user clarification
- `taskyou_create_task` — create new task
- `taskyou_list_tasks` — list active tasks
- `taskyou_show_task` — view task details
- `taskyou_get_project_context` — retrieve cached project context
- `taskyou_set_project_context` — save project context
- `taskyou_screenshot` — capture screen

### Where Code Mode Would Help

**1. Batch Operations (Medium Value)**

Common multi-step patterns in TaskYou:
- List tasks → show each task → create follow-up tasks
- Get project context → (if empty) set project context → complete task

With Code Mode, Claude could write:
```typescript
const tasks = await taskyou_list_tasks({ status: "queued" });
for (const task of tasks) {
  const details = await taskyou_show_task({ task_id: task.id });
  if (details.status === "blocked") {
    await taskyou_create_task({ title: `Unblock: ${details.title}` });
  }
}
```

Instead of 3+ round-trips through the LLM, this executes as a single code block.

**2. Tool Scaling (Future Value)**

Currently 8 tools is well within LLM tool-calling limits. But if TaskYou grows to include:
- Git operations (branch, PR, merge)
- CI/CD integration
- Team collaboration tools
- Webhook management
- Metrics/analytics

Then Code Mode's ability to handle 50+ tools in a single API surface becomes very attractive.

**3. Sandboxed Execution (High Value — Different Angle)**

The V8 isolate sandboxing is interesting not for MCP tool calls, but for task execution itself. Currently TaskYou runs Claude with `--dangerously-skip-permissions` in tmux sessions. A Code Mode-inspired approach could:
- Have Claude generate code instead of shell commands
- Execute that code in a sandboxed environment
- Limit filesystem/network access to the worktree

This is the most interesting angle but requires significant architecture work.

### Where Code Mode Doesn't Help

**1. Current Tool Count**
With only 8 tools, traditional MCP tool-calling works fine. The LLM has no trouble selecting the right tool.

**2. Simple Operations**
Most TaskYou MCP calls are single-step: mark done, ask for input, take screenshot. These don't benefit from code-based batching.

**3. Infrastructure Requirements**
Code Mode requires Cloudflare Workers or workerd for V8 sandboxing. TaskYou runs locally and doesn't depend on Cloudflare infrastructure.

**4. Claude Code Already Uses MCP Natively**
Claude Code (TaskYou's primary executor) has native MCP support. Adding a Code Mode layer would add complexity without clear benefit for the current tool set.

## Recommendations

### Short Term: No Changes Needed

TaskYou's 8 MCP tools are well-served by standard tool-calling. The current architecture is simple, reliable, and well-tested.

### Medium Term: Watch for Batch Tool Pattern

If we find Claude making repetitive sequences of MCP calls (e.g., listing then showing multiple tasks), consider adding a `taskyou_batch` tool that accepts multiple operations:

```json
{
  "name": "taskyou_batch",
  "description": "Execute multiple TaskYou operations in sequence",
  "inputSchema": {
    "type": "object",
    "properties": {
      "operations": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "tool": { "type": "string" },
            "args": { "type": "object" }
          }
        }
      }
    }
  }
}
```

This captures Code Mode's core benefit (batch multiple operations) without requiring V8 isolates.

### Long Term: Sandboxed Task Execution

The most valuable lesson from Code Mode is the sandboxing model. If TaskYou moves toward more autonomous agent execution, consider:

1. **Code-first execution**: Have agents generate structured code (Go, TypeScript, or shell scripts) that runs in a restricted environment
2. **Capability-based security**: Instead of `--dangerously-skip-permissions`, grant specific capabilities (file access, network, git operations) per task
3. **Execution logs**: Code-based execution produces cleaner, more auditable logs than interactive terminal sessions

This aligns with the broader industry trend toward sandboxed agent execution (Cloudflare Workers, E2B, Modal, etc.).

## Decision

No immediate implementation changes. This document serves as a reference for future architecture decisions around:
- Tool scaling beyond 10-15 MCP tools
- Batch operation optimization
- Sandboxed execution environments

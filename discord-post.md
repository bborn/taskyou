# TaskYou Discord Post for Ruby AI Builders

---

Hey Ruby AI Builders! 👋

Just wanted to share **TaskYou** - a personal task management system I've been building that makes working with AI coding agents *actually practical*.

## What makes it awesome:

🎯 **Kanban board in your terminal** - Beautiful TUI built with Bubble Tea. Drag tasks through Backlog → Queued → Processing → Done.

🤖 **Pluggable AI executors** - Works with Claude Code, OpenAI Codex, Gemini, Pi, OpenClaw, or OpenCode. Pick your favorite agent per task.

🌿 **Git worktrees for isolation** - Each task runs in its own worktree. No more "oops, I broke the main branch while testing." Run 10 tasks in parallel without conflicts.

🧠 **Project context caching** - The agent explores your codebase *once*, saves what it learned, and reuses that context across all future tasks. No more burning tokens re-discovering your app structure.

📡 **100% scriptable CLI** - Every action is accessible via CLI. AI agents can create tasks, read executor output, send input to running executors, and orchestrate your entire queue programmatically.

🔌 **MCP integration** - Native Model Context Protocol support. Claude can read your task board, execute tasks, and provide feedback—all through structured tool calls.

🎣 **Event hooks** - Run scripts when tasks change state (created, blocked, completed, failed). Perfect for notifications or custom workflows.

📮 **Email interface** - There's even an extension to create tasks via email. "Forward this bug report to tasks@yourdomain.com" and boom, it's queued.

## Why it's great for Ruby devs:

The worktree setup means your Rails app can run multiple tasks in parallel with isolated databases and ports. Configure `database.yml` to use `WORKTREE_TASK_ID` and each task gets its own DB. No conflicts, no cleanup needed.

```yaml
development:
  database: myapp_dev<%= ENV['WORKTREE_TASK_ID'] ? "_task#{ENV['WORKTREE_TASK_ID']}" : "" %>
```

Plus there's SSH server support - run it on a VPS and access your task board from anywhere.

## Try it:

```bash
curl -fsSL taskyou.dev/install.sh | bash
ty  # launches the TUI
```

Built in Go but designed for working with any codebase. The MCP integration means it plays really well with Claude Code especially.

GitHub: https://github.com/bborn/taskyou

Happy to answer questions! 🚀

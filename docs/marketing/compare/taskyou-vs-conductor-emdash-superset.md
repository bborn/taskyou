# TaskYou vs Conductor / Emdash / Superset

**One line:** Conductor, Emdash, and Superset isolate a worktree per branch and help you review
the diff; TaskYou does that *and* runs the work in the background on a queue, across six
executors, on whatever surface you happen to be holding.

This is the comparison closest to TaskYou's own job. These tools share TaskYou's core insight —
**give every agent its own isolated git worktree** — so the differences are about everything
*around* the worktree: the queue, the lifecycle, the executor choice, and where you can drive it
from.

---

## What they share with TaskYou

Conductor, Emdash, and Superset are worktree-orchestration tools for coding agents. Like TaskYou,
they:

- Run each agent in an **isolated git worktree** so parallel work doesn't collide.
- Center the workflow on **diffs and pull requests** rather than raw terminal panes.
- Aim to let you run **multiple agents in parallel** on the same repo.

If the terminal-multiplexer tools are one camp, this is the "worktree orchestrator" camp — and TaskYou lives
here too. So we'll be precise about the differences instead of hand-waving.

## What TaskYou does differently

| | **TaskYou** | Conductor / Emdash / Superset |
|---|---|---|
| Worktree-per-task isolation | ✅ | ✅ (the shared idea) |
| Diff / PR review flow | ✅ + PR-aware completion lifecycle | ✅ |
| **Kanban board** as the home surface | ✅ Backlog / In Progress / Blocked / Done | ◐ varies; often a list or per-agent view |
| **Queue + background execution** | ✅ stack a column, walk away | ◐ varies by tool |
| **Pluggable executors** | ✅ Claude, Codex, Gemini, Pi, OpenClaw, OpenCode — per task | ◐ usually one or two |
| **Routines** — scheduled unattended runs feeding the queue | ✅ | ❌ |
| **PR-aware completion** (task auto-`done` on merge/close) | ✅ | ◐ |
| **Multi-surface** (TUI · web · desktop · email · SSH · Chrome) | ✅ | ◐ usually one app (often desktop or web) |
| **Fully scriptable CLI + MCP** for agents to drive the queue | ✅ | ◐ |
| Open source | ✅ MIT | ◐ varies |

The pattern: this camp tends to ship **one polished surface** (a desktop app or a web app), **one
or two agents**, and a **worktree-plus-diff** flow. TaskYou matches the worktree-plus-diff flow,
then adds the things that make it an *orchestrator* rather than a launcher:

1. **A real queue.** Drop ten tasks and walk away. The board works the column down in the
   background — you're not launching agents one at a time.
2. **Executor choice per task.** Claude Code for the gnarly refactor, Codex for the boilerplate,
   Gemini or Pi or OpenClaw or OpenCode for whatever fits — on the same board.
3. **PR-aware completion.** A task that opens a PR goes to `blocked` for human review, then is
   promoted to `done` automatically when the PR merges or closes. The board mirrors your repo.
4. **Routines.** Named, unattended agent runs — scouts and monitors — that wake on a schedule and
   *file tasks into your queue*. Your backlog can fill itself.
5. **Surface independence.** The same board in a terminal, a browser, a desktop app, your email,
   and over SSH. Most tools in this camp are a single app.

---

## When to pick which

**Pick Conductor / Emdash / Superset when:**

- You want a single, polished, opinionated app and that app's chosen agent fits your workflow.
- A focused worktree-and-diff experience is exactly the scope you want — no queue, no scheduler,
  no extra surfaces.
- Their specific UX or integrations match how your team already works.

**Pick TaskYou when:**

- You want the worktree-and-diff flow **plus a queue you can stack and walk away from.**
- You want to **mix executors** across tasks instead of being locked to one agent.
- You want **routines** that feed your backlog on a schedule, unattended.
- You want to **drive from anywhere** — terminal, browser, desktop, phone (email), or SSH.
- You want it **open source (MIT)** and scriptable to the last command.

---

*Accuracy note: TaskYou capabilities are documented in the [README](../../../README.md) and the
`extensions/ty-*` READMEs. The competitor column is deliberately marked `◐` where these tools
differ in scope or have shipped features we haven't individually verified — this page describes
the **category** difference (launcher vs orchestrator), not a feature-by-feature audit of each
product. Corrections welcome via PR.*

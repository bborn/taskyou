# TaskYou vs tmux / Zellij

**One line:** tmux and Zellij multiplex *terminals*; TaskYou multiplexes *tasks* — and since
TaskYou is built on tmux under the hood, you get the panes anyway.

This is the friendliest comparison on the list, because it isn't really a competition. tmux and
Zellij are terminal multiplexers — they keep sessions alive and tile panes. TaskYou is an
autonomy-first task orchestrator that *uses* a multiplexer as plumbing. The question isn't
"which one" — it's "what sits on top."

---

## What TaskYou is

A Kanban board for coding agents. You drop a task in a column, an agent picks it up in its own
isolated git worktree, runs in the background, and the card tracks itself to a merged PR. One
agent per task, a worktree per agent.

Under the hood, each executor runs in a **tmux window** inside a daemon session:

```
task-daemon-{PID}              (tmux session)
├── _placeholder               (keeps the session alive)
├── task-123                   (window for task 123)
│   ├── pane 0: executor       (Claude / Codex / … output)
│   └── pane 1: shell          (workdir access)
├── task-124                   (window for task 124)
└── ...
```

So TaskYou doesn't replace tmux — it *programs* it. The difference is everything that wraps
those panes: a board, a queue, worktree isolation, PR tracking, and a database that remembers
what every task is doing.

## What tmux / Zellij give you

- **Persistent sessions** — your work survives a disconnect; reattach later.
- **Panes and windows** — tile, split, and navigate terminals by hand.
- **Scriptability** — tmux especially is automatable (TaskYou leans on exactly this).
- **Zellij extras** — a modern, discoverable UX with layouts, a status bar, and plugins.

What they *don't* do — by design, because they're general-purpose terminal tools — is
understand your work. A pane is a pane. tmux doesn't know that window 3 is an agent, that it's
blocked waiting on you, that it's working in an isolated worktree, or that it just opened a PR.

---

## What TaskYou adds on top of the multiplexer

| | **TaskYou** | **tmux / Zellij** |
|---|---|---|
| Persistent sessions | ✅ (via the daemon) | ✅ |
| Tiled panes | ✅ (executor + shell per task) | ✅ |
| **Knows a pane is an agent** | ✅ | ❌ |
| **Kanban board** of work | ✅ | ❌ |
| **Queue + background execution** | ✅ | ❌ — you launch every process by hand |
| **Worktree-per-task isolation** | ✅ automatic | ❌ — you `git worktree add` yourself |
| **PR-aware completion** | ✅ | ❌ |
| **Pluggable AI executors** | ✅ 6 of them | ❌ — not its job |
| **Task database + history** | ✅ SQLite | ❌ — panes are ephemeral state |
| **Routines** (scheduled unattended runs) | ✅ | ❌ |
| **Web / desktop / email / SSH surfaces** | ✅ | terminal only |

The honest way to say it: **you *could* build a poor man's TaskYou out of tmux** — a session
per task, a `git worktree add` for each, a shell loop to launch agents, and a wall of sticky
notes to track which is which. TaskYou is what you get when someone builds that properly and
adds a board, a queue, worktree lifecycle, PR tracking, and a database so nothing gets lost.

---

## When to pick which

**Reach for tmux / Zellij when:**

- You want a general-purpose multiplexer for *any* terminal work, not just agents.
- You want to hand-manage panes and sessions yourself.
- You're composing your own tooling and want the lowest-level, most scriptable substrate.

**Reach for TaskYou when:**

- Your terminals are mostly **coding agents** and you want to manage them as *work*, not panes.
- You want **queue + background execution + worktree isolation + PR tracking** without scripting
  it together yourself.
- You want the same board on the **web, a desktop app, email, and over SSH** — not just in a
  terminal.

**You're already using both.** TaskYou runs on tmux. If you live in tmux today, TaskYou is the
task layer that sits on top of it — and you can still `tmux attach` to the daemon session and
poke at any task's panes by hand whenever you want to take the wheel.

---

*Accuracy note: the tmux architecture shown above is TaskYou's actual executor model, documented
in the [README](../../../README.md) under "How Task Executors Work." Zellij/tmux capabilities are
their standard documented features.*

# TaskYou vs everything else

**TaskYou is an autonomy-first task orchestrator for coding agents.** You drop a task on a
board, an agent picks it up and works in its own isolated git worktree in the background, and
the card tracks all the way to a merged PR. One agent per task. A worktree per agent.

That's a different job from a terminal multiplexer, a process dashboard, or a worktree-diff
tool. This page lays the whole landscape on one grid, then links to a detailed page for each
contender. We try to be fair: where a tool genuinely beats us at *its* job, we say so.

> **The short version:** most of these tools help you *watch and drive* agents you launch by
> hand. TaskYou helps you *queue outcomes and walk away.* If your mental model is "panes I'm
> babysitting," you want a multiplexer. If it's "a backlog that clears itself," you want TaskYou.

---

## Capability matrix

| Capability | **TaskYou** | tmux / Zellij | cmux / Warp | Conductor / Emdash / Superset |
|---|:--:|:--:|:--:|:--:|
| Unit of work is a **task / outcome** (not a pane) | ✅ | ❌ | ❌ | ✅ |
| **Queue + background execution** (stack work, walk away) | ✅ | ❌ | ❌ | ◐ |
| **Kanban board** (Backlog / In Progress / Blocked / Done) | ✅ | ❌ | ❌ | ◐ |
| **Worktree-per-task isolation**, automatic | ✅ | ❌ | ◐ | ✅ |
| **PR-aware completion** (task state follows the PR to merge) | ✅ | ❌ | ❌ | ◐ |
| **Pluggable executors** (Claude, Codex, Gemini, Pi, OpenClaw, OpenCode) | ✅ | n/a | ❌ | ◐ |
| **Routines** — scheduled unattended runs that feed the queue | ✅ | ❌ | ❌ | ❌ |
| **Live executor panes** to watch/drive an agent | ✅ | ✅ | ✅ | ◐ |
| Runs over **SSH** (board from anywhere) | ✅ | ✅ | ❌ | ❌ |
| **Web UI** (same board in a browser) | ✅ | ❌ | ❌ | ◐ |
| **Desktop app** (macOS / Linux) | ✅ | ❌ | ✅ | ✅ |
| **Email control** (create + unblock tasks from your phone) | ✅ | ❌ | ❌ | ❌ |
| **Fully scriptable CLI / agent API** | ✅ | ◐ | ◐ | ◐ |
| Open source | ✅ MIT | ✅ | ◐ | ◐ |

✅ first-class · ◐ partial / possible-but-not-the-point · ❌ no · n/a not applicable
*(`◐` and `❌` for other tools reflect their stated positioning as of mid-2026; correct us with a PR if a tool has shipped past this.)*

---

## Pick your comparison

- **[TaskYou vs tmux / Zellij](taskyou-vs-tmux.md)** — TaskYou *runs on* tmux; here's what it adds on top.
- **[TaskYou vs Conductor / Emdash / Superset](taskyou-vs-conductor-emdash-superset.md)** — the other worktree-orchestrator camp.
- **[TaskYou vs cmux / Warp](taskyou-vs-cmux-warp.md)** — desktop agent apps vs a board that runs anywhere.

---

## One-liners

- **vs tmux / Zellij:** tmux multiplexes terminals; TaskYou multiplexes *tasks* — and it's built on tmux, so you get the panes anyway.
- **vs Conductor / Emdash / Superset:** they isolate a worktree per branch and help you review the diff; TaskYou does that *and* runs the work in the background on a queue, across six executors, on whatever surface you're holding.
- **vs cmux / Warp:** they're polished Mac apps for agents; TaskYou is the same board in your terminal, your browser, your desktop, your inbox, and over SSH.

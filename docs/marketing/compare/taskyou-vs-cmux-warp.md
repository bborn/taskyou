# TaskYou vs cmux / Warp

**One line:** cmux and Warp are polished apps for running agents on your machine; TaskYou is the
same job as a board that runs *anywhere* — terminal, browser, desktop, email, and over SSH —
and is built to queue work and walk away.

cmux (a Mac terminal app for agents) and Warp (an agentic development platform / terminal) both
bet on a beautiful, local, GUI-forward experience. TaskYou bets on **autonomy and reach**: file
an outcome, let an agent run it in the background in an isolated worktree, and pick up the result
from whatever device you have.

---

## What cmux / Warp are good at

- **Polished, native UX.** Warp in particular is a gorgeous terminal with first-class agent
  features; cmux is a focused Mac app for driving agents. If you want a beautiful local cockpit,
  this is the camp.
- **Low friction on your own machine.** Install the app, open it, go. No daemon mental model, no
  SSH server to think about.
- **Rich interactive features** baked into the app — completions, blocks, agent panes, and so on.

If you primarily work on one Mac and want the most polished local agent experience, these are
strong, and TaskYou doesn't try to out-pretty them.

## Where TaskYou is different

| | **TaskYou** | cmux / Warp |
|---|---|---|
| Center of gravity | A **task board** + queue | A polished terminal/app you drive |
| Queue + background execution | ✅ stack work, walk away | ❌ you drive sessions live |
| Worktree-per-task isolation | ✅ automatic | ◐ varies |
| PR-aware completion | ✅ | ❌ |
| Pluggable executors (6) | ✅ per task | ◐ tied to the app's agent(s) |
| Routines (scheduled unattended runs) | ✅ | ❌ |
| Runs on a **headless server / over SSH** | ✅ `taskd` | ❌ desktop-bound (cmux); ◐ (Warp) |
| Same board on **web + email + Chrome** | ✅ | ❌ |
| Open source | ✅ MIT | ◐ |

Two differences matter most:

1. **Autonomy.** cmux and Warp are places you *drive* agents. TaskYou is a place you *hand off*
   work: queue a column, walk away, come back to diffs. The board, the worktrees, and the
   PR-aware lifecycle exist so you don't have to sit in the loop.

2. **Reach beyond the desktop.** A desktop app lives on the desktop. TaskYou runs the same board
   in your **terminal**, your **browser** (`ty serve`), a **desktop app**, over **SSH** (`taskd`,
   so it can live on a remote box or a cloud VM), via **email** (create and unblock tasks from
   your phone with the `ty-email` extension), and through a **Chrome extension** for
   annotate-and-fix loops. You can kick off work from a VM you SSH into and review it from your
   phone.

---

## When to pick which

**Pick cmux / Warp when:**

- You want the most polished **local, GUI** agent experience and you live on one machine.
- You're driving agents hands-on and don't need a queue, a scheduler, or remote surfaces.
- Native app niceties (blocks, completions, design) are what you're optimizing for.

**Pick TaskYou when:**

- You want to **queue outcomes and walk away**, not drive every session by hand.
- You want **worktree isolation + PR-aware tracking** as defaults.
- You need to run agents on a **remote/cloud box** and triage them from **any device** —
  including your **phone**, by email.
- You want **multiple executors** and **scheduled routines** on one open-source (MIT) board.

**They can coexist:** TaskYou can run on a server and own the queue and worktrees while you use a
slick local terminal (Warp, cmux, or anything else) as the place you drop in to take the wheel on
a single task.

---

*Accuracy note: TaskYou capabilities are documented in the [README](../../../README.md) and the
`extensions/ty-*` READMEs. cmux/Warp entries reflect their general positioning as desktop/terminal
agent apps; `◐` marks places their scope varies or we haven't individually verified a feature.
This is a category comparison, not a feature audit. PRs welcome to correct anything.*

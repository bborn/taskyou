# TaskYou vs herdr — competitive parity

> **Status:** maintained baseline. The `herdr-watch` routine reads this file to
> decide whether a herdr capability or plugin is worth borrowing. If we already
> HAVE or are AHEAD on something, it should not be re-proposed. Keep this
> accurate to the current code — when a verdict changes, edit the row, don't add
> a new doc.
>
> **Last refreshed:** 2026-06-23 (against `main` at the time of writing).

## How to refresh

Re-enumerate the plugin ecosystem before re-scoring:

```bash
gh api "search/repositories?q=topic:herdr-plugin&per_page=50" \
  --jq '.total_count, (.items[] | "\(.full_name) :: \(.description)")'
```

At last count this returns **24 repos** under the `herdr-plugin` topic. herdr
itself is documented at <https://herdr.dev>. When the count or descriptions
change, update the [marketplace table](#3-the-herdr-plugin-marketplace-repo-by-repo)
below.

## Verdict legend

| Verdict | Meaning |
|---|---|
| **HAVE** | We have a roughly equivalent capability. No action. |
| **AHEAD** | We do this *and* more; herdr is the one that's behind. |
| **BEHIND** | herdr does this better, or we lack it. Candidate for the borrow backlog. |
| **BORROW** | Worth lifting the *idea* (not the code — herdr plugins are Lua/TS against herdr's socket; we'd reimplement in Go/the TUI). On the backlog. |
| **N-A** | Multiplexer-layer concern that doesn't apply to an autonomy engine. |

## The one-paragraph framing

herdr is a **multiplexer** — tmux that understands agent state, with panes,
workspaces, mouse-first navigation, pane-history replay, and a CLI/socket plugin
API. TaskYou is an **autonomy/workflow engine** — a task queue with a Kanban
board, background headless execution in isolated git worktrees, six pluggable
executors, an MCP server, event hooks, and PR-aware completion. They overlap
only at the seam where TaskYou *renders* a running agent (the TUI detail view's
live executor pane, the executor dock, `ty serve`'s browser terminal). Most of
herdr's surface is **N-A** to us because it's solving the canvas problem; most
of TaskYou's surface is invisible to herdr because it's solving the orchestration
problem. herdr's own compare page says as much — it explicitly disclaims task
queues, Kanban, background execution, and web dashboards.

---

## 1. Core TaskYou vs herdr core capabilities

| herdr capability | TaskYou verdict | Rationale |
|---|---|---|
| Panes / tabs / splits | **HAVE** (different mechanism) | The TUI detail view runs live executor + shell in split panes; the executor dock join/break-panes a real tmux pane (~20ms). We compose panes via tmux under the hood rather than being the multiplexer. |
| Workspaces | **AHEAD** | herdr's "workspace" ≈ a worktree you opened manually. TaskYou creates a **worktree per task** automatically, tracked in SQLite, with a write-guard so an executor can't escape its worktree. Lifecycle is managed, not manual. |
| Mouse-first navigation | **BEHIND** | TaskYou is keyboard-first by design (Bubble Tea). Mouse support is partial. Not a priority — see Strategic take. |
| Persistent sessions (survive detach) | **HAVE** | The `taskd` daemon owns executor tmux windows; the TUI/desktop/web attach non-destructively and the agent keeps running after you detach. Self-heals stale/lost panes. |
| Agent-state visualization (blocked/working/done/idle) | **AHEAD** | This *is* the Kanban board: Backlog / In Progress / Blocked / Done columns, live via SSE. herdr shows per-pane status badges; we model state as first-class queue state with transitions, PR gating, and notifications. |
| Pane-history replay | **HAVE** | The execution log persists per-task output; the detail view replays it. We log to the task record rather than scrubbing a raw PTY buffer. |
| Run anywhere you can SSH | **HAVE** | `cmd/taskd` is an SSH server (Wish); `ty` attaches to a remote daemon. taskyou-os productizes this onto an exe.dev VM. |
| CLI / socket plugin API | **HAVE** (different shape) | 100% of TaskYou is scriptable via the `ty` CLI — every TUI action has a command, so scripts and agents drive the whole queue. Our extension seam is the `ty serve` HTTP API + MCP server, not a Lua/socket pane API. |
| GitHub-topic plugin marketplace | **HAVE** (smaller) | We ship first-party extensions in `extensions/` (ty-chrome, ty-email, ty-qmd, ty-web) + the `desktop/` GUI, rather than a community topic. No open marketplace yet. |
| — | | **TaskYou-only capabilities herdr has no answer for, below.** |
| Task queue + Kanban | **AHEAD** | herdr disclaims this outright. SQLite-backed tasks, columns, scheduling, retry, dependencies. |
| Background headless execution | **AHEAD** | herdr runs agents in real PTYs you watch; TaskYou runs them unattended via the daemon and `ty run` routines (default `sonnet`, 30m timeout, dangerous permission mode since headless can't answer prompts). |
| Six pluggable executors | **AHEAD** | claude / codex / gemini / pi / opencode / openclaw, behind one `Executor` interface, with per-executor command builders, hooks, and write-guards. herdr is agent-agnostic but doesn't abstract them. |
| Permission modes | **AHEAD** | Four modes — `default`, `accept-edits`, `auto`, `dangerous` — cycled per task, mapping to executor flags (e.g. `--dangerously-skip-permissions`). Safe-resume demotes `dangerous`→`default`. |
| Worktree-per-task + write-guard | **AHEAD** | Each task gets an isolated worktree; a pre-tool write-guard hook (codex/gemini/opencode) blocks writes outside it. No herdr equivalent. |
| MCP server + project-context cache | **AHEAD** | `internal/mcp` exposes taskyou tools to executors; `taskyou_get/set_project_context` caches codebase structure so repeat tasks skip re-exploration. |
| Event hooks | **AHEAD** | Executor settings inject hooks (e.g. `idle_prompt|permission_prompt` → mark task `blocked`); `event_log` is the SSE change feed. |
| PR-aware completion | **AHEAD** | `taskyou_complete` opens/links a PR; the task sits in `blocked` and is auto-promoted to `done` when the PR merges/closes (GitHub integration polls PR state). |
| Web / SSE | **HAVE/AHEAD** | `internal/web` serves the Kanban board + a browser terminal over websocket; `event_log` drives SSE live updates. |
| Tauri desktop GUI | **AHEAD** | `desktop/` (Tauri 2 + xterm.js + portable-pty) gives the same UI natively on macOS/Linux, attaching non-destructively to daemon windows. |

---

## 2. taskyou-os vs herdr distribution model

taskyou-os (`github.com/taskyou/taskyou-os`) is the productized remote-server
distribution of the engine. This is where TaskYou competes most directly with
herdr's "run anywhere you can SSH" pitch — and goes further.

| Dimension | herdr | taskyou-os | Verdict |
|---|---|---|---|
| Install | Binary + clone plugins from GitHub topics | Claude Code plugin marketplace: `/plugin marketplace add taskyou/taskyou-os` → `/plugin install taskyou-os` → `/taskyou-os:launch` wizard | **AHEAD** — one-command, provisions exe.dev VM + SSH keys + daemon. |
| Orchestration model | You drive panes; agents can self-orchestrate via socket | A **General Manager** (GM) session in Claude Code delegates tasks to background agents on the remote VM; `/gm-babysit`, `/gm-status`, `/gm-fix` | **AHEAD** — manager/worker delegation is built in, not a plugin. |
| Remote execution | Your server, your SSH | One-click provisioned exe.dev VM; runs with laptop closed | **HAVE** (productized). |
| Web access | None (terminal-only by design) | Web landing page + live Kanban + one-click browser terminal for the GM | **AHEAD**. |
| Notifications | Community ntfy/push plugins (`herdr-ntfy-notify`, `herdr-push`, `herdr-remote`) | `notifications.jsonl` + a task-event channel (push, not polling); Slack `@mentions` for revisions | **HAVE** (first-party). |
| Integrations | Per-plugin | Linear (agent↔human handoff), Slack (events + mentions), Cloudflare R2 (public asset URLs), GitHub (push work) — built in | **AHEAD**. |
| Health / self-update | n/a | `/taskyou-os:doctor` health checks + auto-update | **AHEAD**. |

**Net:** on the distribution axis, taskyou-os is AHEAD of herdr on everything
except being a multiplexer. herdr deliberately isn't a hosted product; taskyou-os
deliberately is.

---

## 3. ty-extensions vs the closest herdr plugin

The `extensions/` dir plus the `desktop/` GUI are our plugin layer. Each maps to
a herdr plugin (or cluster of them):

| TaskYou extension | What it does | Closest herdr plugin(s) | Verdict |
|---|---|---|---|
| **ty-chrome** | Point-and-click annotate any page served by a task's dev server; delivers selectors + DOM + screenshot into the executor pane; side panel "teleports" the executor into Chrome. Tab→task matching via per-task port (`$WORKTREE_PORT`, 3100–4099). | (none — herdr has link handlers, not a browser-annotate→agent loop) | **AHEAD** — no herdr plugin closes a visual-feedback→code-edit loop. |
| **ty-email** | Email in/out interface: email creates tasks, reply provides input, status updates come back. Gmail/IMAP poll → Claude classifies intent → `ty` CLI. | `herdr-ntfy-notify`, `herdr-remote` (notify/approve from afar) | **AHEAD** — two-way email *control*, not just notifications. |
| **ty-qmd** | Semantic search over task history + docs during execution. Proxies QMD (BM25 + vector + LLM rerank) through the taskyou MCP server; completed tasks become searchable knowledge. | (none) | **AHEAD** — no herdr equivalent for agent memory/retrieval. |
| **ty-web** | Browser Kanban board talking to the `ty serve` HTTP API. | (none — herdr disclaims web dashboards) | **AHEAD** by definition. |
| **ty-gui** (`desktop/`) | Tauri 2 desktop app (xterm.js + portable-pty); same Kanban UI natively, non-destructive attach to daemon windows. | (none — herdr is terminal-only) | **AHEAD** by definition. |

The general shape: **herdr's plugins extend the canvas; our extensions extend
the engine's reach** (into the browser, the inbox, a knowledge index, the
desktop). They aren't competing for the same slot.

---

## 4. The herdr-plugin marketplace, repo-by-repo

24 repos under `topic:herdr-plugin` (refresh with the query above). Scored
against the closest TaskYou capability:

| herdr plugin | What it does | TaskYou verdict | Rationale |
|---|---|---|---|
| `smarzban/herdr-file-viewer` | Git-aware read-only TUI file viewer: tree + content pane, diffs, rendered markdown, syntax highlighting | **BORROW** | We have no in-TUI file/diff browser in the detail view. **Backlog item #1.** |
| `0x5c0f/herdr-insight` | Agent State Timeline Panel | **BORROW** | We have `event_log` but no timeline visualization of it. **Backlog item #2.** |
| `madarco/agentbox` | Run multiple agents in parallel in sandboxed VMs | **HAVE/AHEAD** | Worktree-per-task isolation + write-guard + taskyou-os VM cover this; ours is queue-managed. |
| `cloudmanic/herdr-plus` | "Projects" + Quick Actions | **HAVE** | We have projects (per-task project field, project-context cache) and CLI/keybinding quick actions. |
| `dutifuldev/ghzinga` | Clickable TUI to view a single GitHub issue/PR | **BEHIND (minor)** | We surface PR *status*; no rich in-TUI issue/PR reader. Low priority — overlaps file-viewer backlog. |
| `NathanFlurry/herdr-plugin-jj-workspace` | Create/remove jj workspaces as herdr workspaces | **HAVE** | We do worktree-per-task automatically (git, not jj). jj support is a non-goal. |
| `devskale/herdr-flist` | File list plugin | **BORROW (folds into #1)** | Subsumed by the git-aware file viewer backlog item. |
| `ogulcancelik/herdr-plugin-github-start` | Start Codex/Claude from a GitHub issue/PR/discussion | **HAVE** | `taskyou_create_task` + CLI create a task (any executor) from arbitrary input; GitHub-issue-to-task is a thin wrapper. |
| `zom-2018/herdr-ntfy-notify` | Real-time ntfy push notifications | **HAVE** | taskyou-os notifications.jsonl + task-event channel; ty-email for two-way. |
| `razajamil/herdr-plugin-workspace-manager` | Declarative tab/pane layouts per workspace, applied on worktree creation | **N-A** | Pane-layout concern — multiplexer layer, not ours. |
| `third774/herdr-last-workspace` | Swap to last-focused workspace | **N-A** | Navigation/UX of the multiplexer. |
| `ppggff/herdr-plugin` | Keep macOS input sources stable per pane | **N-A** | Pure terminal-pane concern. |
| `paulbkim-dev/vim-herdr-navigation` | Ctrl+h/j/k/l navigation across panes + vim splits | **N-A** | Multiplexer navigation. |
| `wyattjoh/herdr-plugin-gh-pr` | Show focused pane's branch PR status in sidebar | **HAVE** | `internal/github` shows PR status per task; PR-aware completion goes further. |
| `carsonjones/herdr-plugin-tiles` | Simple pane manager | **N-A** | Multiplexer layer. |
| `kkckkc/herdr-plugin-gh-workflow` | GitHub workflow plugin | **HAVE (partial)** | We poll PR/CI state for completion; full Actions browsing is not a goal. |
| `shizlie/herdr-setup-bootstrap` | Bootstrap new worktrees from `worktree_init.toml` | **HAVE** | Worktree setup is built into task creation; per-project config exists (`project_config.go`). |
| `alon-z/herdr-command-palette` | Fuzzy workspace/directory command palette | **HAVE** | TUI has fuzzy search; CLI covers the rest. |
| `rmarganti/herdr-pluck` | Copy pattern-matched strings from panes | **N-A** | Terminal-buffer utility. |
| `alon-z/herdr-devup` | Per-project dev layouts + tunnel-URL env sync | **HAVE (partial)** | Per-task port + `$WORKTREE_PORT` (ty-chrome) cover the dev-server/tunnel need; pane layouts are N-A. |
| `dcolinmorgan/herdr-push` | Zero-dep event push to herdr-remote for mobile monitoring + one-tap approval | **HAVE** | taskyou-os push notifications + Slack `@mention` revisions + ty-email approval. |
| `Matovidlo/herdr-pr-tracker` | Track the GitHub PR each session produces, gh state + actions | **AHEAD** | This is literally our PR-aware completion, built into the queue, not a tracker plugin. |
| `andrewchng/herdr-sessionizer` | Fuzzy pickers to open projects/worktrees into workspaces | **HAVE** | Project/task pickers in the TUI; worktrees are created, not hand-opened. |
| `dcolinmorgan/herdr-remote` | Monitor/approve agents from phone, menu bar, Telegram (no SSH) | **HAVE (partial)** | taskyou-os web terminal + notifications + ty-email cover phone control; no menu-bar/Telegram surface yet (minor BORROW candidate). |

**Tally:** 2 BORROW (the two deferred items + a couple that fold into them),
the rest HAVE / AHEAD / N-A. The marketplace is overwhelmingly multiplexer-layer
plugins that don't apply to an engine.

---

## 5. Borrow backlog (prioritized, S/M/L)

Only two items survive scoring as genuinely worth building. Both were
**deferred from the first build batch** and are the reason this doc exists.

### P1 — Git-aware file & diff viewer in the TUI detail view  · **M** · borrow `herdr-file-viewer`
A keyboard-driven file tree + content pane inside the task detail view, showing
the worktree's working-tree diff with syntax highlighting and rendered markdown.
Today you leave the TUI (or open the GUI) to see what an executor changed.
- **Why M, not S:** the detail view is render-cached by `viewSignature` and pane
  setup is async (`setupPanesAsync`); a new viewport must route through
  `setViewportContent` or the cache goes stale. Adding a third pane interacts
  with the executor-dock join/break-pane logic.
- **Folds in:** `herdr-flist`, `ghzinga`'s file angle.

### P2 — Agent activity timeline panel from `event_log`  · **S–M** · borrow `herdr-insight`
A timeline visualization of a task's lifecycle built from the existing
`event_log` (the SSE change feed): created → worktree → executor start → hook
fires (idle/permission/blocked) → PR opened → merged. The data already exists
and is already streamed; this is a rendering job.
- **Why S–M:** no new data plumbing — `event_log` + `recordEvent` already emit
  rows. Risk is in the TUI render cache (`hashTaskCard` / `viewSignature` must
  account for new fields) and a clean timeline layout in Lipgloss.

### Watchlist (do not build yet, re-evaluate on refresh)
- **Menu-bar / Telegram control surface** (borrow from `herdr-remote`) — **L**.
  Only if taskyou-os users ask for phone control beyond the web terminal + email.
- **Rich in-TUI GitHub issue/PR reader** (`ghzinga`, `herdr-plugin-gh-workflow`)
  — **M**. Low value while PR-aware completion already surfaces what we gate on.

Everything else under the topic is **N-A** (multiplexer-layer) or already
**HAVE/AHEAD** — the `herdr-watch` routine should not re-propose them.

---

## 6. Strategic take

**Why we do not drop TaskYou for herdr.** It's a canvas-vs-engine distinction.
herdr is the multiplexer — the surface that *renders* and *navigates* running
agents. TaskYou is the autonomy/workflow engine — the thing that *decides what
runs, isolates it, and tracks it to a merged PR*. herdr's own compare page
disclaims exactly the layer we live in: task queues, Kanban, background
execution, web dashboards. Adopting herdr would mean throwing away the queue,
the worktree isolation + write-guard, the six-executor abstraction, the MCP
server, the hooks, PR-aware completion, and taskyou-os — and getting a nicer
pane manager in return. The trade is lopsided. The two things herdr genuinely
does better (a git-aware file viewer, an agent timeline) are small, additive,
and already on the backlog above.

**The open question worth holding.** The one place the two architectures could
*compose* rather than compete: today TaskYou's executors render through tmux
(daemon windows, the executor dock's join/break-pane). In principle the executor
layer could target a **herdr-like pane backend** instead — herdr (or its socket
API) becomes the canvas, while TaskYou stays the brain: the queue, the worktrees,
the routing, the completion logic. That would let us keep our orchestration and
borrow herdr's mouse-first, replayable, well-factored pane surface for free,
rather than reimplementing it. It's not on the roadmap — the tmux backend works
and the executor command-builder already lives in two places that must stay in
sync — but it's the right shape to revisit if herdr's pane model pulls
meaningfully ahead of what we render today.

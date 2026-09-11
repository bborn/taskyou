# TaskYou vs. Symphony — Competitive Teardown

A side-by-side comparison of **TaskYou (`ty`)** and **[Symphony](https://github.com/anantjain-xyz/symphony-rust)**
(`anantjain-xyz/symphony-rust`), focused on two questions from the brief:

1. What should we copy or borrow?
2. Is anything about the way their GUI is built better than ours — or worth improving against?

> Scope note: Symphony is closed-ish in the sense that we compared against its public README and
> documented architecture, not a deep source audit. Claims about Symphony below are sourced from its
> README/docs. Claims about TaskYou are grounded in this repo.

---

## TL;DR

Symphony and TaskYou are **the same product idea built from opposite ends**:

- **Symphony** is a *Linear-native, GUI-first orchestrator*. Its reason to exist is "watch a Linear board,
  dispatch agents to issues." The desktop dashboard is the product.
- **TaskYou** is a *local-first, TUI-first task engine*. Its reason to exist is "one engine, three
  interfaces (TUI/CLI/GUI), fully scriptable and agent-native." The board is ours, not borrowed from a SaaS.

Neither is strictly "ahead." They've each invested in things the other hasn't. The highest-value things to
borrow from Symphony are **operational robustness features for unattended runs** — automatic
exponential-backoff retry with error context, AI-provider rate-limit awareness, and a first-class
prompt-template editor — not GUI architecture (our GUI stack is essentially identical to theirs, and our
TUI-first story is a genuine differentiator we should not dilute).

---

## What the two products actually are

| | **TaskYou (`ty`)** | **Symphony** |
|---|---|---|
| One-liner | Personal task engine: SQLite tasks, git-worktree isolation, Kanban, pluggable AI agents | "Autonomous engineering team for your Linear project" |
| Issue source | **Local** Kanban board (+ email/chrome/web extensions, MCP) | **Linear** board (polled via GraphQL) |
| Primary interface | **TUI** (terminal) — first-class | **Desktop GUI** (React dashboard) |
| Other interfaces | CLI (100% scriptable), Desktop GUI, browser (`ty serve`), SSH, MCP | Desktop GUI only |
| Isolation | git **worktrees** (cheap, shared object store) | fresh **clones** per issue |
| Agents | Claude Code, Codex, Gemini, Pi, OpenClaw, OpenCode | Codex, Claude Code |
| Storage | SQLite (`modernc.org/sqlite`, CGO-free) | SQLite |
| Secrets | env / CLI auth of the underlying agent | **macOS keychain** |
| Language | Go | Rust |

The conceptual overlap is striking: poll/queue → isolate workspace → render a prompt template → dispatch
an agent → track events in SQLite → surface on a board. We arrived at the same architecture independently.

---

## GUI architecture: are they "better"?

**Short answer: no — the stacks are nearly identical.** This is the most important finding for the GUI
half of the brief, because it's easy to assume a polished-looking competitor is built on something better.

| Layer | TaskYou desktop (`desktop/`) | Symphony |
|---|---|---|
| Shell | **Tauri 2** | **Tauri** |
| UI framework | **React 19 + TypeScript** | React + TypeScript |
| Styling / components | Tailwind 4 + radix-ui (shadcn-style) | (React dashboard; shadcn-style) |
| Live updates | SSE (`src/api/sse.ts`) | SSE-style live stream to dashboard |
| Terminal | **xterm.js** (real PTY in desktop, live mirror in browser) | n/a (no embedded terminal documented) |
| Backend bridge | Go engine via local HTTP/SSE + Tauri commands | Rust crates via Tauri commands |

So on the desktop axis we are not behind on technology. Where they differ is **product posture**:

- **Symphony's GUI is the product.** It's the only way to use it, so every capability is exposed there.
- **TaskYou's GUI is one of three faces of the same engine.** Our differentiator is that *the TUI ships
  first* and *the CLI is 100% scriptable* (every TUI action has a command; agents can drive the whole
  queue, read executor output, and send keystrokes). Symphony has no scriptable surface and no terminal
  interface at all.

### Where their GUI genuinely does more than ours (today)

These are dashboard *features*, not architecture, and they're worth matching in our GUI **and** TUI:

1. **Retry queue as a first-class view.** Symphony shows the retry queue (what failed, what's scheduled to
   re-run, with backoff). We have a `RetryModel` but it's a manual, one-off "press `r`, type feedback"
   flow — there's no queue view of pending/auto-retrying work.
2. **Provider rate-limit panel.** Symphony surfaces AI-provider rate-limit signals and token counts in the
   dashboard. We track *GitHub* API rate limits (`internal/github/pr.go`) but nothing about the **agent
   provider's** limits or token usage.
3. **Prompt-template editor with a placeholder reference panel.** Symphony has an in-app editor listing
   available placeholders (`{{issue.identifier}}`, `{{repo.name}}`, …) with a live reference. Our prompts
   are assembled in Go (`internal/executor`) and aren't user-editable from the GUI/TUI.

None of these require a GUI rearchitecture on our side — they're features that would slot into the existing
React + SSE + Go-engine model (and most should also appear in the TUI to stay true to TUI-first).

---

## What we should borrow from Symphony

Ranked by value-to-effort. All of these are **engine** features that would benefit the TUI, CLI, and GUI
at once — consistent with our "one engine, three interfaces" model.

### 1. Automatic retry with exponential backoff + error context (highest value)

Symphony retries failed runs automatically with exponential backoff and **appends a "Retry context"
section containing the prior error** to the prompt on the next attempt.

TaskYou today: retry is **manual**. A human opens the blocked task, presses `r`, and types feedback
(`internal/ui/retry.go`, `GetRetryFeedback`). There is no automatic re-dispatch and no automatic capture of
the failure into the next prompt.

Why borrow it: TaskYou is explicitly built for *unattended* background execution (SSH, daemon, cron-driven
agents). Manual-only retry undercuts that. Auto-retry-with-context is the single biggest robustness gap.

Concrete shape for us:
- Add `retry_count`, `max_retries`, `next_retry_at` to the tasks table.
- On `task.failed`, if `retry_count < max_retries`, schedule a re-dispatch with `next_retry_at = now +
  backoff(retry_count)` instead of going straight to `blocked`.
- Auto-prepend the captured failure (we already capture executor output) as a "Previous attempt failed
  with:" section, reusing the existing `retryFeedback` plumbing in `internal/executor/executor.go`.
- Make `max_retries`/backoff configurable per task and globally; expose the retry queue in the TUI board
  and GUI.

### 2. AI-provider rate-limit + token-usage awareness

Symphony records token counts and provider rate-limit signals per run and shows them. We track none of
this for the agent providers.

Why borrow it: unattended fleets hit provider limits. Knowing "Claude is rate-limited, back off" lets the
scheduler pause/space dispatches instead of burning retries. Token counts also give users a cost signal.

Concrete shape: parse rate-limit / usage signals from executor stderr/stdout (Claude Code and Codex both
emit these), store per-run, and (a) feed them into retry backoff, (b) display a small usage/limit indicator
in the board header. This pairs naturally with #1.

### 3. First-class, editable prompt templates with a placeholder reference

Symphony renders prompts from user-editable templates and documents placeholders (`{{issue.*}}`,
`{{repo.*}}`). Ours are hardcoded in Go.

Why borrow it: lets users tune agent behavior per project without recompiling, and makes the
prompt-assembly logic legible. We already have rich task/project/worktree context to expose as placeholders.

Concrete shape: a template file per project (e.g. `~/.config/task/prompts/<project>.tmpl`) with a
documented placeholder set, an editor surface in GUI/TUI, and a reference panel. Keep the current built-in
as the default template.

### 4. Repository-routing rules (lower priority — different model)

Symphony routes issues to repos via an explicit precedence: `repo:` label → Linear project claim → team
key → default, and "an explicit label is never silently rerouted." TaskYou tasks already carry a project
and worktree, so we don't need issue→repo routing the same way — but the **principle** ("explicit user
intent is never silently overridden") is a good design rule for any future auto-routing/auto-assignment we
add (e.g., picking an executor or project automatically). Borrow the *principle*, not the feature.

### 5. macOS keychain for secrets (situational)

Symphony stores the Linear API key in the macOS keychain, never on disk. TaskYou mostly delegates auth to
the underlying agent CLI, so we have less secret material to hold — but as we add integrations (email
extension creds, future tracker tokens), keychain-backed storage on macOS (with a file fallback elsewhere)
is the right pattern to adopt rather than dotfiles.

---

## What we do that Symphony doesn't (don't regress on these)

Worth stating explicitly so the comparison doesn't read one-directionally — these are our moats:

- **TUI-first.** A full Kanban + live executor panes + fuzzy search in the terminal. Symphony has no
  terminal interface.
- **100% scriptable CLI + MCP / agent-native.** Every action has a command; agents drive the queue, read
  output, and send keystrokes. Symphony has no scripting surface. This is our strongest differentiator and
  we should keep widening the gap, not narrowing it.
- **Not coupled to a SaaS issue tracker.** Local-first board works offline and for personal/non-Linear
  work. Symphony requires a Linear workspace + API key to do anything.
- **More executors** (6 vs 2) and **worktrees instead of full clones** (faster, less disk).
- **Remote access via SSH** and a **browser UI** from the same engine.
- **Project-context caching** (agents cache codebase exploration across tasks) — a token-efficiency feature
  Symphony's docs don't mention.
- **Extensions ecosystem** (`extensions/ty-chrome`, `ty-email`, `ty-web`, `ty-qmd`) — multiple task intake
  channels beyond a single tracker.

---

## Recommended follow-ups

If we act on this teardown, the suggested order (each is a standalone task):

1. **Auto-retry with backoff + error context** — biggest robustness win for unattended runs. (engine + TUI/GUI)
2. **Provider rate-limit / token tracking** — pairs with #1; informs backoff and shows cost. (engine + GUI/TUI header)
3. **Editable prompt templates + placeholder reference** — user-tunable prompts. (engine + GUI/TUI editor)
4. **Retry-queue view** — surface auto-retrying/scheduled work as a first-class list. (TUI + GUI)
5. **Keychain-backed secret storage on macOS** — adopt as we add integration credentials.

Explicitly *not* recommended: re-platforming the desktop GUI. Our stack (Tauri 2 + React 19 + Tailwind +
radix + xterm.js + SSE) already matches Symphony's, and our TUI-first / scriptable posture is the thing that
makes TaskYou distinct.

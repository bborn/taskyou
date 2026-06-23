# Positioning memo: sharpening TaskYou + a "vs X" comparison section

**Status:** draft for review · **Audience:** Bruno + whoever owns taskyou.dev
**Companion drafts:** [`compare/`](compare/) — "TaskYou vs X" pages

---

## TL;DR

The sharpest tools in the coding-agent space are winning the *positioning* fight, not the
*product* fight. The best-positioned competitors have a tighter one-liner, a clear **category
claim**, and a clean `/compare` page that frames every rival on their own terms. TaskYou's
surface ("Kanban for code agents") describes the **UI metaphor** instead of the **job**, and we
have no comparison content at all — so anyone evaluating the space reads someone else's framing
and never hears ours.

The fix is cheap and doesn't require a single product change:

1. **Lead with the job, not the widget.** TaskYou's job is *autonomy* — you describe an
   outcome, an agent executes it in the background, and the board tracks it to a merged PR. Say
   that first.
2. **Claim a category.** TaskYou is an **"autonomy-first task orchestrator for coding agents."**
   That's an honestly different category from a terminal multiplexer, a process dashboard, or a
   worktree-diff tool.
3. **Ship a `/compare` section.** Draft pages are in [`compare/`](compare/). They lead with what
   TaskYou *is* and lay the landscape on one capability grid.

Every claim in these drafts is checked against current code/features in the README and the
shipped extensions. No vaporware. Anything aspirational is called out as such.

---

## 1. Where TaskYou's public marketing lives

| Surface | Location | Canonical? |
|---|---|---|
| Landing page (taskyou.dev) | [`docs/index.html`](../index.html) in **this repo**, published via `docs/CNAME` → `taskyou.dev` | **Yes** — this is the live marketing site. Single page, no `/compare`, no `/docs` route. |
| Product docs | [`README.md`](../../README.md) (long-form, 970+ lines) | Yes — doubles as the docs site (`taskyou.dev` "Docs" link points at the README). |
| Extension docs | `extensions/ty-*/README.md` (email, chrome, web, qmd) | Yes, per-extension. |
| Install scripts | `docs/install.sh`, `docs/install-macos.sh` | Yes. |

**Note on the public-site repo.** The live site is the hand-written `docs/index.html` here,
deployed straight from `docs/` (Cloudflare Pages style, given `docs/_headers` + `docs/CNAME`).
There is no separate site-content repo in this checkout. **These drafts are authored as portable
Markdown under `docs/marketing/`, with rendered HTML pages under `docs/compare/`** so they deploy
to taskyou.dev as a `/compare` section in the same style as the landing page. Flagging so the
final home is a deliberate choice.

---

## 2. Teardown: what the best-positioned competitors do well

The strongest competitive positioning in this space shares a few traits worth stealing:

- **A category claim, in one sentence.** The best tools tell you exactly what they are and what
  mental slot they occupy before you finish reading — often by borrowing a familiar tool's
  credibility ("X for agents"). TaskYou makes you infer the job from a UI metaphor.
- **The analogy does the selling.** A familiar anchor instantly tells the reader *why they'd
  care*. Our "Kanban for code agents" borrows Kanban's familiarity but not its **urgency** —
  Kanban is a board you look at; the strongest analogies name a tool you *live in*.
- **"Agent state at a glance."** The sharpest idea floating around the category: surface
  *blocked / working / done / idle* as first-class, scannable state. It reframes "a wall of
  terminal panes" into "a dashboard of agent status." TaskYou already has the raw material for
  this (Kanban columns + the green-dot activity indicator) and should say so explicitly.
- **A `/compare` page is a moat.** The tools that publish one — a capability matrix, a
  positioning paragraph per rival, a one-line verdict for each — define the **axes** of
  comparison. Whoever defines the axes wins. Right now we're absent from that conversation, so
  the axes get set without us.
- **Mouse-first / low-friction onboarding.** Lowering the learning curve widens the audience.

**The uncomfortable part:** if a buyer reads a competitor's `/compare` and then looks at TaskYou,
they'll slot us into whatever bucket that page already dismissed. We need to be in the
conversation on our **own** axes — autonomy, lifecycle, and distribution — not theirs.

## 3. Where we win — honestly

A lot of tools in this space are **viewers and control layers**: they make already-running agents
*persistent and visible*, but you still drive every agent yourself, pane by pane. That's the
ceiling of that category.

TaskYou is an **orchestrator**: the unit of work is a *task*, not a pane. You describe an outcome
and walk away. That difference cashes out in features the viewer category structurally doesn't
have:

| Axis | TaskYou | Multiplexer / viewer tools |
|---|---|---|
| **Unit of work** | A task (an outcome you want) | A pane (an agent you're watching) |
| **Queue + background execution** | Stack a column, walk away, come back to diffs | You attach and drive each agent live |
| **Worktree-per-task** | Automatic, default substrate — every task isolated | Sometimes integrated; rarely the core metaphor |
| **PR-aware completion** | Task → `blocked` on PR open, auto-promoted to `done` when the PR merges/closes | No PR lifecycle |
| **Pluggable executors** | Claude Code, Codex, Gemini, Pi, OpenClaw, OpenCode — per task | Attaches to whatever agent you launched |
| **Routines** | Named unattended runs (scouts/monitors) that feed the queue on a schedule | — |
| **Distribution** | TUI **+ web UI + desktop app + email + SSH + Chrome extension** | Usually one surface |

Two of these are worth leading with:

- **Autonomy.** Queue, background execution, worktree-per-task, and PR-aware completion add up to
  a loop the viewer category can't run: *task in → agent works unattended → PR out → board
  reflects merge.* Those tools keep the human in the driver's seat of every pane. We let you
  leave.
- **Distribution / reach.** The common reach story is "SSH from any device that has a terminal."
  Ours is genuinely multi-surface: the same board in a **terminal**, a **browser** (`ty serve`,
  localhost:8484), a **desktop app** (macOS/Linux), over **SSH** (`taskd`), via **email** (create
  tasks + reply to unblock from your phone, no terminal required), and a **Chrome extension** for
  annotate-and-fix loops. You can triage your agents from a phone on the couch. A terminal-only
  tool can't.

**Be honest about what we don't lead on:** if what someone wants is a *live, mouse-first grid of
interactive agent panes they drive by hand*, a purpose-built multiplexer is better at exactly
that. TaskYou runs on tmux too and has live executor panes in the detail view — but our center of
gravity is the **board and the queue**, not the pane grid. The compare pages say this out loud;
pretending otherwise would make the whole `/compare` section read as spin.

## 4. Sharpen the positioning / tagline

Current hero (`docs/index.html`):

> **Kanban for code agents.** Calm, cool, a little crazy. An agent for every task. A worktree for every agent.

Keep the voice — "calm, cool, a little crazy" and the mug-in-sunglasses mascot are real brand
equity and we should not sand them off. The problem is only the **top line**: it names the widget,
not the job.

**Recommended changes (smallest-diff-first):**

1. **Add a category line above or beside the hero** — the thing we're missing:
   > **An autonomy-first task orchestrator for coding agents.**
   This is the sentence that wins the "what *is* this" race. "Kanban for code agents" can stay as
   the friendly restatement right under it.

2. **Promote the job-story line.** The site already contains the perfect line as a section header
   further down: **"Drop a task. Walk away. Merge the diff."** That's a job in six words. Pull it
   up near the hero.

**Net hero proposal (illustrative — final copy is a separate task):**

> ### An autonomy-first task orchestrator for coding agents.
> **Drop a task. Walk away. Merge the diff.**
> An agent for every task. A worktree for every agent.
> *Calm, cool, a little crazy.*

This keeps 100% of the existing personality and adds the one thing the page is missing: a claim
about *what TaskYou is for*.

---

## 5. Recommended next steps

1. **Approve / edit the `/compare` drafts** in [`compare/`](compare/) (vs tmux/Zellij, vs
   Conductor/Emdash/Superset, vs cmux/Warp) + the [`compare/index.md`](compare/index.md)
   capability matrix.
2. **Decide the publish target** for `/compare` (static HTML on taskyou.dev — already drafted
   under `docs/compare/` — vs a public-site repo) — see §1.
3. **Land the hero tweak** (§4) — a ~10-line change to `docs/index.html`, separate PR.
4. **Pick the canonical comparison axes** (capability matrix rows). Our matrix centers them on
   autonomy, lifecycle, and distribution. Worth a deliberate sign-off because these become the
   terms of every future comparison.

*Every capability asserted in these drafts maps to a shipped feature documented in the README or
an `extensions/ty-*` README. Where something is partial or executor-specific (e.g. session
resumption is Claude/Pi/OpenClaw only), the drafts say so.*

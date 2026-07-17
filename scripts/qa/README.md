# ty TUI QA harness

Drive the **real** ty TUI programmatically against a **throwaway, isolated** instance,
so we can QA real features (board, detail view, forms, executor panes) without
touching the live daemon, DB, or tasks.

It's a simple loop: **up → seed → key → assert state → down**.

## Content standard: QA data must look real

Every screenshot this harness produces is a potential **marketing and
documentation asset**, so treat QA content as production copy. The board and
detail views we render must look like a real team's real board — never `test 1`,
`foo`, `asdf`, or lorem ipsum. Concretely:

- **Seed with `scripts/qa/ty-qa-seed.sh`.** It populates a curated set of
  believable tasks (real-sounding titles, a sentence or two of body, sensible
  tags, a spread across every column, a couple pinned) across a few realistic
  projects. Edit that file like copy that could ship on the website: evergreen,
  no secrets, no real customer data, no dated references, no throwaway strings.
- **Shoot with the seeded DB kept** (`TY_QA_SHOT_KEEP_DB=1`). The harness then
  **freezes the daemon automatically** (see `ty-qa-freeze.sh`) so `queued` tasks
  sit still in the In Progress column instead of being picked up, executed, and
  demoted mid-shot.
- **Frame it well.** Use dimensions wide enough that no column is clipped
  (`TY_QA_SHOT_W` / `TY_QA_SHOT_H`), and prefer a tight crop over acres of empty
  space.

New QA screens and flows should hold to this bar, not just the two examples below.

## Why

Lots of features can only really be verified by driving the actual TUI — pane
join/break, status transitions, forms, keybindings, detail rendering. This harness
makes that scriptable and repeatable instead of a manual one-off.

## Isolation

Everything is namespaced off two env vars (set automatically by `lib.sh`):

| | live instance | this harness |
|---|---|---|
| DB (`WORKTREE_DB_PATH`) | `~/.local/share/task/tasks.db` | `/tmp/ty-qa/tasks.db` |
| tmux (`WORKTREE_SESSION_ID`) | pid-based | `task-{ui,daemon}-qa` |
| projects (`projects_dir`) | `~/Projects` | `/tmp/ty-qa/projects` |

Override location/id with `TY_QA_ROOT` and `TY_QA_SID`.

## Quickstart

```bash
scripts/qa/ty-qa-up.sh                 # build binary, fresh DB, register 'qa' project
scripts/qa/ty-qa-seed.sh               # populate realistic tasks across a few projects
scripts/qa/ty-qa-tui.sh                # launch the real TUI in tmux session task-ui-qa
scripts/qa/ty-qa-key.sh n              # drive it: open the new-task form
scripts/qa/ty-qa-state.sh              # assert: view == "new_task", etc.
scripts/qa/ty-qa-capture.sh            # or eyeball the rendered screen
scripts/qa/ty-qa-down.sh               # stop (add --purge to delete the DB)
```

Seeding is optional for pure state assertions, but **required for anything you
screenshot** — see the content standard above. When you drive the TUI by hand
against seeded data, run `scripts/qa/ty-qa-freeze.sh` first so the auto-started
daemon doesn't execute your `queued` tasks (`--off` to undo).

To watch live while scripting: `tmux attach -t task-ui-qa`.

## Asserting state

The TUI runs with `--debug-state-file`, dumping JSON on every update.
`ty-qa-state.sh` prints a summary, or pass a `jq` filter:

```bash
scripts/qa/ty-qa-state.sh '.view'                 # "dashboard" | "detail" | "new_task" | ...
scripts/qa/ty-qa-state.sh '.detail.has_panes'     # true once panes are joined
scripts/qa/ty-qa-state.sh '.dashboard.selected_task_id'
```

## Keybindings (most useful for scripting)

Board: `P`/`B`/`L`/`D` focus In-Progress/Backlog/Blocked/Done · `Up`/`Down` select ·
`Enter` open detail · `n` new · `e` edit · `x` execute · `X` execute dangerous ·
`!` toggle dangerous/safe · `S` change status · `/` filter · `?` help.

Detail: `Enter` (from board) opens it and fires the real `joinTmuxPane` · `!` toggles
mode (fires the resume path → `agentSendTarget` "continue working") · `\` toggle shell
pane · `Esc` close.

## Three tiers of test

1. **No agent (default).** Most UI features — navigation, forms, filters, status
   changes, rendering — need only the TUI over the isolated DB. Just `up` + `tui` + drive.

2. **Live panes without the daemon** — `ty-qa-agent.sh <task-id>`. Stands up a real
   agent window for a task and points its DB row at it, so opening the task in the TUI
   exercises the real `joinTmuxPane` / nudge / shell-pane code. Use this for pane and
   executor-interaction QA. (Needed because ty's daemon lock is global — you can't run
   a second daemon next to the live one.)

3. **Full daemon** — only if no other ty daemon is running (stop the live one first),
   then `WORKTREE_DB_PATH=… WORKTREE_SESSION_ID=qa "$TY_BIN" daemon`. Real end-to-end
   executor spawning. Heaviest; depends on Claude auth/trust.

## Pipeline stress test — slow init + concurrency

```
scripts/qa/ty-qa-pipeline-stress.sh                      # PASS/FAIL, exits non-zero on a zombie
scripts/qa/ty-qa-pipeline-stress.sh --binary /tmp/ty-old # reproduce against an older build
scripts/qa/ty-qa-pipeline-stress.sh --pipelines 5 --init-delay 45
```

Run this after touching **anything** in the workflow completion path —
`WorkflowStepFinished`, the `reconcileFinishedWorkflowSteps` sweep, the Stop hook, or
worktree setup.

It stands up an isolated instance whose project has a `bin/worktree-setup` that sleeps
(default 30s, longer than the sweep's 16s tick), then launches several pipelines at once.
Those two conditions — **slow worktree init** and **concurrency** — are what every real
project has and what a throwaway git repo run one-at-a-time does not.

It asserts one timing-robust invariant:

> **A step cannot be completed before its session started.**

Any `done` step whose `completed_at` precedes its session-start log — or that has no
session at all — was never run, which means the DAG advanced past work that never
happened. That is reported as a `ZOMBIE` and fails the run.

### Why this exists

A sweep meant to rescue finished-but-unsignalled steps was marking steps `done` ~10s in,
before their agent ever started: a freshly-created worktree is clean and its HEAD is
already on origin, and during init there is no tmux window either — so "hasn't started"
was indistinguishable from "finished". On a 3-pipeline run it silently deleted every
Build and review stage; each Verify then found nothing upstream, built the slice alone,
and shipped an unreviewed PR.

Two lessons are baked into this script:

- **Never infer completion from absence** (no window, no diff against origin). Only from
  positive evidence of work — a commit the step actually made.
- **Harness fidelity beats harness convenience.** The bug was invisible to a fast, serial
  QA project and unmissable in a slow, concurrent one. If a test can't be made to fail on
  the broken code, it isn't testing anything.

## Worked example — the pane-routing regression

Reproduces "executor stops working when the detail view is open": with the detail
view open, the agent pane is joined into the UI session and a nudge sent to the
window's pane `.0` lands in the shell instead of the agent.

```bash
scripts/qa/ty-qa-up.sh
"$TY_BIN" create "pane routing" -p qa
scripts/qa/ty-qa-agent.sh 1                 # live agent for task 1
scripts/qa/ty-qa-tui.sh
scripts/qa/ty-qa-key.sh P Enter             # open detail -> real joinTmuxPane
scripts/qa/ty-qa-state.sh '.detail.has_panes'   # => true (panes joined)
# the agent pane is now in task-ui-qa; sending to the persisted claude_pane_id reaches
# the agent, while the old window-relative target (task-daemon-qa:task-1.0) does not.
scripts/qa/ty-qa-down.sh --purge
```

## Profiling render performance

To find rendering bottlenecks, run the harness with profiling enabled. The TUI
accepts `--cpuprofile` / `--memprofile`; `ty-qa-profile.sh` launches the isolated
instance with both, drives a render-heavy stress sequence, quits gracefully so
the profiles flush, and prints the hottest render call stacks.

```bash
scripts/qa/ty-qa-up.sh                                  # build + isolated instance
"$TY_BIN" create "perf" -p qa                           # seed a few tasks
scripts/qa/ty-qa-profile.sh                             # capture + summarize

# Inspect interactively:
go tool pprof "$TY_BIN" /tmp/ty-qa/cpu.prof             # then: top / list KanbanBoard.View / web
go tool pprof "$TY_BIN" /tmp/ty-qa/mem.prof             # heap allocations
```

The board render is cached by a signature of its inputs (see `KanbanBoard.View`),
so idle re-renders (ticks, mouse motion, unrelated events) are nearly free; the
profile's render time comes from cache-miss frames (navigation, task changes).

The **detail view** (Enter on a card) is tuned the same way:

- **Opening is instant.** All tmux work — the window search plus the ~30-call
  join/split/resize that happens when an executor is already running — runs off
  the Bubble Tea update thread (`setupPanesAsync`), so the view paints
  immediately with a loading spinner and the panes drop in when ready, instead
  of freezing the UI for the whole join.
- **`DetailModel.View()` is render-cached** by a signature of its inputs (same
  trick as the board): idle frames skip the expensive `viewport.View()` +
  bordered `box.Render()` (~2ms / ~2.7MB) and cost only the cheap header/help
  render used to detect changes. See `viewSignature` and `detail_cache_test.go`.
- The shell-process indicator is polled on a throttle in `Refresh()` instead of
  shelling out to tmux from `renderHeader()` on every frame.

For reproducible micro-measurements (and CI regression guarding), the Go
benchmarks are the fastest loop:

```bash
go test ./internal/ui/ -run '^$' -bench 'BenchmarkKanbanView' -benchmem
go test ./internal/ui/ -run '^$' -bench 'BenchmarkDetail'     -benchmem
# add -cpuprofile=/tmp/c.prof to capture render call stacks from a benchmark
```

## Screenshots & PR evidence (VHS + R2)

To attach real-TUI screenshots to a PR, render with **VHS** and publish to the
public R2 evidence bucket — no manual uploads, no friction. These shots double as
marketing/docs assets, so seed realistic data first (see the content standard at
the top).

**Board / detail shots (with seeded data — the common case):**

```bash
scripts/qa/ty-qa-up.sh                                  # build + isolated instance
scripts/qa/ty-qa-seed.sh                                # realistic tasks across a few projects
mkdir -p "$TY_QA_ROOT/shots"

# TY_QA_SHOT_KEEP_DB=1 keeps the seeded DB AND auto-freezes the daemon so queued
# tasks don't execute mid-shot. Widen so no column is clipped. Extra args = VHS tape.
TY_QA_SHOT_KEEP_DB=1 TY_QA_SHOT_W=1500 TY_QA_SHOT_H=760 \
  scripts/qa/ty-qa-shoot.sh "$TY_QA_PROJECTS/storefront" "$TY_QA_ROOT/shots/board.png" "Sleep 3s"

# Open a task's detail: focus In Progress (P), Enter, let it settle.
TY_QA_SHOT_KEEP_DB=1 TY_QA_SHOT_W=1300 TY_QA_SHOT_H=720 \
  scripts/qa/ty-qa-shoot.sh "$TY_QA_PROJECTS/storefront" "$TY_QA_ROOT/shots/detail.png" \
  "Sleep 2s" 'Type "P"' "Sleep 500ms" "Enter" "Sleep 3s"
```

**First-run shots (fresh DB, no data):** omit `TY_QA_SHOT_KEEP_DB` — each shot
starts from an empty DB so first-run detection fires. Extra args are VHS tape lines.

```bash
scripts/qa/ty-qa-shoot.sh /tmp/ty-qa/plainfolder  /tmp/ty-qa/shots/welcome.png "Sleep 5s"   # welcome fork
scripts/qa/ty-qa-shoot.sh /tmp/ty-qa/plainfolder  /tmp/ty-qa/shots/picker.png \
  "Sleep 5s" "Enter" "Sleep 1s" 'Type "ty"' "Sleep 2s"                                       # fork -> picker -> filter
```

**Publish** (prefix is usually the PR number):

```bash
scripts/qa/ty-qa-publish.sh 555 "$TY_QA_ROOT"/shots/*.png
# -> ![board](https://pub-...r2.dev/taskyou-qa/<date>/555-board.png)  ...
```

Then paste the printed markdown into a PR comment (or `gh pr comment <n> -F -`).

**Before/after a UI change:** build the old binary from the base branch into a
second path and point `TY_BIN` at it for the "before" shot, reusing the same
seeded DB, e.g. `TY_BIN=/tmp/ty-before scripts/qa/ty-qa-shoot.sh …`.

**Why VHS, not `tmux capture-pane`:** a detached tmux session mis-reports its
width to bubbletea, so centred modals overflow and render corrupted. VHS runs
the TUI in a correctly-sized headless terminal — screenshots match real users.

**Tooling / config:** needs `vhs` and `imagemagick` (`brew install vhs imagemagick`),
and a configured `rclone` remote. `ty-qa-publish.sh` writes to
`r2-personal:qa-evidence/taskyou-qa/<date>/` and prints public
`pub-….r2.dev` URLs. The write remote is **`r2-personal`** (the read-only `r2`
remote returns 403 on PutObject); override via `TY_QA_R2_REMOTE`/`TY_QA_R2_BUCKET`/
`TY_QA_R2_PUBLIC`. No credentials live in the scripts — they're in the rclone remote.

## Gotchas

- **Launching the TUI always ensures a daemon**, which immediately executes any
  `queued` tasks. For static/seeded screenshots that churn ruins the shot, so
  `ty-qa-shoot.sh` freezes the daemon when `TY_QA_SHOT_KEEP_DB=1`; if you drive
  the TUI by hand, run `ty-qa-freeze.sh` yourself. `ty-qa-down.sh` clears it.
- The TUI must run **inside** `task-ui-<sid>` — `joinTmuxPane` attaches agent panes there.
- An agent's `pane_current_command` shows as the Claude **version string** (e.g. `2.1.162`), not `claude`.
- Claude's folder-trust prompt needs one `Enter` unless `~/.claude.json` already trusts the worktree (`ty-qa-agent.sh` sends it).
- Requires `tmux`, `go`, `python3`; `jq` and `sqlite3` for state filters / the agent helper.

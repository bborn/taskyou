# ty TUI QA harness

Drive the **real** ty TUI programmatically against a **throwaway, isolated** instance,
so we can QA real features (board, detail view, forms, executor panes) without
touching the live daemon, DB, or tasks.

It's a simple loop: **up → key → assert state → down**.

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
scripts/qa/ty-qa-tui.sh                # launch the real TUI in tmux session task-ui-qa
"$TY_BIN" create "hello" -p qa         # (or: scripts/qa/ty-qa-up.sh prints the binary path)
scripts/qa/ty-qa-key.sh n              # drive it: open the new-task form
scripts/qa/ty-qa-state.sh              # assert: view == "new_task", etc.
scripts/qa/ty-qa-capture.sh            # or eyeball the rendered screen
scripts/qa/ty-qa-down.sh               # stop (add --purge to delete the DB)
```

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

For reproducible micro-measurements (and CI regression guarding), the Go
benchmarks are the fastest loop:

```bash
go test ./internal/ui/ -run '^$' -bench 'BenchmarkKanbanView' -benchmem
# add -cpuprofile=/tmp/c.prof to capture render call stacks from a benchmark
```

## Screenshots & PR evidence (VHS + R2)

To attach real-TUI screenshots to a PR, render with **VHS** and publish to the
public R2 evidence bucket — no manual uploads, no friction.

```bash
scripts/qa/ty-qa-up.sh                                  # build + isolated instance

# Render screens (VHS sizes the terminal correctly; ty renders in-pane via TMUX env).
# A fresh DB per shot => true first-run. Extra args are VHS tape lines.
mkdir -p /tmp/ty-qa/shots
scripts/qa/ty-qa-shoot.sh "$TY_QA_PROJECTS/demo"  /tmp/ty-qa/shots/01-card.png    "Sleep 9s"   # git-repo card (waits for claude -p)
scripts/qa/ty-qa-shoot.sh /tmp/ty-qa/plainfolder  /tmp/ty-qa/shots/02-welcome.png "Sleep 5s"   # welcome fork
scripts/qa/ty-qa-shoot.sh /tmp/ty-qa/plainfolder  /tmp/ty-qa/shots/03-picker.png \
  "Sleep 5s" "Enter" "Sleep 1s" 'Type "ty"' "Sleep 2s"                                          # fork -> picker -> filter

# Upload + get the markdown image block (prefix is usually the PR number).
scripts/qa/ty-qa-publish.sh 555 /tmp/ty-qa/shots/*.png
# -> ![01-card](https://pub-...r2.dev/taskyou-qa/<date>/555-01-card.png)  ...
```

Then paste the printed markdown into a PR comment (or `gh pr comment <n> -F -`).

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

- The TUI must run **inside** `task-ui-<sid>` — `joinTmuxPane` attaches agent panes there.
- An agent's `pane_current_command` shows as the Claude **version string** (e.g. `2.1.162`), not `claude`.
- Claude's folder-trust prompt needs one `Enter` unless `~/.claude.json` already trusts the worktree (`ty-qa-agent.sh` sends it).
- Requires `tmux`, `go`, `python3`; `jq` and `sqlite3` for state filters / the agent helper.

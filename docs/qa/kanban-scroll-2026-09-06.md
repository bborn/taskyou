# Kanban scrolling stress test — September 6, 2026

The user prioritized fluid TUI scrolling. The previous 60-task benchmark alternated
between two visible cards and did not exercise growing columns or viewport scrolling.

## Changes

- Render signatures cover the visible cards, column metadata, and scroll position.
  Scrolling no longer hashes every task and its summary on every keypress.
- Pinned-layout calculation stops once pins fill the viewport, avoiding a scan of
  an arbitrarily long pinned prefix on navigation.
- The TUI explicitly requests all active tasks; `Limit: 0` had silently loaded 100.
  The shared listing API retains its zero/default behavior and uses negative limits
  for explicit unlimited queries.
- Local and SSH Kanban renderers use Bubble Tea's supported 120 fps ceiling to
  reduce output scheduling latency. Unchanged frames remain cached.
- Added viewport/cache and full-active-list regression coverage and a scrolling
  benchmark at 100 / 1,000 / 10,000 tasks.
- Added `scripts/qa/ty-qa-scroll.py`, which measures highlighted task IDs and verifies
  final selection, rather than treating unrelated screen changes as successful input.

## Render benchmark

Apple M4 Pro, 160×50 terminal, one backlog column, descriptive titles and ~1 KiB
summaries, navigation repeatedly crossing viewport boundaries:

| Tasks | Before | After |
|---|---:|---:|
| 100 | 0.480 ms | 0.286 ms |
| 1,000 | 2.590 ms | 0.289 ms |
| 10,000 | 23.115 ms | 0.286 ms |

These timings include navigation and View, not terminal I/O. The new viewport
signature reduces 10,000-task frame computation by about 80×.

## Actual TUI stress test

Private QA tmux server, 160×50, 10,000 backlog tasks plus 100 each processing,
blocked, and done. Each task has a ~1 KiB summary and body. The executor was frozen
using a persistent sleep in the private tmux server. A separate SQLite writer
appended approximately 50 synthetic log rows per second.

Both comparison binaries loaded all 10,000 backlog tasks and used the new database
indexes. The before binary retained the full-board render hash; the after binary
used viewport hashing. No concurrent builds/tests ran during these main latency
measurements.

At **30 keys/second**, 300 Down followed by 300 Up:

| Direction | Before median / p95 / worst | After median / p95 / worst |
|---|---:|---:|
| Down | 68.13 / 190.39 / 222.65 ms | 21.91 / 29.60 / 33.86 ms |
| Up | 100.88 / 203.98 / 238.69 ms | 15.55 / 29.93 / 33.83 ms |

All 600 selected positions were observed in both runs, and both final task IDs
matched exactly. This after measurement still used the 60 fps renderer ceiling.

After raising the renderer ceiling to 120 fps, at **60 keys/second**:

| Direction | Median | p95 | Worst |
|---|---:|---:|---:|
| Down | 15.39 ms | 22.69 ms | 57.53 ms |
| Up | 13.71 ms | 20.92 ms | 26.44 ms |

Both final IDs matched, demonstrating that input was not lost. The sampler observed
589 of 600 intermediate selections at this higher rate; skipped observed frames
are not equivalent to dropped inputs. Largest observed frame gaps were 58.41 ms
Down and 34.22 ms Up. The isolated outlier remains visible in this report.

A separate sequence of **200 rapid reversals** (Down, Up, Right, Left repeated),
waiting for the exact expected highlighted ID each time, measured **13.74 ms median,
19.80 ms p95, and 23.81 ms maximum**. This reversal run had no background writer.

Timings include key injection, tmux IPC, frame scheduling, and pane capture. They
measure keyboard scrolling and column navigation, not physical iTerm rendering or
mouse-wheel behavior. Raw measurements and synthetic fixtures remain under
`/private/tmp/ty-qa-scroll/` on the audit machine.

## Remaining performance work

The broader audit is still active: asynchronous detail refresh/cleanup and
separating daemon maintenance from queue dispatch remain to implement and verify.
Board/palette search and duplicate desktop board snapshots were addressed in the
follow-up below. The scrolling
results above do not claim those unrelated paths are complete.


## Search and desktop refresh follow-up

Board filtering and command-palette keyword searches now run as Bubble Tea
commands. Each has at most one search in flight and retains only the latest query.
Searches read copies of task values; stale results cannot repaint a newer query.
Palette Enter waits for the current search, and old palette instances cannot
publish results into a reopened palette. Keyword matching, project aliases,
workflow filters, and historical-task database supplementation are preserved.

A 10,000-task search setup benchmark measures **0.366 ms**, 5 allocations. This
is input-loop setup only: it excludes database search, scoring, and rendering.
Those operations execute in the background. One actual QA search smoke test found
board task #1234 in **36.9 ms** and palette task #9999 in **33.82 ms**, measured
from completed text injection to captured result. These are individual checks,
not latency distributions.

A fresh 120 Down / 120 Up scroll run at 60 keys/sec with the synthetic log writer
measured **13.82 / 12.55 ms median**, **20.68 / 19.30 ms p95**, and **22.80 / 21.04
ms worst**. Both final IDs were correct; 238 of 240 intermediate selections were
observed. Raw results: `/private/tmp/ty-qa-scroll/async-search.json` and
`search-smoke.json`.

The desktop subscribes to a signal-only board stream instead of requesting a
snapshot it discards. The default SSE snapshot contract is retained. Desktop
refreshes coalesce overlapping requests and serialize task/activity updates.
Full task data remains available for body searches and editing.

Validation: UI, DB, parity, server and web Go suites; targeted search race tests;
Node refresh/log tests; desktop typecheck/production build; private seeded TUI
scroll/search checks. Tests use temporary data, and the QA executor stays frozen.

## Detail observation follow-up

Periodic detail task/log reads and memory, listening-port, and shell-process
checks now execute in a coalesced background command. The command snapshots only
the fields its checks need. Results are bound to their originating detail view;
newer task events and log loads take precedence. Pane-title updates also execute
as commands. Tests use a temporary tmux stub to verify neither scheduling nor
result handling runs a subprocess, and cover stale results and overlap prevention.

Scheduling measured **0.0016 ms** with the race detector enabled (pane health
checks suppressed in this microbenchmark). UI/DB/parity/server tests and focused
race tests passed. This does **not** measure or fix pane reattachment/cleanup:
those lifecycle operations still need coordination outside the input loop, along
with the outstanding daemon-maintenance work.

## Pane handoff follow-up

Back and previous/next task transitions now detach the detail model immediately
and clean up its panes in a background command. Registered pane workers finish
before cleanup; a new detail attachment waits for the handoff. Worker results
carry their originating model, so a late result cannot modify the next task.
Back cancels pending task loads. Periodic pane-health checks use private snapshots
and schedule tracked attachment work when recovery is needed.

A temporary tmux stub that fails every command after 30 ms measured **0.066 ms**
for the Back handler and **907.812 ms** for background cleanup. This proves the
command boundary, not terminal rendering latency. Focused race tests cover worker
ordering, immediate board return, cancellation, stale probes and destination
recovery. UI/DB/parity/server suites passed.

Real private-tmux QA used a harmless sleep process in task #10000 over ten
attach/detach cycles. The first check exposed an existing bug: moving the last
pane out of a daemon session removes the session; failed destination creation
then killed the executor. Cleanup now recreates the missing destination. If
recovery still fails, it preserves the pane and returns to its detail view,
preventing a new task from replacing it. The corrected ten-cycle run preserved
the same sleep process throughout and restored the full layout every time.

Measured Escape-to-board **141.085 ms median / 227.65 ms worst**, with full layout
restoration **212.195 / 262.25 ms**. Raw results are in
`/private/tmp/ty-qa-scroll/pane-handoff.json`. This is improved input-loop isolation,
not evidence of an instant terminal transition. Escape timing is 10 ms in the QA
server; a proposed q comparison was invalid because q is not a detail Back key.
The remaining terminal-visible delay needs further profiling. The QA server was
stopped after checks. Other action-specific cleanup paths and daemon maintenance
remain part of the active audit.

## Final bounded pass

At the user's request, finish the current cleanup changes and report the remaining
findings instead of expanding the audit further.

Cleanup now reads pane dimensions once from a consistent window snapshot and
batches fourteen style/binding reset commands into one tmux invocation. A failed
batch retains the previous best-effort fallback. The repeated ten-cycle private
QA test measured:

| Measurement | Before median / worst | After median / worst |
|---|---:|---:|
| Escape to board | 141.08 / 227.65 ms | 69.25 / 190.89 ms |
| Full layout restored | 212.19 / 262.25 ms | 181.42 / 225.57 ms |

All ten cycles preserved the synthetic task process. Raw after results:
`/private/tmp/ty-qa-scroll/pane-handoff-batched.json`. These are small local samples,
not a guarantee of instant transitions or a statistically controlled benchmark.

The first rerun also exposed stale canonical-window handling: duplicate cleanup
would delete all matching live windows if the saved window ID no longer existed.
It now selects a surviving main window before deleting duplicates, persists that
selection, and leaves shell-only remnants alone. Pure regression tests cover stale
and valid IDs, linked windows, unrelated sessions/tasks, and shell-only cases.
The real QA run exercises replacement-window recovery with the stale saved ID.

Validation: UI/DB/parity/server suites; focused executor regression and race test;
pane-layout rounding/missing-pane tests; rebuilt CLI; ten real private pane cycles.

Remaining findings, intentionally left for a later task: action-specific cleanup
paths still contain synchronous work; daemon maintenance still shares the dispatch
loop; some terminal-visible detail transitions still exceed 100 ms. Kanban scroll
measurements and the implemented improvements above remain valid. No claim is
made that every performance opportunity in the app has been exhausted.

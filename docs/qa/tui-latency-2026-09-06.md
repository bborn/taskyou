# TUI latency fix — September 6, 2026

The latest-activity query scanned historical logs for every active task, and the
TUI ran that query and process checks on its input loop. Database write events
could also launch overlapping board reloads.

The fix uses an indexed latest-row lookup, loads activity and prompt snapshots
and running-process indicators in background commands, and coalesces reloads
into one active request plus one pending follow-up. Duplicate-window cleanup
and suspended-task resume now run with asynchronous detail-pane setup.

## QA measurements

The isolated QA harness ran at 160×46 with approximately 2,131 synthetic tasks
and 864,406 historical logs concentrated on 123 active tasks. A separate writer
added approximately 50 log entries per second. The executor was kept frozen by
a persistent sleep process in the private QA tmux server.

Each binary received 80 navigation actions, 12 opens, and 12 closes. Timings
measure sending a key through tmux until the pane changes; open/close also
require the expected detail/board text. They include IPC and frame scheduling,
not physical terminal-app rendering. Navigation detects the first changed frame,
so unrelated refresh frames are a possible source of measurement noise.

| Action | Before median / maximum | After median / maximum |
|---|---:|---:|
| Navigation | 22.04 / 317.88 ms | 21.09 / 39.81 ms |
| Open task | 43.75 / 122.03 ms | 39.10 / 45.29 ms |
| Close detail | 11.29 / 340.31 ms | 19.71 / 27.47 ms |

This deliberately stresses active-task history. It exceeds the approximately
37,000 active-task log entries measured in the user's database during the
initial investigation; it does not establish that every possible source of
local input latency has been removed. These cases did not exercise live executor
pane teardown.

## Automated validation

- Database, UI, and parity package tests passed.
- Race-enabled refresh tests passed.
- Latest-log coverage includes duplicate/missing task IDs, tasks without logs,
  empty requests, and equal timestamps with different log IDs.
- Refresh tests verify that completed snapshots can be applied without a
  database connection, process checks run in commands, pending questions retain
  their display, overlapping loads coalesce, and errors release the load slot.

The local `bin/ty` was rebuilt. Existing TUI processes must be reopened to run
the new binary.

## Startup follow-up

The first patch still gated the loading screen on terminal enrichment. Pending
questions with missing tmux panes incurred a 200 ms retry each, sequentially.
The original navigation measurements had no pending-question logs and started
after a fixed warmup, so they missed this startup case.

Added pending-question logs to 80 blocked tasks in the same isolated synthetic
fixture. Timed tmux session creation through the first captured board containing
both Backlog and In Progress, with the executor frozen. The previous binary took
8,971 ms. The final rebuilt binary took **130.9, 75.9, and 72.1 ms** on three
launches. These are warm local database measurements, including tmux IPC, and
do not measure physical iTerm rendering or a stopped production daemon's startup.

Task data, hook prompt summaries, and activity now arrive before terminal checks.
Terminal enrichment runs separately, with only one command in flight. Optional
pane captures do not retry and share a two-second deadline. Late captures cannot
overwrite a replaced or resolved prompt. Existing retry behavior remains for
callers that need reliable pane capture (such as session handoff).

Regression coverage loads 80 pending questions, verifies the board is usable
before any tmux command, and checks stale and current enrichment results.
Database/UI/parity tests and race-enabled refresh/startup tests passed. The final
binary was rebuilt locally; reopen the TUI to use it.

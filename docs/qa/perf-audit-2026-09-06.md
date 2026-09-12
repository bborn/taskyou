# TaskYou performance audit — September 6, 2026

Audited `db71bf06` after the startup and board responsiveness fixes. No product
code changed. Scope: TUI input/refresh paths, shared SQLite queries, executor
scheduling, desktop data loading/logs, and browser terminal transport.

## Priority and evidence

| Priority | Opportunity | Evidence | Recommended change |
|---|---|---|---|
| 1 | Remove remaining terminal work from TUI input handlers | Injected 30 ms failed tmux calls: prompt event handlers 316–323 ms; detail cleanup 818 ms; focus check 40 ms | Background commands with immutable results; serialize pane transitions without blocking the input loop |
| 1 | Stream browser terminal output promptly | Actual local WebSocket handler: typed input to changed frame 458.52 ms with immediate stub output | Event-driven terminal transport; as an incremental step, coalesced redraw after input without the resize-only delay |
| 2 | Avoid duplicate desktop board refreshes | 1,000-task response 1,314,002 bytes / 25.40 ms; discarded SSE snapshot another 17.35 ms | One useful snapshot or lightweight invalidation, narrow card DTOs, coalesced refreshes |
| 2 | Bound and batch desktop log consumption | 500 appends at 50,000 retained logs: 172.8 ms for array operations alone | Batch events, constant-time deduplication, bounded recent window, paginated history and virtualized rendering |
| 2 | Index task listing; move search off the input loop | 50,000-task board load 222.81 → 6.44 ms with experimental indexes; broad search 194–296 ms | Validate partial/expression indexes, async search with stale-result rejection, narrow search rows |
| 2 | Separate executor maintenance from queue dispatch | Code trace: GitHub/terminal maintenance runs inline in the worker consuming queue wakeups | Separate bounded maintenance workers; retain task-level synchronization |

Priorities reflect user-visible responsiveness first. Numbers from injected
latency and enlarged fixtures are explicitly distinguished from current-scale
measurements below. No claim that these are all occurring in the live session.

## 1. Remaining TUI input-loop stalls

`internal/ui/app.go:1449` captures terminal contents synchronously while handling
an executor status event with a new prompt. `CapturePaneContent` retries failed
captures with a 200 ms sleep. The startup fix moved this work out of board loading,
but the real-time event path remains. With a temporary tmux executable that sleeps
30 ms then fails, repeated calls to the actual event handler took 316.27, 318.09,
and 322.83 ms. First invocation was slower (422–564 ms across runs).

`internal/ui/app.go:2600` calls `DetailModel.Cleanup` directly on Back/Escape.
`internal/ui/detail.go:2634` acquires the pane-operation mutex, gathers dimensions,
restores styles/keybindings, and moves panes. The timeout is established after
locking; dimension helpers also have their own deadlines. Cleanup took 817.75 ms
with the same failed-command stub. This is a failure-path reproduction, not a
measurement of normal live-pane teardown.

`internal/ui/app.go:1503` also invokes focus checks synchronously every 300 ms.
`internal/ui/detail.go:2990` permits a 200 ms wait each time. The injected test
measured 40.07 ms. `DetailModel.Refresh` at line 672 synchronously performs DB
reads, log COUNT, memory/process probes, port checks, and pane-health checks.
The lsof check alone has a 500 ms timeout; memory probes share a two-second budget.
Those deadlines are code-level risks, not observed normal-case timings.

Move these reads into coalesced background commands. Pane cleanup needs a
serialized transition operation and stale-result protection, so rapid Escape /
Enter cannot move an executor into the wrong view. Test with delayed and missing
panes, real private-tmux panes, and rapid task switching. Reuse stable pane IDs.

## 2. Browser terminal echo is limited by polling

`internal/web/terminal.go:126` forwards input via tmux but does not request a fresh
frame. Output is captured every 500 ms. Only resize requests trigger early redraw,
and that path deliberately waits 80 ms. Each changed frame clears and rewrites
the screen.

A temporary database and tmux stub backed the real HTTP/WebSocket handler. After
receiving its initial frame, the client waited 50 ms, typed one byte, and timed
the next changed frame: **458.52 ms**. The stub changed its frame immediately on
send-keys. The delay is the server polling interval, not a slow executor.

This path is the browser fallback in
`desktop/src/components/TerminalPane.tsx:208`; the native Tauri PTY transport is a
different path and was not measured. Prefer output-driven streaming with
backpressure. A smaller change can request a coalesced refresh after input, using
a short separate schedule so it catches asynchronous shell echo without inheriting
the resize delay. Test burst typing and output that arrives after send-keys returns.

## 3. Desktop fetches the board twice

`internal/web/sse.go:130` loads up to 500 full task records to create each SSE board
snapshot. `desktop/src/api/sse.ts:7` uses that snapshot only as a change signal.
`desktop/src/store.ts:138` then requests 1,000 full task records and separately
loads activity. Boot also subscribes after loading everything, so the initial SSE
snapshot schedules another task fetch.

On 2,131 synthetic tasks with 1 KiB bodies, ten direct handler iterations averaged:

- `/api/tasks?all=true&limit=1000`: **25.40 ms**, **1,314,002 bytes**.
- Board SSE snapshot: **17.35 ms**, **5,532 bytes**, discarded by this client.

These exclude network, JSON parsing, and React rendering. The debounce clears its
timer before awaiting the fetch, allowing overlap when requests take longer than
the arrival interval. Results have no request-generation check.

Use a useful shared card snapshot with activity, or a cheap version/invalidation
event plus one card fetch. Keep body/history in detail requests. Share server
snapshots across subscribers, coalesce client refreshes, reject stale responses,
and avoid changing the existing SSE contract for other consumers accidentally.

## 4. Long-lived log views grow in cost

`desktop/src/components/DetailView.tsx:149` subscribes even when logs are collapsed.
Every event scans the retained array with `some` and copies it with spread. There
is no retention cap. `LogList.tsx` maps all rows and formats dates when shown.
`internal/db/tasks.go:1725` returns all rows after `sinceID` without a batch limit.

A Node probe running the exact deduplicate-and-append expression for 500 incoming
logs measured **36.2 ms** with 10,000 retained rows and **172.8 ms** with 50,000.
This excludes React/DOM cost and is a stress microbenchmark, not browser profiling.
The initial subscription can start at zero while detail data is loading; depending
on request timing, it can race the latest-100 response and replay older history.
SSE reconnection also reuses the initial `since` URL rather than a current cursor.

Batch incoming events, track IDs/cursor without rescanning history, retain a bounded
recent window, and fetch older pages explicitly. Virtualize displayed history.
Wait for the initial history cursor before subscribing, and handle reconnection
without replaying already-consumed logs. Keep historical data accessible.

## 5. Task-list indexes and synchronous search

`internal/db/tasks.go:479` selects wide rows and sorts by pinned/status-dependent
time. Current task indexes cover status and project, not this ordering.
`internal/ui/app.go:4283` performs active and done lists each refresh. Search uses
leading-wildcard LIKE plus sorting (`tasks.go:635`) synchronously from both board
filtering (`app.go:2388`) and palette typing (`command_palette.go:259`).

Synthetic task fixtures retained 123 active tasks and grew completed history:

| Operation | 2,131 tasks | 50,000 tasks |
|---|---:|---:|
| Broad matching search, limit 100 | 8.49–11.04 ms | 194.35–296.39 ms |
| No-match search | 1.21–3.13 ms | 33.74–34.58 ms |
| Board database load | 9.08–10.37 ms | 179.70–222.81 ms |

In the same 50,000-row run, two indexes created only in the throwaway database
reduced board load from **222.81 ms to 6.44 ms**:

```sql
CREATE INDEX audit_active_order ON tasks(
  pinned DESC,
  CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC,
  id DESC
) WHERE deleted_at IS NULL AND status NOT IN ('done', 'archived');

CREATE INDEX audit_status_recency ON tasks(
  status,
  CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC,
  id DESC
) WHERE deleted_at IS NULL;
```

Validate write overhead and query plans across real distributions before adding
migrations. Move keyword search into commands with cancellation/stale-result guards;
consider indexed search only while preserving current substring/fuzzy semantics.

The TUI currently says Limit 0 means all active tasks, but `ListTasks` defaults it
to 100. These results preserve that behavior. Fixing the cap is a separate
correctness requirement: speed must not depend on hiding active tasks.

## 6. Queue wakeups wait behind maintenance

`internal/executor/executor.go:1393` processes queue wakeups and scheduled maintenance
in the same select loop. Review reconciliation at line 1506 loops repositories and
calls GitHub synchronously; `internal/github/pr.go:384` permits 15 seconds for each
PR listing. Auth/pane checks and cleanup run in that worker too. A queued task's
wakeup cannot be handled until the current maintenance operation returns.

This finding is established by control flow, not a measured live GitHub delay.
Separate dispatch from bounded maintenance, preserve per-task locking/idempotence,
and test wakeup latency with a deliberately blocked maintenance provider. Do not
replace serialization with unbounded goroutines.

## Healthy paths and limits

Existing Go benchmarks on this Apple M4 Pro:

- Cached board render: **0.016 ms**, zero allocations.
- Board navigation render: **0.412 ms**.
- Cold board render: **0.849 ms**.
- Cached detail view: **0.023 ms**; cold detail view: **2.19 ms**.

These do not justify another broad rendering rewrite. The largest opportunities
are blocking I/O, redundant payloads, and scaling queries. New-form construction
also reads preferences/projects/task references synchronously; it was inspected
but not measured, so it is not ranked as a confirmed bottleneck.

The audit used synthetic temporary databases and stub processes. No live task,
agent session, daemon, or production database was modified. No full desktop browser
CPU/heap profile, remote SSH benchmark, or cold-storage benchmark was run. The
WebSocket probe required loopback networking permission after the sandbox denied
its first bind; the permitted rerun passed. Other probes and existing rendering
benchmarks passed.

## Reproducing the audit

Probe sources are preserved as text so they do not add timing-dependent tests to
normal CI:

- `perf-audit-2026-09-06/ui_probe.go.txt`
- `perf-audit-2026-09-06/web_probe.go.txt`

Temporarily copy them to `internal/ui/perf_audit_probe_test.go` and
`internal/web/perf_audit_probe_test.go`, ensuring those paths are unused first.
Run `go test ./internal/ui ./internal/web -run '^TestPerfAudit' -v -count=1` in an
environment allowing a loopback listener. Delete only those two temporary copies
afterward. All fixture writes use `t.TempDir`; tmux is stubbed, including cleanup.

Render baseline: `go test ./internal/ui -run '^$' -bench 'Benchmark(Kanban|Detail)' -benchmem -benchtime=200ms`.

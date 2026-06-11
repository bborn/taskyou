# QA Report — Routines TUI view (feat/routines)

- **Date:** 2026-06-11 · **Branch:** feat/routines (through 09179653)
- **Harness:** scripts/qa (isolated instance at /tmp/ty-qa, VHS screenshots)
- **Mode:** report-only (/qa-only) — no product fixes applied
- **Seed:** 4 routines covering every health state (ok / failed / disabled / never-run), runs executed through the real runner with a stub `claude`; alert task created by the real failure path.

## What was tested → PASS

| Surface | Evidence | Result |
|---|---|---|
| Board shows pinned `Routine failed:` alert + digest-titled task | qa7-routines-board.png | PASS — alert pinned to top of Backlog, 📌 icon |
| `u` opens Routines view; all 4 states render (enabled/disabled, ok/failed/never-run); columns aligned; colors correct | qa8-routines-view.png | PASS |
| `enter` on failed routine opens log viewer with full run output + `[ty] run failed` trailer; esc returns to table | qa9-routines-log.png | PASS |
| `d` toggles disable; state persists (re-render shows `disabled` dim) | qa10-routines-disable.png | PASS |
| Failure alert dedup (second failed run, no second task) | seeded earlier in session, DB check | PASS |
| Disabled routine: `ty run` exits 0 with notice | CLI run | PASS |
| Isolation: real `~/.config/task/routines` and live DB untouched after QA | `ty routines` post-teardown | PASS |
| Production launchd: migrated plist fired `ty run twitter-monitor` autonomously, recorded `ok · 3m19s` | live run history | PASS |

## Findings (not fixed — report-only)

### ISSUE-001 · HIGH (test infra) — debug state can't see the new view
`internal/ui/debug.go:218` `viewName()` has no `ViewRoutines` case → with the routines view open, `ty-qa-state.sh '.view'` returns `"unknown"`. The harness's whole assert-on-state model is blind to the new view.
**Repro:** `ty-qa-tui.sh && ty-qa-key.sh u && ty-qa-state.sh '.view'` → `"unknown"` (reproduced live this session).

### ISSUE-002 · HIGH (test infra) — every ty-qa script dies on clean start under stock macOS bash
lib.sh's `tmux()` wrapper + the `tmux kill-session -t X 2>/dev/null || true` pattern hits a bash **3.2.57** errexit bug: a failing command inside a function called with a redirect in an `||` list aborts the whole script despite `|| true`. macOS ships bash 3.2 and `#!/usr/bin/env bash` resolves to it on this machine (no newer bash installed), so `ty-qa-tui.sh` (and any sibling that kills a not-yet-existing session) exits 1 before launching anything whenever the QA tmux server isn't already running.
**Minimal repro:** `/bin/bash -c 'set -euo pipefail; f() { command false; }; f 2>/dev/null || true; echo ok'` → prints nothing, exit 1.
**Workaround used this session:** pre-create the session so kill-session succeeds.

### ISSUE-003 · MEDIUM (test infra) — harness doesn't forward routines isolation env
`ty-qa-tui.sh` passes only `WORKTREE_DB_PATH`/`WORKTREE_SESSION_ID` into the tmux command, so a TUI launched by the harness reads the **real** `~/.config/task/routines` while showing the isolated DB's run history — mixed state, misleading assertions. `ty-qa-shoot.sh` had the same gap; forwarding for `TY_ROUTINES_DIR`/`TY_ROUTINES_STATE_DIR` was added there this session (test-infra change, committed separately). `ty-qa-tui.sh` still needs the equivalent.

### ISSUE-004 · LOW (product, cosmetic) — sub-second runs render as "took 0s"
Both the TUI Routines view and `ty routines` CLI show `· 0s` for sub-second runs. Suggest `<1s` rendering. Visible in qa8/qa10.

## Health

Functional 100 · Visual 100 · UX 92 (ISSUE-004) · Test-infra 60 (ISSUE-001/002/003).
**Product surface: ship-ready. The risk is all in the harness's ability to test it, not in the feature.**

## Screenshots

qa7-routines-board.png · qa8-routines-view.png · qa9-routines-log.png · qa10-routines-disable.png (repo root, VHS-rendered, isolated instance)

# Remote execution QA — 2026-09-07

## Automated verification

- `go test -race -p 1 ./...`: all packages pass.
- After the final prompt/session isolation and UI refresh changes:
  `go test -race -p 1 ./internal/executor ./internal/ui`: pass.
- Focused restart replay, per-coordinator session lookup, run-specific prompts
  and event validation checks: pass with race detection.
- `extensions/ty-on`: `go test -race ./...`: pass.
- golangci-lint v2.13.2, root and extension: zero issues.
- Desktop: `npm run build` (TypeScript + Vite): pass. Existing bundle size
  advisory remains. Dependencies were installed from the frozen pnpm lockfile.
- CLI and daemon binaries: `go build -buildvcs=false`: pass. This isolated
  environment could not obtain Go VCS stamping metadata; Git operations work.
- `git diff --check`: pass.

Regression coverage includes duplicate delivery, SQLite reopen/replay, stale
attempt rejection, wrong-host events, move fencing, failed inbox persistence
preventing acknowledgment, POSIX signal/host-agent round trip, failed capture
preserving window existence, failed enumeration remaining unknown, executor
eligibility, SSH destination mapping, remote-required policy, remote Codex flags,
shared placement API and surface parity.

The first broad run exposed a pre-existing shell completion test deadlock: it
filled an unread OS pipe. The test now captures to a temporary file. A one-second
pane recovery test initially timed out under concurrent load; it passed alone
and in both serial race-enabled runs.

## Harness and visual checks

Used `scripts/qa/ty-qa-up.sh`, `ty-qa-seed.sh`, `ty-qa-freeze.sh`, `ty-qa-tui.sh`,
`ty-qa-key.sh`, `ty-qa-state.sh`, `ty-qa-capture.sh` and `ty-qa-shoot.sh` with an
isolated SQLite database and tmux namespace (`remote-execution`). The embedded
web app ran on loopback port 18484 against the same QA database.

- TUI: `@` opens Task placement; debug state reports `placement`. Submitting
  `local` for seeded task #4 shows “Task #4 now runs here.”
- Browser: opened seeded task #1, expanded Change host, submitted local
  placement, and verified the success message and recorded placement.
- Browser health: a synthetic remote placement/health row shows the destination,
  reason, directory, reconnecting state and last observation. This screenshot
  verifies rendering; it is not evidence of a live network outage.
- Screenshots were visually inspected and published with `ty-qa-publish.sh`.

Remote protocol behavior was exercised with real POSIX shells and controlled
SSH/tmux stubs. No production host or paid agent session was used. Native Tauri
packaging and a live remote Codex session were not exercised in this QA run.

## Screenshots


<!-- QA evidence (paste into the PR comment) -->
![placement-tui](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-07/remote-execution-placement-tui.png)
![placement-web](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-07/remote-execution-placement-web.png)
![placement-web-success](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-07/remote-execution-placement-web-success.png)
![placement-web-health](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-07/remote-execution-placement-web-health.png)

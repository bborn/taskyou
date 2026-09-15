# Workspace panels QA — 2026-09-14

## Scope

Shell, pull request summaries, file browsing and Markdown previews share task tabs across the CLI, TUI and GUI. QA used isolated databases and tmux servers with the repository's synthetic storefront fixtures and a fake agent. No live task data was used in the screenshots.

## Verification

| Check | Result |
| --- | --- |
| `go test -p 2 ./...` | Passed |
| `golangci-lint run` (v2.13.2, built with Go 1.26) | 0 issues |
| `pnpm --dir desktop build` | Passed; existing bundle size/import warnings |
| `go build -tags ui -o /tmp/ty-workspace-qa/ty ./cmd/task` | Passed |
| Existing QA pane views suite | 34 passed, 0 failed |
| Existing QA key suite (`TERM=xterm-256color`) | 23 passed, 0 failed |
| Existing QA pane edge suite (`TERM=xterm-256color`) | 32 passed, 0 failed |
| `TY_QA_WORKSPACE_SHOTS=1 scripts/qa/ty-qa-workspace.sh` | Passed |
| GUI in Chromium, 1440×1000 and 390×844 | Launcher, PR, Markdown, deduplication, mobile toggle and keyboard divider checked |

The dedicated harness checks real terminal input, tab switching, PR and Markdown rendering, canonical duplicate opens, shell PID and background job survival, and helper cleanup when leaving task detail. Regression tests cover dropped space keys, stale content responses, resource confinement and bounds, remote file protocol behavior, and pane mirrors preserving size/zoom while forwarding input. A further runtime cross-surface check found and fixed the GUI duplicating a shell parked by the TUI; the API now reuses the owned pane, and newly created shells receive ownership tags. Verified GUI input, close and reopen with the same parked pane ID and process ID, and no browser errors.

The machine's preinstalled linter was built with Go 1.25 and could not check this Go 1.26 repository. The reported lint result uses v2.13.2 built with the repository toolchain.

## Coverage limits

The GUI was exercised through `ty serve`; the native Tauri wrapper was not launched. Remote file protocol tests execute the same bounded Python script locally; no live SSH host was used. Browser embedding and runtime third-party plugins are outside this initial provider set. Terminal mirrors retain task-owned dimensions; the native TUI shell remains available for tmux scrollback.

## Screenshots

Captured from the QA harness fixtures and uploaded with `scripts/qa/ty-qa-publish.sh`.


<!-- QA evidence (paste into the PR comment) -->
![gui-launcher](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-gui-launcher.png)
![gui-pr](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-gui-pr.png)
![gui-markdown](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-gui-markdown.png)
![gui-mobile](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-gui-mobile.png)
![tui-launcher](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-tui-launcher.png)
![tui-pr](https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev/taskyou-qa/2026-09-14/workspace-panel-tui-pr.png)

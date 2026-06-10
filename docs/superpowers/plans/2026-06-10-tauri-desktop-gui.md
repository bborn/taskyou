# TaskYou Desktop — Tauri GUI Port

## Goal

Port the TaskYou TUI into a native desktop GUI using Tauri 2, feature complete and fully
working. Hard requirement: the executor pane must be a **real terminal** (a true terminal
emulator driven by a real PTY), not a polled `capture-pane` mirror like the existing
`/api/tasks/{id}/terminal` WebSocket bridge.

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│ Tauri app (desktop/)                                       │
│ ┌────────────────────┐   IPC/Channels  ┌─────────────────┐ │
│ │ React + TS (webview)│◄──────────────►│ Rust core        │ │
│ │  xterm.js terminals │                │  PTY manager     │ │
│ │  kanban / detail /  │                │  (portable-pty)  │ │
│ │  forms / palette    │                │  sidecar         │ │
│ └─────────┬──────────┘                │  supervisor      │ │
│           │ HTTP + SSE                 └───────┬─────────┘ │
└───────────┼────────────────────────────────────┼───────────┘
            ▼                                    ▼
   ty serve (REST + SSE, extended)        tmux client per terminal
            ▼                             (grouped session attach)
        SQLite  ◄── ty daemon (executor) ── tmux server
                                            task-daemon-N:task-{id}
                                            pane .0 claude / .1 shell
```

- **Data plane**: the existing `internal/web` HTTP API (`ty serve`), extended with the
  endpoints the GUI needs (attachments, settings, executors, autocomplete, session
  bootstrap). All business logic stays in Go.
- **Terminal plane**: the requirement-critical part. The Rust side spawns a PTY
  (portable-pty, the wezterm PTY layer) running a **grouped tmux session** attached to the
  task's daemon window:
  `tmux new-session -s ty-gui-{task}-{nonce} -t {daemon-session} \; select-window -t {window-id}`
  with `destroy-unattached on` and session-scoped `mouse on`. This gives a genuine
  interactive terminal — full escape sequences, mouse, resize — into the same claude+shell
  panes the TUI shows, without moving panes (join-pane stays TUI-only). PTY bytes stream to
  xterm.js (`@xterm/xterm`, VS Code's emulator) over Tauri Channels.
- **Sidecar supervision**: on launch the Rust core health-checks `GET /api/status`; if no
  server is running it locates the `ty` binary (setting → PATH) and spawns
  `ty serve --port {port}`; likewise ensures `ty daemon` runs (respecting its PID file).
  Children it spawned are terminated on app exit; pre-existing processes are left alone.

### Why this approach

- Reusing `ty serve` + SQLite + daemon keeps one source of truth and zero duplicated
  business logic (the alternative — reimplementing DB/executor logic in Rust — was
  rejected as a DRY disaster and a divergence trap, cf. the executor command-builder
  divergence bug).
- Grouped tmux session per terminal view is the only non-destructive way to view a
  specific window of the daemon session from multiple clients; `join-pane` (TUI approach)
  *moves* panes and would fight the TUI. Each GUI terminal gets an independent
  current-window; `destroy-unattached` auto-cleans on disconnect.
- xterm.js is the only production-grade terminal emulator for webviews (VS Code,
  Hyper); paired with a real PTY it satisfies "real terminal" exactly.

## Workstreams

### A. Go API extensions (`internal/web`)
1. `GET/PATCH /api/settings` — settings table passthrough (whitelisted keys).
2. Attachments: `GET /api/tasks/{id}/attachments`, `POST /api/tasks/{id}/attachments`
   (JSON base64), `GET /api/attachments/{id}` (download), `DELETE /api/attachments/{id}`.
3. `GET /api/executors` — registered executors + availability.
4. `POST /api/autocomplete` — ghost-text suggestions (graceful 503 without API key).
5. `GET /api/tasks/{id}/terminal-info` — daemon session, window id, pane ids, liveness —
   what the GUI needs to attach. `POST /api/tasks/{id}/session` — ensure a resumable
   session/window exists (parity with TUI detail view bootstrapping).
6. Tests for every new handler following `server_test.go` patterns.

### B. Tauri shell (`desktop/`)
1. Scaffold Tauri 2 + React + TS + Vite (pnpm).
2. Rust `pty` module: spawn/write/resize/kill, output streamed via Channel; kills tmux
   grouped session on drop; unit tests for registry bookkeeping.
3. Rust `supervisor` module: ty binary discovery, port config, health check, spawn/adopt
   `ty serve` + `ty daemon`, cleanup on exit.

### C. Frontend features (parity inventory from TUI exploration)
- **Board**: 4 columns, cards (id/title/project color/pinned/age/spinner/PR badge/blocked
  deps/needs-input), live updates (SSE board stream), column collapse, keyboard parity
  (arrows, B/P/L/D jumps, x/X, e, c, a, d, t, S, /, f, n, enter, ?), filter bar
  (`#id`, `[project]`, text), command palette, toasts + native notifications.
- **Detail**: header (status, permission badges, executor selector, PR badge, pin),
  markdown body (description + activity summary), dependencies (blocked-by/blocks),
  execution log (live SSE, typed icons), attachments panel, **real terminal pane**
  (xterm.js attach flow with reattach/self-heal, fit-addon resize → PTY → tmux),
  actions: execute/dangerous, retry-with-feedback, close, archive, delete, status change,
  open worktree in editor, open branch/PR in browser.
- **Forms**: new/edit task (project picker, title+ghost text, markdown body, attachments
  drag-drop, type, executor, effort, permission mode, #task-ref autocomplete, PR URL
  paste), project CRUD, task type CRUD, app settings (port, ty path, theme).
- **Dialogs**: retry, status change, confirmations, help overlay.

### D. Integration, QA, polish
- Lifecycle: PTY/grouped-session cleanup, owned-sidecar shutdown.
- Empty/error states: no server, no daemon, no tmux, task without window.
- End-to-end QA on an isolated `WORKTREE_DB_PATH` instance; terminal attach verified
  against a real tmux task window.
- `go vet`, `golangci-lint`, `gofmt` (CI landmines), `tsc --noEmit`, `cargo clippy`,
  full builds.

## Execution order

1. Branch `feat/tauri-desktop-gui`; fix Rust toolchain (rustup, no dotfile edits).
2. Workstream A (pure Go, parallel with rustup install).
3. Scaffold desktop app; Rust PTY + supervisor.
4. Frontend: foundation (API client, SSE, store, theme) → board → detail + terminal →
   forms → palette/settings/notifications.
5. QA end-to-end, lint/test everything, commit, PR.

## Risks

- **WKWebView + xterm.js renderer**: WebGL addon can misbehave in WKWebView; default to
  canvas/DOM renderer, keep WebGL opt-in.
- **tmux grouped-session quirks**: window-size follows latest client (daemon session is
  normally detached, so the GUI wins); zoom/layout state is shared per window across the
  group — acceptable, same semantics as two attached tmux clients.
- **`ty serve` lacks executor loop**: daemon must run for execution; supervisor handles.
- **Session bootstrap from serve process**: `StartResumableSession` path must work outside
  the daemon process (it only shells out to tmux; verify during A.5).

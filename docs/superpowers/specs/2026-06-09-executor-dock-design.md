# Executor Dock — Design

**Date:** 2026-06-09
**Status:** Approved, ready for implementation plan
**Branch:** `feat/executor-dock`

## Motivation

The "quick input" feature (press `tab` on a blocked task to type an inline
reply that gets sent to the executor via `tmux send-keys`) is unreliable and is
being removed entirely. In its place we add a **toggleable executor dock** below
the kanban board: a panel that shows the highlighted task's executor and lets
you drop into it to interact directly — without the heaviness of moving live
tmux panes every time the selection changes.

## Goal

Give the user a persistent, optional view of "what is the executor doing right
now" for the highlighted task, with a cheap default (read-only snapshot) and a
deliberate, fast path to full interactivity (live joined tmux pane) — only paid
when the user explicitly asks for it.

## Interaction Model

- **Toggle key** opens/closes the dock. Off by default; when off, the board
  behaves exactly as it does today (no layout change, no cost). Once opened, the
  dock **stays open until toggled closed**.
- **Dock open, board focused:** the dock shows a **read-only snapshot** of the
  highlighted task's executor pane (`tmux capture-pane`), re-captured as the
  selection moves. This is cheap (~100ms, no pane movement) and is the default
  state.
- **`shift+↓` / `shift+→`:** promote the highlighted task to a **live,
  interactive** joined tmux pane and move focus into it. The user types directly
  to the executor (no inline text box, no `send-keys` translation layer).
- **`shift+↑` / `esc`:** return focus to the board. The live pane goes back to
  its daemon window; the dock reverts to snapshot mode for whatever is
  highlighted.
- **Moving the board selection** always swaps the dock to the newly highlighted
  task's snapshot. At most one live pane exists at any moment — the one the user
  is actively focused into.

## The One Physical Constraint (and how we resolve it)

The dock is a single screen region. It can hold **either** a TUI-rendered
snapshot (just text) **or** one real joined tmux pane — never both. A joined
live pane physically occupies that region as a sibling tmux pane, so it cannot
remain there while the region renders a *different* task's snapshot.

Therefore, when the user is live on task A and then arrows to task B, A's live
pane must vacate the region so B's snapshot can render.

**Resolution that honors "we already paid the price, leave it there":** when a
live pane vacates, it simply returns to **its own daemon window** (a single
`tmux join-pane`, not a teardown). Because the task's window/pane IDs are cached
after the first join and the dock's tmux styling is applied at the session level
(once), **re-entering any task you've already gone live on is the cheap path**
(~sub-100ms join + resize), not the cold cost. The user never re-pays the
expensive cold join for a task they've already visited live.

## Why this is fast (cost model)

Measured against `internal/ui/detail.go`'s existing join machinery:

- `tmux join-pane` itself is a single command — tens of ms.
- The multi-second figure observed for the *detail view* comes from paths that
  **do not apply here**: the `waitingForExecutor` poll loop (only for
  freshly-queued tasks whose executor has not spawned) and session/window
  *creation* sleeps (`detail.go:1127` 100ms, `detail.go:902` 300ms — only on the
  "start a new session" path).
- For an already-running executor (which is exactly the case when we have a
  snapshot to promote): resolve window (cached → ~0; else one `list-windows`
  ~50–100ms), `list-panes` (~50ms), `join-pane` (~30ms), session styling
  (~250ms, **once** when the dock first opens), resize (~30ms). Cold worst case
  ~400–700ms; warm re-entry sub-100ms.

**Snapshot mode never moves panes** — it only reads, so following the selection
around the board stays cheap regardless of how many executors are running.

## Architecture / Components

1. **Dock model** — new, focused unit (e.g. `internal/ui/executor_dock.go`)
   holding dock state: `open bool`, `mode {snapshot, live}`, the snapshot text +
   content version (for render caching, mirroring DetailModel's
   `viewportContentVersion`/`viewSignature` discipline), and the live pane IDs
   when promoted. One clear purpose: own the dock's state and rendering.
2. **Snapshot source** — reuse `executor.CapturePaneContent(
   executor.TmuxSessionName(taskID), N)` where `N` is sized to the dock's
   content height. No new tmux helper needed.
3. **Live join/break** — a focused single-pane join below the TUI pane. Where it
   overlaps with `detail.go`'s `joinTmuxPanes`/`breakTmuxPanes`, factor the
   shared tmux sequence into a reusable helper rather than copy-pasting; the dock
   needs only the executor pane (no side-by-side shell), so its path is simpler.
4. **App wiring** (`internal/ui/app.go`) — toggle key in the keymap; `shift`+
   arrow handlers for promote/demote; insert the dock into `viewDashboard()`'s
   vertical composition (shrinking `kanbanHeight` by the dock height when open);
   a cheap snapshot-refresh tick that runs **only** while the dock is open and in
   snapshot mode.
5. **Quick input removal** — delete the dedicated quick-input code and edit the
   shared footer/keymap/message paths per the inventory below. Approve/deny
   (`y`/`N`) and detail (`enter`) stay.

## Quick Input Removal Inventory

Dedicated (delete): `app.go` QuickInput keybinding (291–294), apply (362),
`quickInputFocused` field (512), `replyInput` init (610–612, 653), update
routing (804–806), tab handler (2230–2239), `updateQuickInput()` (2293–2338),
focused footer render (1967–1972), `sendTextToExecutor()` (4756–4767);
`config/keybindings.go` `QuickInput` field (59); all quick-input tests in
`app_test.go`.

Shared (edit, don't delete): `app.go` KeyMap `QuickInput` field (106), footer
hints (1929, 1931 — drop `tab input`/`tab reply`, keep approve/deny/detail),
`executorRespondedMsg` (4725–4730) and its handler (1322–1342 — drop the
`"reply"` action, keep `"approve"`/`"deny"`).

## Performance Requirements

- Snapshot mode must add **no measurable cost** to board navigation beyond one
  `capture-pane` per selection change. Re-capture is throttled/debounced so rapid
  arrowing does not spawn a capture per keystroke.
- The dock closed must be a **zero-cost no-op** — no ticks, no captures, no
  layout math beyond a single boolean check.
- Promoting to live must feel instant on warm re-entry (sub-100ms) and ≤~700ms
  cold.
- View rendering uses the existing signature/cache discipline so an open dock in
  snapshot mode does not force full re-renders every frame.

## Testing Strategy (required by request)

1. **Unit tests** — dock state machine (toggle, snapshot↔live transitions,
   selection-swap behavior, focus in/out), footer no longer advertises quick
   input, approve/deny still wired. TDD where practical.
2. **QA** — drive the real TUI in an isolated ty instance (per the
   `reference_qa_isolated_ty_tui` runbook) via tmux + `--debug-state-file`:
   toggle the dock, snapshot follows selection, shift-arrow promotes and lets you
   type, shift-up/esc demotes, re-entry is instant, closing restores the board.
   Capture screenshot evidence.
3. **Perf regression** — confirm board navigation with the dock **closed** is
   unchanged from `main` (no new ticks/captures), and that approve/deny/detail
   paths are unaffected. Use the existing benchmark harness if present.
4. **Perf of the new feature** — measure snapshot capture latency, cold vs warm
   live-join latency, and that rapid board navigation with the dock open in
   snapshot mode does not regress frame time. Assert against the targets above.

## YAGNI / Out of Scope

- No multiple simultaneous live panes; no pinning a live pane to a task while
  browsing another (explicitly decided against).
- No inline text-entry box (that was the removed quick-input).
- No persistence of dock open/closed across restarts (can revisit later).

## Loose ends to finalize during implementation

- **Toggle key:** propose `e` (executor) if free in the current keymap; verify
  against `DefaultKeyMap` and fall back to an unused key otherwise.
- **Dock height:** propose ~40% of terminal height when open; consider reusing
  the detail-view height config so resize behavior is consistent.
- **Snapshot lines:** dynamic — capture exactly enough lines to fill the dock's
  content height (not a fixed small count).

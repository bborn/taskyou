# List view: live executor pane

**Date:** 2026-06-07
**Status:** Approved (design)
**Branch:** `task/4196-list-view-status-should-have-active-whic`

## Summary

When the dashboard list view has a row selected, show that task's **live, interactive executor tmux pane** on the right of the screen — Bubble Tea list on the left, the real executor pane joined on the right. This turns the list into a master/detail triage surface: scan tasks on the left, watch (and optionally interact with) the selected task's executor on the right, and press `Enter` to drop into the full detail workspace.

This builds on the existing tmux integration: `ty` already runs inside a tmux UI session, and the detail view already joins a task's executor pane out of the daemon session into the UI session. The list-view pane reuses that machinery for a different *layout* (left/right split instead of the detail view's top/bottom-split).

## Goals

- In list view, the selected task's executor pane appears live on the right.
- Navigating the list updates which executor is shown, without thrashing tmux.
- The right pane is **draggable to resize**, like the detail view's panes, and the chosen width persists.
- Leaving list view (any exit path) never kills a running executor.

## Non-goals

- Showing more than one executor pane at a time (only the selected task's).
- A shell pane in list view (that remains a detail-view feature).
- Read-only/captured previews (we join the real pane; rejected in favor of live).
- Reworking the detail view's layout.

## Background / current architecture

(Verified against current code on the task branch.)

- **`ty` runs inside tmux and requires it.** `cmd/task/main.go` re-execs into a tmux UI session named `task-ui-<sessionID>` if `$TMUX` is unset; the Bubble Tea TUI is pane `:.0`. Mouse is enabled via `tea.WithMouseCellMotion()` (`cmd/task/main.go:3594`), so tmux pane borders are drag-resizable.
- **Executors live in a daemon session.** Each task's executor runs in a window of `task-daemon-<sessionID>` (`internal/executor`, `TmuxSessionName(taskID)`, `new-window`). `executor.IsExecutorSessionRunning(taskID)` reports whether it's live; `executor.CapturePaneContent(target, n)` can read pane text.
- **Detail view joins panes.** `internal/ui/detail.go` `joinTmuxPanes()` does `join-pane -v` to bring the executor below the (shrunk) TUI pane, then `join-pane -h` / `split-window -h` for the shell. It tracks `claudePaneID`, `workdirPaneID`, `tuiPaneID`, `cachedWindowTarget`, `daemonSessionID`, and guards async work with `paneLoading`.
- **Drag-resize persistence pattern (to mirror).** Detail view captures `initialDetailHeight` / `initialShellWidth` right after join, then on tick/teardown compares the current tmux pane size (`display-message -p '#{pane_width}'`) against the initial; if it changed by >2% it treats it as a user drag and persists via `saveShellPaneWidth()` → setting `shell_pane_width` (`internal/config/config.go`). Next join reads it back with `getShellPaneWidth()`.
- **List view today.** `internal/ui/list_view.go` is a pure Bubble Tea table (selection, sort, filters). Selection moves via `MoveUp/MoveDown`; `Enter`/click/`1-9` call `m.loadTask(id)` which opens the detail view. The list view does no tmux work today.
- **Race lessons (memory `tmux-pane-join-architecture`).** Once a pane is joined into the UI session it has *moved* out of the daemon window — operate on it by **pane ID**, not the daemon window target, and serialize pane operations behind a loading flag to avoid concurrent join/break races killing panes.

## Design

### Visible states

The right pane's presence is derived from the selected task and a visibility toggle:

| Condition | Layout |
|---|---|
| Preview visible **and** selected task has a live executor | **Split**: list left (~55%), executor right (~45%, or persisted width) |
| Preview visible **and** selected task has no live executor | **Full-width**: pane broken back to daemon, window unsplit, list reflows full width |
| Preview hidden (`p`) | Always full-width list; no joining regardless of selection |

Preview is **on by default** when entering list view.

### Selection → pane swap (debounce + serialization)

Moving the cursor must not touch tmux on every keystroke.

1. **Debounce.** On selection change, arm a ~300 ms timer using `tea.Tick` carrying a monotonically increasing **generation counter**. A tick whose generation ≠ the current generation is stale and ignored. Rapid arrowing just moves the highlight; tmux acts only once the selection settles.

2. **Pure decision function.** When a live (non-stale) tick fires, compute the desired action from pure inputs and return one of:
   - `none` — selected task already joined, or nothing to do.
   - `collapse` — preview visible but selected task has no live executor (or preview hidden) and a pane is currently joined → break it back, unsplit.
   - `swap(taskID)` — preview visible, selected task has a live executor different from the joined one → break current (if any), join the new one.

   ```
   decidePreviewAction(sel, joined, visible, executorRunning(sel), loading) -> Action
   ```
   This function has no I/O and is **unit-tested** across the combinations (hidden, no-executor, same task, different task, mid-load).

3. **Serialized execution.** A single `previewLoading` flag gates execution; never run two join/break operations concurrently. A request arriving mid-flight sets a `previewDirty` marker; on completion we re-run the decision against the *latest* selection so we always converge. All async results flow back as a `previewUpdatedMsg` (mirroring detail view's `panesJoinedMsg`) — no shared-state mutation from goroutines.

4. **Swap mechanics** (all targeting by **pane ID**):
   - Break the currently-joined preview pane back to its daemon window (`break-pane`/`join-pane` reverse) if one is joined.
   - `join-pane -h` the selected task's executor pane into the right slot at the persisted/default width.
   - `select-pane` back to the **list (TUI) pane** so keystrokes keep driving Bubble Tea.

### Draggable resize + persistence

- The executor pane border is drag-resizable for free (tmux mouse mode is already on).
- Mirror the detail view's persistence: capture `initialListExecutorWidth` right after join; on a periodic tick / on teardown, compare current `#{pane_width}` to initial and, if changed >2%, persist via a new setting **`list_executor_pane_width`** (`internal/config/config.go`).
- New join reads it back (`getListExecutorPaneWidth()`), defaulting to **45%** when unset. Getter/saver mirror `getShellPaneWidth` / `saveShellPaneWidth`.

### Focus & keybindings (list view)

- `↑/↓`, sort/filter keys, `v`, `p` — drive the Bubble Tea list as today.
- `Shift-Right` → move tmux focus into the executor pane to interact; `Shift-Left` → focus back to the list. Reuses the detail view's existing root-level `select-pane` bindings.
- `Enter` (and click / `1-9`) → open the **full detail view** (detail top + executor + shell bottom), unchanged. The inline pane is for triage/peek; detail is the full workspace.
- `p` → toggle the executor pane off/on.

### Lifecycle / teardown (correctness-critical)

The executor must **never** be killed by leaving the list view. Always break the preview pane back to its daemon window before tearing down, on every exit path:

- Toggle to board (`v`) → break back, unsplit, restore full-width TUI.
- `Enter` into detail → break back + unsplit first, then the detail view joins as today (minor flicker acceptable; guarantees a single owner of the executor pane).
- `p` hide → break back, unsplit.
- App quit → break back before the UI session/window closes.
- Window resize (`WindowSizeMsg`) → re-apply the split proportion to the persisted/default width.

### Code shape

- **Shared helper.** Extract the pane primitives the detail view already has — join/break/capture by pane ID, the `loading` guard, current-size read, drag-detect/save — into a small reusable unit (e.g. `internal/ui/executor_pane.go`) used by both detail and list. This avoids duplicating the racy logic and trims `detail.go`. Scope the extraction to what list view needs; do not refactor unrelated detail-view behavior.
- **List view owns layout + decision only.** `ListView` (or `AppModel`) gains: `previewVisible bool`, `previewLoading bool`, `previewJoinedTaskID int64`, `previewDirty bool`, `previewGen int`, `initialListExecutorWidth int`. The pure `decidePreviewAction` lives next to the list view and is tested.
- New config constant `SettingListExecutorPaneWidth = "list_executor_pane_width"`.

## Testing

**Unit (Go):**
- `decidePreviewAction` truth table: hidden; no executor; same task already joined; different task with executor; request while loading (→ dirty); preview toggled off while joined (→ collapse).
- Debounce generation logic: a stale-generation tick is a no-op; only the latest generation triggers an action.
- Existing list-view tests continue to pass.

**Manual / QA (isolated `ty` TUI harness + screenshots):**
- Split appears for a running task; collapses for a backlog/done task; reappears on returning to a running task.
- Rapid arrow navigation does not thrash (only settles after ~300 ms).
- `Shift-Right`/`Shift-Left` focus in/out; typing reaches the executor.
- Drag the border → width changes and **persists** across hide/show and across leaving/re-entering list view.
- `Enter` opens the full detail view with the executor intact.
- Teardown: toggle to board, hide with `p`, and **quit** all leave the executor running in the daemon (verify the daemon window still exists; no orphaned/killed pane).
- Capture screenshots of: split state, full-width (no-executor) state, focused-executor state, and post-resize state. Attach to the PR.

## Risks & mitigations

- **Join/break races** (documented in memory) → serialize behind `previewLoading`, target by pane ID, drop stale generations.
- **Accidentally killing an executor on teardown** → always break back before unsplit/exit; QA explicitly verifies the daemon window survives.
- **Layout churn on mixed lists** (collapse/expand as you cross running/non-running tasks) → accepted per design decision; debounce smooths it.
- **Handoff to detail view** → break back + unsplit before detail joins, so there is never contention over the same executor pane.

## Open questions

None blocking. Default width (45%) and debounce (300 ms) are tunable during QA.

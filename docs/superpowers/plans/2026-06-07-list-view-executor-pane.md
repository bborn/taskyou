# List view live executor pane — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the dashboard list view, show the selected task's live, interactive executor tmux pane on the right; navigating the list swaps it (debounced), it's drag-resizable with a persisted width, and it's always broken back to the daemon (never killed) when you leave.

**Architecture:** The `ty` TUI already runs inside a tmux UI session; the executor for each task runs in a window of the daemon session. A new `listExecutorPane` controller (in `internal/ui/executor_pane.go`) joins the selected task's executor pane to the right of the Bubble Tea TUI pane and breaks it back on teardown — reusing the detail view's process-preserving window logic. `AppModel` drives it: a pure `decidePreviewAction` function decides noop/collapse/swap; selection navigation triggers a **debounced, serialized, async** swap; explicit transitions (Enter→detail, `v`→board, `p` hide, quit) do a **synchronous** break-back so the executor is back in the daemon before anything else touches the UI session. Bubble Tea does NOT render the executor — tmux owns that pane; the list simply re-renders at the narrower width tmux gives it.

**Tech Stack:** Go, Bubble Tea (charmbracelet), tmux CLI, SQLite settings, the repo's `scripts/qa/` TUI harness + VHS for screenshots.

---

## Key facts (verified against current code)

- TUI runs in tmux session `task-ui-<sid>`, pane `:.0`; mouse is on (`tea.WithMouseCellMotion()` in `cmd/task/main.go:3594`) so pane borders are drag-resizable.
- Executor windows live in `task-daemon-<sid>`, window name `executor.TmuxWindowName(taskID)` (= `task_<id>`); target via `executor.TmuxSessionName(taskID)`.
- Detail view's `joinTmuxPanes()` (`internal/ui/detail.go:1280`) **kills every non-TUI pane in the UI session** on entry → the preview pane MUST be broken back before opening detail.
- Detail view persists drag-resizes by capturing an initial pane size, comparing current vs initial (>2% = user drag), and saving a `%` setting (`internal/config/config.go`: `SettingShellPaneWidth`). We mirror this with a new `SettingListExecutorPaneWidth`.
- Process-preserving break-back pattern lives in `breakTmuxPanes()` (`detail.go:1860`) using `findOrCreateTaskWindow()` (`detail.go:1770`) + `killPaneWithProcess()` (`detail.go:2171`). On join-pane failure it falls back to `break-pane` to a new window rather than killing the executor.
- There is **no** `IsExecutorSessionRunning`; the reliable "does this task have a live executor pane" predicate is "a `task_<id>` window exists in a daemon session" — i.e. `findTaskWindow()` (`detail.go:772`) returning non-empty.

---

## File structure

- **Create** `internal/ui/executor_pane.go` — package-level tmux helpers shared with detail (`findTaskWindowTarget`, `killTmuxPaneWithProcess`, `findOrCreateDaemonWindow`), the `listExecutorPane` controller (`join`/`breakBack`/width helpers), the pure `decidePreviewAction`, the `validatePanePct` helper, and the msg/result types.
- **Create** `internal/ui/executor_pane_test.go` — unit tests for `decidePreviewAction` and `validatePanePct`.
- **Modify** `internal/config/config.go` — add `SettingListExecutorPaneWidth`.
- **Modify** `internal/ui/app.go` — preview state on `AppModel`; arm-on-navigation (debounce); `listPreviewTickMsg` / `listPreviewUpdatedMsg` handlers; `p` toggle; synchronous break-back on Enter/`v`/quit; arm-on-enter-list.
- **Modify** `internal/ui/debug.go` — expose preview state (`preview_visible`, `preview_joined_task_id`) for QA assertions.
- **Modify** `internal/ui/list_view.go` — show a small "· pane hidden" affordance in the summary when `p` has hidden the preview (so the toggle is discoverable). Selection accessor `SelectedTask()` already exists.

> **Scope decision (deviation from spec):** to avoid destabilizing the detail view's battle-tested pane code in the same change, this plan **adds** the shared helpers as new package functions and has the list controller use them, but does **not** refactor `detail.go` to call them (beyond nothing required). Deduping detail.go onto these helpers is a follow-up once the list pane is proven. Noted here intentionally.

---

## Task 1: Config setting for the persisted width

**Files:**
- Modify: `internal/config/config.go` (the `Setting*` const block near line 21)

- [ ] **Step 1: Add the setting constant**

In the existing `const ( ... )` settings block, after `SettingShellPaneWidth`:

```go
	SettingListExecutorPaneWidth = "list_executor_pane_width"
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "config: add list_executor_pane_width setting"
```

---

## Task 2: Pure decision function + width validator (TDD)

**Files:**
- Create: `internal/ui/executor_pane.go`
- Create: `internal/ui/executor_pane_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/ui/executor_pane_test.go`:

```go
package ui

import "testing"

func TestDecidePreviewAction(t *testing.T) {
	cases := []struct {
		name        string
		selectedID  int64
		selHasExec  bool
		joinedID    int64
		visible     bool
		want        previewAction
	}{
		{"hidden, nothing joined", 5, true, 0, false, previewNoop},
		{"hidden, something joined", 5, true, 5, false, previewCollapse},
		{"visible, no selection", 0, false, 0, true, previewNoop},
		{"visible, selected has no executor, none joined", 7, false, 0, true, previewNoop},
		{"visible, selected has no executor, stale joined", 7, false, 9, true, previewCollapse},
		{"visible, selected has executor, none joined", 5, true, 0, true, previewSwap},
		{"visible, selected has executor, different joined", 5, true, 9, true, previewSwap},
		{"visible, selected has executor, already joined", 5, true, 5, true, previewNoop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decidePreviewAction(c.selectedID, c.selHasExec, c.joinedID, c.visible)
			if got != c.want {
				t.Fatalf("decidePreviewAction(%d,%v,%d,%v) = %v, want %v",
					c.selectedID, c.selHasExec, c.joinedID, c.visible, got, c.want)
			}
		})
	}
}

func TestValidatePanePct(t *testing.T) {
	cases := []struct{ in, def, want string }{
		{"", "45%", "45%"},
		{"garbage", "45%", "45%"},
		{"45", "45%", "45%"},   // missing %
		{"5%", "45%", "45%"},   // below min
		{"95%", "45%", "45%"},  // above max
		{"30%", "45%", "30%"},  // valid
		{"90%", "45%", "90%"},  // valid edge
	}
	for _, c := range cases {
		if got := validatePanePct(c.in, c.def); got != c.want {
			t.Fatalf("validatePanePct(%q,%q) = %q, want %q", c.in, c.def, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/ui/ -run 'TestDecidePreviewAction|TestValidatePanePct' -v`
Expected: FAIL (undefined: previewAction, decidePreviewAction, validatePanePct).

- [ ] **Step 3: Implement the pure pieces**

Create `internal/ui/executor_pane.go` with the imports and the pure functions:

```go
package ui

import (
	"context"
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// previewAction is what the list-view executor preview should do for the
// current selection. Decided purely (no I/O) by decidePreviewAction.
type previewAction int

const (
	previewNoop previewAction = iota
	previewCollapse
	previewSwap
)

// decidePreviewAction decides the preview pane action from the current state.
//   selectedID:  task selected in the list (0 if none)
//   selHasExec:  whether the selected task has a live executor window to show
//   joinedID:    task whose executor pane is currently joined (0 if none)
//   visible:     whether the preview is enabled (toggled with `p`)
func decidePreviewAction(selectedID int64, selHasExec bool, joinedID int64, visible bool) previewAction {
	if !visible || selectedID == 0 || !selHasExec {
		if joinedID != 0 {
			return previewCollapse
		}
		return previewNoop
	}
	if joinedID == selectedID {
		return previewNoop
	}
	return previewSwap
}

// validatePanePct returns v if it is a "NN%" string within [10,90], else def.
func validatePanePct(v, def string) string {
	if strings.HasSuffix(v, "%") {
		if p, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil && p >= 10 && p <= 90 {
			return v
		}
	}
	return def
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/ui/ -run 'TestDecidePreviewAction|TestValidatePanePct' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_pane.go internal/ui/executor_pane_test.go
git commit -m "list view: pure decision + width validator for executor preview"
```

---

## Task 3: Shared tmux helpers (find window target, kill pane, find/create daemon window)

**Files:**
- Modify: `internal/ui/executor_pane.go`

These mirror detail.go's `findTaskWindow`, `killPaneWithProcess`, and `findOrCreateTaskWindow` but as package functions taking explicit params (no DetailModel). Not unit-tested (pure tmux I/O); exercised by QA in Task 8.

- [ ] **Step 1: Add the helpers**

Append to `internal/ui/executor_pane.go`:

```go
// findTaskWindowTarget returns the "session:windowID" of the daemon window
// running task taskID's executor, or "" if none exists. Updates the stored
// window ID in the DB when found.
func findTaskWindowTarget(ctx context.Context, database *db.DB, task *db.Task) string {
	if task == nil {
		return ""
	}
	windowName := executor.TmuxWindowName(task.ID)
	out, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-a",
		"-F", "#{session_name}:#{window_id}:#{window_name}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		sessionName, windowID, name := parts[0], parts[1], parts[2]
		if !strings.HasPrefix(sessionName, "task-daemon-") {
			continue
		}
		if name == windowName {
			if database != nil && windowID != "" {
				database.UpdateTaskWindowID(task.ID, windowID)
				task.TmuxWindowID = windowID
			}
			return sessionName + ":" + windowID
		}
	}
	return ""
}

// killTmuxPaneWithProcess kills a pane and the process inside it (SIGKILL,
// since Claude ignores SIGTERM). Shared shape with detail.go's method.
func killTmuxPaneWithProcess(ctx context.Context, paneID string) {
	if paneID == "" {
		return
	}
	if pidOut, err := osExec.CommandContext(ctx, "tmux", "display-message", "-t", paneID, "-p", "#{pane_pid}").Output(); err == nil {
		if pid := strings.TrimSpace(string(pidOut)); pid != "" {
			osExec.CommandContext(ctx, "kill", "-9", pid).Run()
		}
	}
	osExec.CommandContext(ctx, "tmux", "kill-pane", "-t", paneID).Run()
}

// findOrCreateDaemonWindow returns the window ID for task's executor in
// daemonSession, creating a placeholder window if none exists. Mirrors
// detail.go's findOrCreateTaskWindow.
func findOrCreateDaemonWindow(ctx context.Context, database *db.DB, task *db.Task, daemonSession, workdir string) string {
	windowName := executor.TmuxWindowName(task.ID)
	// Priority 1: stored window ID if still valid.
	if task.TmuxWindowID != "" {
		out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-t", task.TmuxWindowID, "-p", "#{window_id}").Output()
		if err == nil && strings.TrimSpace(string(out)) == task.TmuxWindowID {
			return task.TmuxWindowID
		}
		if database != nil {
			database.UpdateTaskWindowID(task.ID, "")
		}
	}
	// Priority 2: search by name.
	if out, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-t", daemonSession, "-F", "#{window_id}:#{window_name}").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && parts[1] == windowName {
				if database != nil {
					database.UpdateTaskWindowID(task.ID, parts[0])
				}
				return parts[0]
			}
		}
	}
	// Priority 3: create placeholder.
	if err := osExec.CommandContext(ctx, "tmux", "new-window", "-d", "-t", daemonSession+":", "-n", windowName, "-c", workdir, "tail", "-f", "/dev/null").Run(); err != nil {
		return ""
	}
	if out, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-t", daemonSession, "-F", "#{window_id}:#{window_name}").Output(); err == nil {
		var id string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && parts[1] == windowName {
				id = parts[0]
			}
		}
		if id != "" {
			if database != nil {
				database.UpdateTaskWindowID(task.ID, id)
			}
			return id
		}
	}
	return ""
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean (functions are unused for now — Go allows unused package-level funcs).

- [ ] **Step 3: Commit**

```bash
git add internal/ui/executor_pane.go
git commit -m "list view: shared tmux helpers for executor preview"
```

---

## Task 4: The `listExecutorPane` controller (join right / break back / width persistence)

**Files:**
- Modify: `internal/ui/executor_pane.go`

- [ ] **Step 1: Add the controller and its state/result types**

Append to `internal/ui/executor_pane.go`:

```go
// joinedState is the volatile state of the currently-joined preview pane.
// Owned by AppModel; passed into controller ops and returned back so the tea
// goroutine never mutates AppModel directly.
type joinedState struct {
	taskID        int64
	paneID        string
	daemonSession string
	initialWidth  int // executor pane width % captured right after join
}

// previewResult is returned from controller ops and applied in AppModel.Update.
type previewResult struct {
	gen   int // generation that requested this op (for staleness checks)
	state joinedState
	err   error
}

// listExecutorPane joins/breaks a single task's executor pane to the right of
// the TUI pane in the UI session. Identity/config only; volatile joined state
// is threaded through method args/returns.
type listExecutorPane struct {
	database      *db.DB
	uiSessionName string
	tuiPaneID     string
}

func newListExecutorPane(database *db.DB) *listExecutorPane {
	return &listExecutorPane{database: database}
}

// ensureIdentity caches the UI session name and TUI pane id. Call from the
// main loop (not a goroutine); the active pane is the TUI pane because we
// always select-pane back to it after every op.
func (p *listExecutorPane) ensureIdentity() bool {
	if os.Getenv("TMUX") == "" {
		return false
	}
	if p.uiSessionName == "" {
		if out, err := osExec.Command("tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
			p.uiSessionName = strings.TrimSpace(string(out))
		}
	}
	if p.tuiPaneID == "" {
		if out, err := osExec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			p.tuiPaneID = strings.TrimSpace(string(out))
		}
	}
	return p.uiSessionName != "" && p.tuiPaneID != ""
}

func (p *listExecutorPane) widthPref() string {
	def := "45%"
	if p.database == nil {
		return def
	}
	v, err := p.database.GetSetting(config.SettingListExecutorPaneWidth)
	if err != nil || v == "" {
		return def
	}
	return validatePanePct(v, def)
}

// currentWidthPct returns the joined executor pane's current width as a % of
// (executor + tui) widths, or 0 on error.
func (p *listExecutorPane) currentWidthPct(execPaneID string) int {
	if execPaneID == "" || p.tuiPaneID == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ew := tmuxPaneWidth(ctx, execPaneID)
	tw := tmuxPaneWidth(ctx, p.tuiPaneID)
	if ew <= 0 || tw <= 0 {
		return 0
	}
	return (ew*100 + (ew+tw)/2) / (ew + tw)
}

func tmuxPaneWidth(ctx context.Context, paneID string) int {
	out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", paneID, "#{pane_width}").Output()
	if err != nil {
		return 0
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return w
}

// saveWidthIfResized persists the executor pane width if the user dragged it
// (>2% off the captured initial), mirroring detail.go's resize-detection.
func (p *listExecutorPane) saveWidthIfResized(prev joinedState) {
	if prev.paneID == "" || prev.initialWidth <= 0 {
		return
	}
	cur := p.currentWidthPct(prev.paneID)
	if cur <= 0 {
		return
	}
	if cur < prev.initialWidth-2 || cur > prev.initialWidth+2 {
		if p.database != nil && cur >= 10 && cur <= 90 {
			p.database.SetSetting(config.SettingListExecutorPaneWidth, strconv.Itoa(cur)+"%")
		}
	}
}
```

- [ ] **Step 2: Add `breakBack` (process-preserving)**

Append:

```go
// breakBack returns the joined executor pane to its daemon window (creating the
// window if tmux destroyed it when the pane was joined out), preserving the
// executor process, and resizes the TUI pane back to full width. Persists the
// width first if `save` or the user dragged it. Returns a zeroed joinedState.
func (p *listExecutorPane) breakBack(prev joinedState, save bool) joinedState {
	if prev.paneID == "" {
		p.resizeTuiFull()
		return joinedState{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if save {
		// Explicit teardown always records the current width.
		if cur := p.currentWidthPct(prev.paneID); cur >= 10 && cur <= 90 && p.database != nil {
			p.database.SetSetting(config.SettingListExecutorPaneWidth, strconv.Itoa(cur)+"%")
		}
	} else {
		p.saveWidthIfResized(prev)
	}

	daemon := prev.daemonSession
	if daemon == "" {
		daemon = executor.TmuxDaemonSession
	}
	task := &db.Task{ID: prev.taskID}
	if p.database != nil {
		if full, err := p.database.GetTask(prev.taskID); err == nil && full != nil {
			task = full
		}
	}
	windowID := findOrCreateDaemonWindow(ctx, p.database, task, daemon, p.workdir(task))
	if windowID == "" {
		// Could not re-home — DO NOT kill the executor; leave the pane in place.
		p.resizeTuiFull()
		return joinedState{}
	}
	// Detect a placeholder-only window so we can remove it after joining.
	paneCountOut, _ := osExec.CommandContext(ctx, "tmux", "display-message", "-t", windowID, "-p", "#{window_panes}").Output()
	hadPlaceholder := strings.TrimSpace(string(paneCountOut)) == "1"

	err := osExec.CommandContext(ctx, "tmux", "join-pane", "-d", "-s", prev.paneID, "-t", windowID).Run()
	if err != nil {
		// Fallback: break to a fresh window rather than kill the process.
		osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", prev.paneID, "-t", daemon+":", "-n", executor.TmuxWindowName(prev.taskID)).Run()
		p.resizeTuiFull()
		return joinedState{}
	}
	if hadPlaceholder {
		osExec.CommandContext(ctx, "tmux", "kill-pane", "-t", windowID+".0").Run()
	}
	p.resizeTuiFull()
	return joinedState{}
}

func (p *listExecutorPane) workdir(task *db.Task) string {
	if task != nil && task.WorktreePath != "" {
		return task.WorktreePath
	}
	return os.Getenv("HOME")
}

func (p *listExecutorPane) resizeTuiFull() {
	if p.tuiPaneID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", p.tuiPaneID, "-x", "100%").Run()
}
```

- [ ] **Step 3: Add `joinRight`**

Append:

```go
// joinRight joins task's executor pane to the right of the TUI pane at the
// preferred width, then returns focus to the TUI. Returns the new joinedState,
// or a zeroed state (no error) if the task has no live executor window.
func (p *listExecutorPane) joinRight(task *db.Task) (joinedState, error) {
	if task == nil {
		return joinedState{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	target := findTaskWindowTarget(ctx, p.database, task) // "session:windowID" or ""
	if target == "" {
		return joinedState{}, nil
	}
	daemonSession := target
	if i := strings.Index(target, ":"); i >= 0 {
		daemonSession = target[:i]
	}

	// Choose the source pane: stored Claude pane if still in the window, else first.
	panesOut, err := osExec.CommandContext(ctx, "tmux", "list-panes", "-t", target, "-F", "#{pane_id}").Output()
	if err != nil {
		return joinedState{}, nil
	}
	paneIDs := strings.Split(strings.TrimSpace(string(panesOut)), "\n")
	if len(paneIDs) == 0 || paneIDs[0] == "" {
		return joinedState{}, nil
	}
	src := paneIDs[0]
	if task.ClaudePaneID != "" {
		for _, id := range paneIDs {
			if id == task.ClaudePaneID {
				src = task.ClaudePaneID
				break
			}
		}
	}

	// Join to the right of the TUI pane at the preferred width.
	if err := osExec.CommandContext(ctx, "tmux", "join-pane", "-h", "-l", p.widthPref(), "-s", src, "-t", p.tuiPaneID).Run(); err != nil {
		return joinedState{}, err
	}
	// The joined pane is now active; capture its id.
	joinedID := src
	if out, err := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
		joinedID = strings.TrimSpace(string(out))
	}
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", joinedID, "-T", "Executor #"+strconv.FormatInt(task.ID, 10)).Run()

	// Visual affordance + draggable borders, and Shift-arrow focus switching.
	p.styleAndBind()

	// Return focus to the TUI so list keys keep driving Bubble Tea.
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", p.tuiPaneID).Run()

	st := joinedState{taskID: task.ID, paneID: joinedID, daemonSession: daemonSession}
	st.initialWidth = p.currentWidthPct(joinedID)
	return st, nil
}

// styleAndBind sets draggable heavy borders + Shift-Left/Right pane focus
// switching (mirrors detail.go). Unbound again on breakBack via unbindKeys.
func (p *listExecutorPane) styleAndBind() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := p.uiSessionName
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "pane-border-lines", "heavy").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "pane-border-indicators", "arrows").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "pane-active-border-style", "fg=#61AFEF").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "status-right", " drag border to resize · shift+→ focus executor ").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Right", "select-pane", "-t", ":.+").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Left", "select-pane", "-t", ":.-").Run()
}

func (p *listExecutorPane) unbindStyleAndBind() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := p.uiSessionName
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Right").Run()
	osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", "S-Left").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "status-right", " ").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "pane-border-lines", "single").Run()
	osExec.CommandContext(ctx, "tmux", "set-option", "-t", s, "pane-border-indicators", "off").Run()
}
```

Then call `p.unbindStyleAndBind()` at the top of `breakBack` when `prev.paneID != ""` (before the join-back), so styling/bindings are reset. Add that one line.

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: clean. (Requires `db.DB.GetTask`, `UpdateTaskWindowID`, `GetSetting`, `SetSetting` — all exist.)

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_pane.go
git commit -m "list view: executor pane controller (join right / break back / width)"
```

---

## Task 5: AppModel state + async swap on navigation (debounced, serialized)

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Add state fields to AppModel**

In the `AppModel` struct (near the other dashboard fields ~line 530), add:

```go
	// List-view live executor preview
	listPreview        *listExecutorPane
	previewVisible     bool        // p toggle; true = show pane for selected task
	previewLoading     bool        // a join/break op is in flight
	previewDirty       bool        // a newer request arrived mid-flight
	previewGen         int         // debounce generation; stale ticks ignored
	previewJoined      joinedState // currently-joined pane state
```

In the AppModel constructor (where other fields init, ~line 671), add:

```go
	m.listPreview = newListExecutorPane(database)
	m.previewVisible = true
```

(Use the actual db variable name in that constructor; it is the same `database`/`db` passed to other views.)

- [ ] **Step 2: Add the message types**

Near the other msg types in app.go:

```go
// listPreviewTickMsg fires after the selection-change debounce; gen guards staleness.
type listPreviewTickMsg struct{ gen int }

// listPreviewUpdatedMsg carries the result of an async preview join/break.
type listPreviewUpdatedMsg struct{ result previewResult }
```

- [ ] **Step 3: Add arm + helpers**

Add methods on AppModel:

```go
// armPreview schedules a debounced preview refresh for the current selection.
// Returns a Cmd to be batched into the caller's return. No-op outside tmux.
func (m *AppModel) armPreview() tea.Cmd {
	if m.listPreview == nil || os.Getenv("TMUX") == "" {
		return nil
	}
	m.previewGen++
	gen := m.previewGen
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return listPreviewTickMsg{gen: gen}
	})
}

// selectedHasExecutor reports whether task t has a live executor window to show.
func (m *AppModel) selectedHasExecutor(t *db.Task) bool {
	if t == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return findTaskWindowTarget(ctx, nil, t) != ""
}

// runPreviewAction dispatches the decided action as an async Cmd (serialized by
// previewLoading). Call only from Update.
func (m *AppModel) runPreviewAction(gen int) tea.Cmd {
	if !m.listPreview.ensureIdentity() {
		return nil
	}
	sel := m.listView.SelectedTask()
	var selID int64
	if sel != nil {
		selID = sel.ID
	}
	action := decidePreviewAction(selID, m.selectedHasExecutor(sel), m.previewJoined.taskID, m.previewVisible)
	if action == previewNoop {
		return nil
	}
	if m.previewLoading {
		m.previewDirty = true
		return nil
	}
	m.previewLoading = true
	prev := m.previewJoined
	p := m.listPreview
	switch action {
	case previewCollapse:
		return func() tea.Msg {
			st := p.breakBack(prev, false)
			return listPreviewUpdatedMsg{result: previewResult{gen: gen, state: st}}
		}
	default: // previewSwap
		return func() tea.Msg {
			p.breakBack(prev, false) // return the old pane first
			st, err := p.joinRight(sel)
			return listPreviewUpdatedMsg{result: previewResult{gen: gen, state: st, err: err}}
		}
	}
}
```

- [ ] **Step 4: Handle the two messages in Update**

In `AppModel.Update`'s type switch, add cases:

```go
	case listPreviewTickMsg:
		if msg.gen != m.previewGen {
			return m, nil // stale debounce tick
		}
		return m, m.runPreviewAction(msg.gen)

	case listPreviewUpdatedMsg:
		m.previewLoading = false
		m.previewJoined = msg.result.state
		if m.previewDirty {
			m.previewDirty = false
			return m, m.runPreviewAction(m.previewGen)
		}
		return m, nil
```

- [ ] **Step 5: Arm on list navigation**

In `updateListNav` (app.go ~765), after each branch that changes the selection (`MoveUp`, `MoveDown`, `SelectVisibleRow`, and the click handler path in Update ~1024), batch in `m.armPreview()`. Concretely, change the navigation returns from `return true, nil` to `return true, m.armPreview()` for the Up/Down cases, and for `SelectVisibleRow`/click return `tea.Batch(m.loadTask(...), ...)` only where it opens detail (those don't arm; see Task 6). For pure selection moves:

```go
	case key.Matches(msg, m.keys.Up):
		m.listView.MoveUp()
		return true, m.armPreview()
	case key.Matches(msg, m.keys.Down):
		m.listView.MoveDown()
		return true, m.armPreview()
```

- [ ] **Step 6: Build + run existing tests**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean build, tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/app.go
git commit -m "list view: debounced async executor preview on navigation"
```

---

## Task 6: Synchronous teardown on explicit transitions (Enter, v, p, quit)

**Files:**
- Modify: `internal/ui/app.go`

The executor pane must be broken back synchronously before detail's `joinTmuxPanes` (which kills non-TUI panes), before switching to board, on hide, and on quit.

- [ ] **Step 1: Add a synchronous teardown helper**

```go
// teardownPreviewSync breaks the joined executor pane back to its daemon window
// synchronously (so the executor is re-homed before detail view or quit). Safe
// to call when nothing is joined.
func (m *AppModel) teardownPreviewSync(save bool) {
	if m.listPreview == nil || m.previewJoined.paneID == "" {
		return
	}
	m.listPreview.ensureIdentity()
	m.listPreview.breakBack(m.previewJoined, save)
	m.previewJoined = joinedState{}
	m.previewLoading = false
	m.previewDirty = false
	m.previewGen++ // invalidate any in-flight debounce tick
}
```

- [ ] **Step 2: Tear down before opening detail**

In every list-view path that calls `m.loadTask(...)` to open detail (Enter handler ~2304, numeric `SelectVisibleRow` ~ in updateListNav, click handler ~1024), call `m.teardownPreviewSync(true)` immediately before `m.loadTask`. Example for the Enter case:

```go
	case key.Matches(msg, m.keys.Enter):
		if task := m.dashboardSelectedTask(); task != nil {
			m.teardownPreviewSync(true)
			return m, m.loadTask(task.ID)
		}
```

- [ ] **Step 3: Tear down / re-arm on view toggle**

In `toggleViewMode` (~749): when switching to board, tear down; when switching to list, arm. Return any arm Cmd. Since `toggleViewMode` currently returns nothing, have its caller (the `ToggleView` case in `updateDashboard` ~2187) capture and return the cmd:

```go
	if key.Matches(msg, m.keys.ToggleView) {
		cmd := m.toggleViewMode()
		return m, cmd
	}
```

And change `toggleViewMode` to:

```go
func (m *AppModel) toggleViewMode() tea.Cmd {
	if m.viewMode == ViewModeList {
		m.teardownPreviewSync(true)
		m.viewMode = ViewModeBoard
		// ... existing selection-sync code ...
		return nil
	}
	m.viewMode = ViewModeList
	// ... existing selection-sync code ...
	return m.armPreview()
}
```

(Preserve the existing selection-preservation logic already in `toggleViewMode`.)

- [ ] **Step 4: `p` toggles preview visibility**

In `updateListNav`, add a branch (before the default) for the `p` key. NOTE: `p`/`ctrl+p` is currently "go to task" (palette) per the help bar — verify the existing binding in `updateListNav`/keys and pick a free key if `p` is taken (fallback: `e` is "edit"; use `P` (shift) for preview toggle if `p` conflicts). Assuming `P`:

```go
	if msg.String() == "P" {
		m.previewVisible = !m.previewVisible
		if m.previewVisible {
			return true, m.armPreview()
		}
		m.teardownPreviewSync(true)
		return true, nil
	}
```

- [ ] **Step 5: Tear down on quit**

Find the quit path (search `tea.Quit` in app.go). Before returning `tea.Quit` from the dashboard/list, call `m.teardownPreviewSync(true)`. If there is a central place where the app already cleans up the detail view on quit, add the preview teardown alongside it.

- [ ] **Step 6: Build + tests**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean, pass.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/app.go
git commit -m "list view: synchronous executor-pane teardown on enter/toggle/hide/quit"
```

---

## Task 7: Debug state + hidden-affordance (small)

**Files:**
- Modify: `internal/ui/debug.go`
- Modify: `internal/ui/list_view.go`

- [ ] **Step 1: Expose preview state for QA**

In `debug.go`'s `DebugList` (or the dashboard debug section), add fields and populate them where the app builds debug state:

```go
	PreviewVisible      bool  `json:"preview_visible"`
	PreviewJoinedTaskID int64 `json:"preview_joined_task_id"`
```

Populate from `m.previewVisible` and `m.previewJoined.taskID` in the function that assembles the debug snapshot (search `debugState`/`DebugState` in app.go/debug.go).

- [ ] **Step 2: Hidden affordance in the summary**

In `list_view.go` `renderFilterChips`, the `ListView` cannot see AppModel's `previewVisible`. Add a settable field `previewHidden bool` on `ListView` and a setter `SetPreviewHidden(bool)`; call it from AppModel whenever `previewVisible` changes. When true, append a dim ` · pane hidden (P)` to the summary; when false and in tmux, optionally nothing. Keep it tiny.

```go
// in ListView struct:
	previewHidden bool
// setter:
func (l *ListView) SetPreviewHidden(v bool) { if l != nil { l.previewHidden = v } }
// in renderFilterChips, after building summary:
	if l.previewHidden {
		summary += sep + muted.Render("pane hidden (P)")
	}
```

- [ ] **Step 3: Build + tests**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean, pass. (Update `TestListViewSummaryShowsActiveFiltersOnly` only if it asserts exact summary; it uses Contains, so unaffected.)

- [ ] **Step 4: Commit**

```bash
git add internal/ui/debug.go internal/ui/list_view.go internal/ui/app.go
git commit -m "list view: expose preview state in debug + hidden affordance"
```

---

## Task 8: Manual QA with the harness + screenshots

**Files:** none (verification). Uses `scripts/qa/` + the seeded `/tmp/ty-qa` instance and the `live panes without daemon` tier (`scripts/qa/ty-qa-agent.sh`).

- [ ] **Step 1: Rebuild the isolated instance and stand up a live executor for a task**

```bash
scripts/qa/ty-qa-up.sh offerlab
# seed tasks (see session notes) then:
scripts/qa/ty-qa-agent.sh 1     # stand up a real executor window+pane for task #1 and point its DB row at it
scripts/qa/ty-qa-tui.sh
```

- [ ] **Step 2: Drive and assert split appears for a running task**

```bash
scripts/qa/ty-qa-key.sh v          # list view
scripts/qa/ty-qa-key.sh Up Up      # land on task #1 (running)
sleep 1                            # debounce
scripts/qa/ty-qa-state.sh '.dashboard.list.preview_joined_task_id'   # expect 1
scripts/qa/ty-qa-capture.sh        # eyeball: list left, executor right
```
Expected: `preview_joined_task_id == 1`; two panes visible.

- [ ] **Step 3: Assert collapse on a no-executor task; no thrash on rapid nav**

```bash
scripts/qa/ty-qa-key.sh Down Down Down   # move to a backlog/done task quickly
sleep 1
scripts/qa/ty-qa-state.sh '.dashboard.list.preview_joined_task_id'   # expect 0 (collapsed, full-width)
```

- [ ] **Step 4: Assert executor SURVIVES teardown (the critical correctness check)**

```bash
# Note the executor window before:
tmux list-windows -a -F '#{session_name}:#{window_name}' | grep task_1
scripts/qa/ty-qa-key.sh Up Up      # back to #1 (re-joins)
sleep 1
scripts/qa/ty-qa-key.sh v          # toggle to board -> synchronous break-back
tmux list-windows -a -F '#{session_name}:#{window_name}' | grep task_1   # window MUST still exist
```
Expected: the `task_1` daemon window still exists after toggling away (executor not killed).

- [ ] **Step 5: Enter detail from the preview, confirm clean handoff**

```bash
scripts/qa/ty-qa-key.sh v Up Up    # list, select #1
sleep 1
scripts/qa/ty-qa-key.sh Enter      # open detail; preview must break back first, detail joins
scripts/qa/ty-qa-state.sh '.view'  # "detail"
scripts/qa/ty-qa-state.sh '.detail.has_panes'  # true
scripts/qa/ty-qa-key.sh Escape
```

- [ ] **Step 6: Drag-resize persistence**

Attach (`tmux attach -t task-ui-qa`), drag the border, detach; toggle board/list; confirm the executor pane reappears at the dragged width. Check `ty settings get list_executor_pane_width` reflects it.

- [ ] **Step 7: PNG screenshots via VHS**

Render split state, collapsed/full-width state, and focused-executor state to PNGs (custom tape against `/tmp/ty-qa/tasks.db`, no DB wipe; press `v`, navigate, `Sleep`). Attach to the PR.

- [ ] **Step 8: Tear down**

```bash
scripts/qa/ty-qa-down.sh --purge
```

---

## Task 9: Final integration + PR

- [ ] **Step 1: Full build + test**

Run: `go build ./... && go test ./internal/ui/ ./internal/config/ ./internal/db/`
Expected: pass (pre-existing `internal/ai`/`internal/autocomplete` failures unrelated).

- [ ] **Step 2: Run the repo lint (before pushing)**

Use the project lint workflow (gofmt/vet); fix any issues.

- [ ] **Step 3: Update the PR**

Push; update PR #564 description with the executor-pane feature, the debounce/teardown model, the drag-resize setting, and the QA screenshots.

---

## Self-review notes

- **Spec coverage:** layout/states (Tasks 4–6), debounce+serialization+pure decision (Tasks 2,5), draggable resize + persisted `list_executor_pane_width` (Tasks 1,4), focus keys Shift-←/→ (Task 4 `styleAndBind`), teardown on all exits incl. quit and the detail-view kill-panes hazard (Task 6), tests (Task 2) + QA/screenshots (Task 8). All covered.
- **Type consistency:** `previewAction`, `joinedState`, `previewResult`, `listExecutorPane`, `decidePreviewAction`, `validatePanePct`, `armPreview`, `runPreviewAction`, `teardownPreviewSync`, `listPreviewTickMsg`, `listPreviewUpdatedMsg` used consistently across tasks.
- **Open risk to verify during execution:** the `p` key may already be bound ("go to task") — Task 6 Step 4 picks `P` if so; confirm against the keymap. The exact quit path (Task 6 Step 5) must be located in app.go. `db.DB.GetTask` signature to be confirmed at use.

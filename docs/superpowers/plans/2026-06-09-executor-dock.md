# Executor Dock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unreliable quick-input feature with a toggleable executor dock below the kanban: a cheap read-only snapshot of the highlighted task's executor that follows the selection, plus a fast path to a live joined tmux pane on shift-arrow.

**Architecture:** A new focused `DockModel` (`internal/ui/executor_dock.go`) owns dock state (open/closed, snapshot-vs-live, current snapshot text) and rendering. All tmux side effects go through a `paneController` interface so the state machine and view are unit-testable without a real tmux server. The real controller adapts the proven join/break/capture code already in `detail.go` and `executor.go`. Snapshot mode only ever calls `tmux capture-pane` (read-only, cheap); the expensive `join-pane` is paid only on explicit promotion and reused warm thereafter. App wiring inserts the dock into `viewDashboard()` and routes a toggle key + promotion.

**Tech Stack:** Go, Bubble Tea (charmbracelet/bubbletea), Lipgloss, tmux.

---

## File Structure

- **Create** `internal/ui/executor_dock.go` — `DockModel`, `paneController` interface, snapshot/live state machine, `View()`, height math.
- **Create** `internal/ui/executor_dock_tmux.go` — `tmuxPaneController` (real impl), adapting `detail.go`/`executor.go` tmux sequences.
- **Create** `internal/ui/executor_dock_test.go` — unit tests with a fake `paneController`.
- **Modify** `internal/ui/app.go` — remove quick input; add dock field, toggle key, promotion handling, snapshot tick, and `viewDashboard()` composition.
- **Modify** `internal/ui/app_test.go` — delete quick-input tests; add footer/wiring assertions.
- **Modify** `internal/config/keybindings.go` — remove `QuickInput` config field.

Each phase below is independently shippable.

---

# Phase 1 — Remove Quick Input

Pure removal + shared-line edits. After this phase the TUI builds, tests pass, and the footer no longer advertises quick input; approve/deny/detail still work.

### Task 1.1: Delete the dedicated quick-input update + helpers

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Delete `updateQuickInput`** — remove the whole function `internal/ui/app.go:2293-2338` (`// updateQuickInput handles input...` through its closing brace).

- [ ] **Step 2: Delete `sendTextToExecutor`** — remove `internal/ui/app.go:4756-4767` (`// sendTextToExecutor sends arbitrary text...` through closing brace). It is only used by quick input.

- [ ] **Step 3: Delete the QuickInput key handler case** — remove `internal/ui/app.go:2230-2239`:

```go
	case key.Matches(msg, m.keys.QuickInput):
		// Focus the quick input field if selected task needs input
		if task := m.kanban.SelectedTask(); task != nil {
			if m.tasksNeedingInput[task.ID] || m.detectPermissionPrompt(task.ID) {
				m.quickInputFocused = true
				m.replyInput.SetValue("")
				m.replyInput.Focus()
				return m, textinput.Blink
			}
		}
```

- [ ] **Step 4: Delete the quick-input update routing** — remove `internal/ui/app.go:803-806`:

```go
		// Handle quick input mode (needs all message types for text input)
		if m.currentView == ViewDashboard && m.quickInputFocused {
			return m.updateQuickInput(msg)
		}
```

- [ ] **Step 5: Build to surface remaining references**

Run: `go build ./internal/ui/ 2>&1 | head -30`
Expected: compile errors only for now-unused `replyInput`, `quickInputFocused`, and the `"reply"` branch / footer block we remove next (and possibly unused `textinput` import). Note them.

### Task 1.2: Remove quick-input model fields and the focused footer block

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Remove the model fields** — delete `internal/ui/app.go:510-512`:

```go
	// Quick input for sending text to executor (always visible when task needs input)
	replyInput        textinput.Model
	quickInputFocused bool // Whether quick input field has keyboard focus
```

- [ ] **Step 2: Remove `replyInput` initialization** — open `internal/ui/app.go` around lines 605-655 and delete the block that constructs `replyInput` (a `textinput.New()` configured for replies) and the line assigning it into the model struct (`replyInput: ...`). Use:

Run: `grep -n "replyInput" internal/ui/app.go`
Expected after edit: no matches.

- [ ] **Step 3: Remove the focused-input footer block** — delete `internal/ui/app.go:1967-1972`:

```go
	// Show quick input bar when focused
	if m.quickInputFocused {
		label := warnStyle.Render("input: ")
		inputHints := hintStyle.Render("  enter send  esc cancel")
		lines = append(lines, barStyle.Render(label+m.replyInput.View()+inputHints))
	}
```

- [ ] **Step 4: Remove the now-unused `textinput` import if applicable**

Run: `grep -n "textinput\." internal/ui/app.go`
If `filterInput textinput.Model` (and similar) still use it, KEEP the import. Only remove the `"github.com/charmbracelet/bubbles/textinput"` import line if zero matches remain.

- [ ] **Step 5: Build**

Run: `go build ./internal/ui/ 2>&1 | head -30`
Expected: remaining errors only about the `"reply"` action branch and the keymap field (fixed next).

### Task 1.3: Edit shared footer hints and the executorResponded "reply" branch

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Fix footer hints** — at `internal/ui/app.go:1927-1932` replace:

```go
	var hints string
	if m.questionPrompts[task.ID] {
		hints = hintStyle.Render("tab reply  enter detail")
	} else {
		hints = hintStyle.Render("y approve  N deny  tab input  enter detail")
	}
```

with:

```go
	var hints string
	if m.questionPrompts[task.ID] {
		hints = hintStyle.Render("enter detail")
	} else {
		hints = hintStyle.Render("y approve  N deny  enter detail")
	}
```

- [ ] **Step 2: Fix the doc comment** — at `internal/ui/app.go:1915-1917` change `with approve/deny/tab-input hints. When quick input is focused, shows the text input.` to `with approve/deny/detail hints.`

- [ ] **Step 3: Remove the `"reply"` branch** — at `internal/ui/app.go:1326-1332` replace:

```go
			action := "Approved"
			if msg.action == "deny" {
				action = "Denied"
			} else if msg.action == "reply" {
				action = "Replied to"
			}
```

with:

```go
			action := "Approved"
			if msg.action == "deny" {
				action = "Denied"
			}
```

- [ ] **Step 4: Update `executorRespondedMsg` doc + action comment** — at `internal/ui/app.go:4725-4730` change the comment `// executorRespondedMsg is sent after approve/deny/reply...` to `// executorRespondedMsg is sent after approve/deny...` and the struct field comment `action string // "approve", "deny", or "reply"` to `action string // "approve" or "deny"`.

- [ ] **Step 5: Build**

Run: `go build ./internal/ui/ 2>&1 | head -30`
Expected: only the keymap `QuickInput` field reference (fixed next), if any.

### Task 1.4: Remove the QuickInput keybinding from KeyMap and config

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/config/keybindings.go`

- [ ] **Step 1: Remove KeyMap struct field** — delete `internal/ui/app.go:105-106`:

```go
	// Quick input focus
	QuickInput key.Binding
```

- [ ] **Step 2: Remove the default binding** — delete `internal/ui/app.go:291-294`:

```go
		QuickInput: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "input"),
		),
```

- [ ] **Step 3: Remove the config apply line** — delete `internal/ui/app.go:362`:

```go
	km.QuickInput = applyBinding(km.QuickInput, cfg.QuickInput)
```

- [ ] **Step 4: Remove the config struct field** — delete `internal/config/keybindings.go:59` (`QuickInput *KeybindingConfig` with its yaml tag).

- [ ] **Step 5: Build the whole module**

Run: `go build ./... 2>&1 | head -30`
Expected: clean build (no output).

### Task 1.5: Delete quick-input tests and assert the footer changed

**Files:**
- Modify: `internal/ui/app_test.go`

- [ ] **Step 1: Delete the quick-input tests** — remove these functions entirely: `TestQuickInput_TabEntersQuickInput`, `TestQuickInput_EscUnfocuses`, `TestQuickInput_EmptyEnterUnfocuses`, `TestQuickInput_EnterWithTextSendsAndUnfocuses`, `TestQuickInput_EnterWithTextNoSelectedTask`, `TestQuickInput_TabIgnoredWithoutBlockedTask`, `TestRenderExecutorPromptPreview_ShowsTabInputHint`, `TestRenderExecutorPromptPreview_QuickInputFocused`.

Run: `grep -n "quickInputFocused\|TestQuickInput\|ShowsTabInputHint\|tab input" internal/ui/app_test.go`
Expected after edit: no matches (also fix any other test asserting `"tab input"` in `TestRenderExecutorPromptPreview_NoPrompt` — update its expectation to assert the string `"enter detail"` is present and `"tab input"` is absent).

- [ ] **Step 2: Run the UI tests**

Run: `go test ./internal/ui/ 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go internal/config/keybindings.go
git commit -m "feat: remove unreliable quick-input feature

Deletes the tab-to-reply inline input and its tests. Approve/deny/detail
keybindings are unchanged. Frees the tab key for the executor dock toggle."
```

---

# Phase 2 — Executor Dock: Snapshot Mode

Adds the toggleable dock rendering a read-only snapshot that follows the selection. No live panes yet. Independently shippable and useful.

### Task 2.1: Create the DockModel + paneController seam (state machine)

**Files:**
- Create: `internal/ui/executor_dock.go`
- Test: `internal/ui/executor_dock_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ui

import (
	"testing"

	"howdyrunner/internal/db" // adjust to the module's actual import path for db
)

// fakePaneController records calls and returns canned snapshots.
type fakePaneController struct {
	captures   []int64 // task IDs captured, in order
	snapshot   string
	joined     []int64
	broken     []int64
	focused    []string
	tuiFocused bool
}

func (f *fakePaneController) Capture(task *db.Task, lines int) string {
	f.captures = append(f.captures, task.ID)
	return f.snapshot
}
func (f *fakePaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	f.joined = append(f.joined, task.ID)
	return "%99", nil
}
func (f *fakePaneController) BreakBack(task *db.Task, paneID string) error {
	f.broken = append(f.broken, task.ID)
	return nil
}
func (f *fakePaneController) FocusPane(paneID string) error {
	f.focused = append(f.focused, paneID)
	return nil
}
func (f *fakePaneController) TUIPaneFocused() bool { return f.tuiFocused }
func (f *fakePaneController) ResizeTUIFull()       {}

func TestDock_ToggleOpensAndCloses(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.IsOpen() {
		t.Fatal("dock should start closed")
	}
	d.Toggle()
	if !d.IsOpen() {
		t.Fatal("dock should be open after toggle")
	}
	d.Toggle()
	if d.IsOpen() {
		t.Fatal("dock should be closed after second toggle")
	}
}

func TestDock_ClosedIsZeroHeight(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.Height(40) != 0 {
		t.Fatalf("closed dock height = %d, want 0", d.Height(40))
	}
	d.Toggle()
	if d.Height(40) == 0 {
		t.Fatal("open dock height should be > 0")
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: FAIL — `undefined: NewDockModel`.

- [ ] **Step 3: Write the minimal implementation**

```go
package ui

import (
	"howdyrunner/internal/db" // adjust to the module's actual import path for db
)

// dockMode is the dock's current display mode.
type dockMode int

const (
	dockSnapshot dockMode = iota // read-only capture-pane text (cheap, follows selection)
	dockLive                     // a real executor pane joined below the TUI (interactive)
)

// paneController abstracts the tmux side effects the dock needs, so the dock's
// state machine and rendering can be unit-tested without a real tmux server.
type paneController interface {
	// Capture returns a read-only snapshot (last `lines`) of the task's executor pane.
	Capture(task *db.Task, lines int) string
	// JoinBelow joins the task's executor pane below the TUI pane, resizing the TUI
	// pane to tuiHeightPercent. Returns the joined pane id.
	JoinBelow(task *db.Task, tuiHeightPercent int) (paneID string, err error)
	// BreakBack returns a previously-joined executor pane to its daemon window.
	BreakBack(task *db.Task, paneID string) error
	// FocusPane gives keyboard focus to the given pane id.
	FocusPane(paneID string) error
	// TUIPaneFocused reports whether the TUI pane currently holds focus.
	TUIPaneFocused() bool
	// ResizeTUIFull resizes the TUI pane back to full height.
	ResizeTUIFull()
}

// DockModel owns the executor dock state and rendering. One responsibility:
// show the highlighted task's executor (snapshot or live) below the board.
type DockModel struct {
	ctl  paneController
	open bool
	mode dockMode

	// snapshot text and a version counter for render caching parity with DetailModel.
	snapshot       string
	contentVersion uint64

	// live-pane state (Phase 3)
	livePaneID string
	liveTaskID int64
}

// NewDockModel constructs a closed dock backed by the given controller.
func NewDockModel(ctl paneController) *DockModel {
	return &DockModel{ctl: ctl, mode: dockSnapshot}
}

// IsOpen reports whether the dock is visible.
func (d *DockModel) IsOpen() bool { return d.open }

// Toggle opens or closes the dock. Closing always reverts to snapshot mode.
func (d *DockModel) Toggle() {
	d.open = !d.open
	if !d.open {
		d.mode = dockSnapshot
		d.snapshot = ""
	}
}

// dockHeightPercent is the fraction of the terminal height the dock occupies.
const dockHeightPercent = 40

// Height returns the dock's rendered height for a given terminal height.
// Returns 0 when closed so the board layout is untouched.
func (d *DockModel) Height(termHeight int) int {
	if !d.open {
		return 0
	}
	h := termHeight * dockHeightPercent / 100
	if h < 6 {
		h = 6
	}
	return h
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_dock.go internal/ui/executor_dock_test.go
git commit -m "feat(dock): DockModel skeleton with paneController seam"
```

### Task 2.2: Snapshot refresh following the selection

**Files:**
- Modify: `internal/ui/executor_dock.go`
- Test: `internal/ui/executor_dock_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDock_RefreshCapturesSelectedTaskWhenOpenSnapshot(t *testing.T) {
	f := &fakePaneController{snapshot: "● Bash(ls)\nDo you want to proceed?"}
	d := NewDockModel(f)
	task := &db.Task{ID: 4281}

	// Closed: refresh must be a no-op (no capture).
	d.Refresh(task, 40)
	if len(f.captures) != 0 {
		t.Fatalf("closed dock captured %d times, want 0", len(f.captures))
	}

	// Open snapshot: refresh captures the selected task.
	d.Toggle()
	d.Refresh(task, 40)
	if len(f.captures) != 1 || f.captures[0] != 4281 {
		t.Fatalf("captures = %v, want [4281]", f.captures)
	}
	if d.Snapshot() != f.snapshot {
		t.Fatalf("snapshot = %q, want %q", d.Snapshot(), f.snapshot)
	}
}

func TestDock_RefreshBumpsVersionOnlyOnChange(t *testing.T) {
	f := &fakePaneController{snapshot: "same"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 1}
	d.Refresh(task, 40)
	v1 := d.contentVersion
	d.Refresh(task, 40) // identical content
	if d.contentVersion != v1 {
		t.Fatal("version bumped despite unchanged snapshot")
	}
	f.snapshot = "different"
	d.Refresh(task, 40)
	if d.contentVersion == v1 {
		t.Fatal("version did not bump on changed snapshot")
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/ui/ -run TestDock_Refresh -v 2>&1 | tail -20`
Expected: FAIL — `d.Refresh undefined` and `d.Snapshot undefined`.

- [ ] **Step 3: Write the implementation** — add to `internal/ui/executor_dock.go`:

```go
// Snapshot returns the most recently captured snapshot text.
func (d *DockModel) Snapshot() string { return d.snapshot }

// Refresh re-captures the selected task's executor into the snapshot, but only
// when the dock is open and in snapshot mode. termHeight sizes the capture so we
// read roughly enough lines to fill the dock. No-op otherwise (zero cost when
// closed or live). Returns true if the snapshot content changed.
func (d *DockModel) Refresh(task *db.Task, termHeight int) bool {
	if !d.open || d.mode != dockSnapshot || task == nil {
		return false
	}
	lines := d.Height(termHeight) // capture ~one screen's worth
	if lines < 1 {
		lines = 1
	}
	next := d.ctl.Capture(task, lines)
	if next == d.snapshot {
		return false
	}
	d.snapshot = next
	d.contentVersion++
	return true
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_dock.go internal/ui/executor_dock_test.go
git commit -m "feat(dock): snapshot refresh follows selection, cheap when closed"
```

### Task 2.3: Dock View rendering

**Files:**
- Modify: `internal/ui/executor_dock.go`
- Test: `internal/ui/executor_dock_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDock_ViewShowsSnapshotAndHeader(t *testing.T) {
	f := &fakePaneController{snapshot: "● Bash(cat x)\nProceed? > 1. Yes"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 4281}
	d.Refresh(task, 40)

	out := d.View(80, 40, task)
	if !strings.Contains(out, "4281") {
		t.Fatalf("view missing task id, got:\n%s", out)
	}
	if !strings.Contains(out, "Proceed?") {
		t.Fatalf("view missing snapshot content, got:\n%s", out)
	}
	if !strings.Contains(out, "shift") { // promotion hint
		t.Fatalf("view missing interact hint, got:\n%s", out)
	}
}

func TestDock_ViewEmptyWhenClosed(t *testing.T) {
	d := NewDockModel(&fakePaneController{})
	if d.View(80, 40, &db.Task{ID: 1}) != "" {
		t.Fatal("closed dock View should be empty")
	}
}
```

Add `"strings"` to the test file imports.

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/ui/ -run TestDock_View -v 2>&1 | tail -20`
Expected: FAIL — `d.View undefined`.

- [ ] **Step 3: Write the implementation** — add to `internal/ui/executor_dock.go` (import `fmt`, `strings`, `github.com/charmbracelet/lipgloss`):

```go
// View renders the dock for the given terminal size and highlighted task.
// Returns "" when closed. In live mode the region is occupied by the real tmux
// pane, so we render only a one-line title bar (the pane draws itself).
func (d *DockModel) View(width, termHeight int, task *db.Task) string {
	if !d.open {
		return ""
	}
	h := d.Height(termHeight)
	titleStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(ColorMuted)

	id := int64(0)
	if task != nil {
		id = task.ID
	}

	if d.mode == dockLive {
		title := titleStyle.Render(fmt.Sprintf(" executor #%d ", id)) +
			hintStyle.Render("(LIVE · shift+↑ back to board)")
		return lipgloss.NewStyle().Width(width).Render(title)
	}

	title := titleStyle.Render(fmt.Sprintf(" executor #%d ", id)) +
		hintStyle.Render("(shift+↓ to interact · read-only)")

	body := d.snapshot
	if strings.TrimSpace(body) == "" {
		body = hintStyle.Render("  (no executor output yet)")
	}
	// Keep the last (h-1) lines so the title fits.
	bodyLines := strings.Split(body, "\n")
	maxBody := h - 1
	if maxBody < 1 {
		maxBody = 1
	}
	if len(bodyLines) > maxBody {
		bodyLines = bodyLines[len(bodyLines)-maxBody:]
	}
	box := lipgloss.NewStyle().
		Width(width).
		Height(h).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, bodyLines...)...))
}
```

NOTE: confirm `ColorWarning` and `ColorMuted` exist in the `ui` package (used by `renderExecutorPromptPreview`). They do.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_dock.go internal/ui/executor_dock_test.go
git commit -m "feat(dock): render snapshot view with title + interact hint"
```

### Task 2.4: Real tmux controller (snapshot-only methods)

**Files:**
- Create: `internal/ui/executor_dock_tmux.go`

- [ ] **Step 1: Implement the real controller's Capture; stub the live methods for now**

```go
package ui

import (
	"howdyrunner/internal/executor" // adjust to module path
	"howdyrunner/internal/db"       // adjust to module path
)

// tmuxPaneController is the real paneController backed by tmux + the executor pkg.
type tmuxPaneController struct {
	uiSessionName string // e.g. "task-ui-<pid>"
}

func newTmuxPaneController(uiSessionName string) *tmuxPaneController {
	return &tmuxPaneController{uiSessionName: uiSessionName}
}

// Capture reads the task's executor pane read-only. Prefer the stored pane id
// (most precise); fall back to the task's window target.
func (c *tmuxPaneController) Capture(task *db.Task, lines int) string {
	if task == nil {
		return ""
	}
	target := task.ClaudePaneID
	if target == "" {
		target = executor.TmuxSessionName(task.ID)
	}
	return executor.CapturePaneContent(target, lines)
}

// Live-pane methods are implemented in Phase 3.
func (c *tmuxPaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	return "", nil
}
func (c *tmuxPaneController) BreakBack(task *db.Task, paneID string) error { return nil }
func (c *tmuxPaneController) FocusPane(paneID string) error               { return nil }
func (c *tmuxPaneController) TUIPaneFocused() bool                        { return true }
func (c *tmuxPaneController) ResizeTUIFull()                              {}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/ui/ 2>&1 | head -20`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/executor_dock_tmux.go
git commit -m "feat(dock): real tmux controller with read-only capture"
```

### Task 2.5: Wire the dock into AppModel + toggle key + layout

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Add the keymap field** — in the `KeyMap` struct (where `QuickInput` was, ~line 105) add:

```go
	// Toggle the executor dock below the board
	ToggleDock key.Binding
```

- [ ] **Step 2: Add the default binding** — in `DefaultKeyMap()` (where the QuickInput binding was, ~line 291) add. `tab` is now free:

```go
		ToggleDock: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "executor dock"),
		),
```

- [ ] **Step 3: Add the model field** — in the `AppModel` struct add near the kanban field:

```go
	// Executor dock shown below the board (toggleable)
	dock *DockModel
```

- [ ] **Step 4: Initialize it** — where `AppModel` is constructed (same constructor that builds `m.kanban`), set:

```go
	m.dock = NewDockModel(newTmuxPaneController(m.uiSessionName))
```

Run: `grep -n "uiSessionName" internal/ui/app.go | head`
If the AppModel has no `uiSessionName` field, pass the value the detail view uses for `m.uiSessionName` (search detail.go for how it is derived) or `""` — `Capture` does not need it; only Phase 3 join uses it. Using `""` here is acceptable until Phase 3 wires the real session name.

- [ ] **Step 5: Handle the toggle key** — in the main dashboard key switch (near the other `case key.Matches(...)` blocks, e.g. after the Help case ~line 2259) add:

```go
	case key.Matches(msg, m.keys.ToggleDock):
		m.dock.Toggle()
		if m.dock.IsOpen() {
			if task := m.kanban.SelectedTask(); task != nil {
				m.dock.Refresh(task, m.height)
			}
		} else {
			m.dock.ResizeTUIFull() // ensure board reclaims full height (no-op in snapshot)
		}
		return m, nil
```

- [ ] **Step 6: Refresh snapshot on navigation** — after each of the four nav handlers at `internal/ui/app.go:2033-2047` (`MoveLeft/Right/Up/Down`), capture immediately so the dock feels responsive. Replace the four cases with:

```go
	case key.Matches(msg, m.keys.Left):
		m.kanban.MoveLeft()
		m.refreshDockForSelection()

	case key.Matches(msg, m.keys.Right):
		m.kanban.MoveRight()
		m.refreshDockForSelection()

	case key.Matches(msg, m.keys.Up):
		m.kanban.MoveUp()
		m.refreshDockForSelection()

	case key.Matches(msg, m.keys.Down):
		m.kanban.MoveDown()
		m.refreshDockForSelection()
```

(Keep any trailing `return m, nil` exactly as the originals had it — inspect lines 2033-2048 and preserve their existing return/cmd handling; only add the `m.refreshDockForSelection()` call.)

- [ ] **Step 7: Add the helper** — near `viewDashboard`:

```go
// refreshDockForSelection re-captures the dock snapshot for the currently
// highlighted task. Cheap no-op when the dock is closed.
func (m *AppModel) refreshDockForSelection() {
	if m.dock == nil || !m.dock.IsOpen() {
		return
	}
	if task := m.kanban.SelectedTask(); task != nil {
		m.dock.Refresh(task, m.height)
	}
}
```

- [ ] **Step 8: Insert the dock into the layout** — in `viewDashboard()` (`internal/ui/app.go:1710-1757`) compute the dock view/height and subtract from `kanbanHeight`. After the `promptPreview` block (~line 1716) add:

```go
	// Render the executor dock if open
	dockView := ""
	dockHeight := 0
	if m.dock != nil && m.dock.IsOpen() {
		dockView = m.dock.View(m.width, m.height, m.kanban.SelectedTask())
		dockHeight = lipgloss.Height(dockView)
	}
```

Change the `kanbanHeight` computation (line 1730) to also subtract `dockHeight`:

```go
	kanbanHeight := m.height - headerHeight - filterBarHeight - helpHeight - promptPreviewHeight - dockHeight
```

And in the content assembly (the `else` branch at lines 1750-1756) append the dock above the help line:

```go
	} else {
		kanbanView := m.kanban.View()
		contentParts = append(contentParts, kanbanView)
		if promptPreview != "" {
			contentParts = append(contentParts, promptPreview)
		}
		if dockView != "" {
			contentParts = append(contentParts, dockView)
		}
		contentParts = append(contentParts, helpView)
	}
```

- [ ] **Step 9: Build**

Run: `go build ./... 2>&1 | head -20`
Expected: clean.

- [ ] **Step 10: Add a wiring test** — in `internal/ui/app_test.go`:

```go
func TestToggleDock_OpensDockAndShrinksBoard(t *testing.T) {
	m := newTestAppModel(t) // use the existing test constructor used by other app tests
	m.height = 40
	m.width = 100

	// Closed by default.
	if m.dock.IsOpen() {
		t.Fatal("dock should start closed")
	}
	// Press tab.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(*AppModel)
	if !m.dock.IsOpen() {
		t.Fatal("tab should open the dock")
	}
	view := m.viewDashboard()
	if !strings.Contains(view, "executor #") {
		t.Fatalf("dashboard missing dock after toggle:\n%s", view)
	}
}
```

NOTE: match `newTestAppModel` to whatever helper the existing app tests use (grep `func newTestAppModel` / how other tests build an `AppModel`). If tests construct `AppModel` differently, mirror that exact pattern.

- [ ] **Step 11: Run tests**

Run: `go test ./internal/ui/ 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat(dock): toggle with tab, render below board, follow selection"
```

### Task 2.6: Snapshot refresh tick (keeps live executor output current)

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Find the existing tick pattern** — the app already runs periodic ticks (e.g. for polling tasks/spinner). Identify one:

Run: `grep -n "tea.Tick\|tickMsg\|func.*tick" internal/ui/app.go | head`

- [ ] **Step 2: Add a dock tick message + command** — mirror the existing tick style. Add:

```go
type dockTickMsg struct{}

// dockTick schedules the next dock snapshot refresh. ~750ms keeps the snapshot
// current without hammering tmux. Only meaningful while the dock is open in
// snapshot mode; the handler self-gates and reschedules.
func dockTick() tea.Cmd {
	return tea.Tick(750*time.Millisecond, func(time.Time) tea.Msg { return dockTickMsg{} })
}
```

- [ ] **Step 3: Kick off the tick when the dock opens** — in the `ToggleDock` case (Task 2.5 Step 5), when opening, also return the tick:

```go
	case key.Matches(msg, m.keys.ToggleDock):
		m.dock.Toggle()
		if m.dock.IsOpen() {
			m.refreshDockForSelection()
			return m, dockTick()
		}
		m.dock.ResizeTUIFull()
		return m, nil
```

- [ ] **Step 4: Handle the tick** — in the top-level `Update` switch add a case:

```go
	case dockTickMsg:
		if m.dock != nil && m.dock.IsOpen() {
			m.refreshDockForSelection()
			return m, dockTick() // reschedule only while open
		}
		return m, nil // dock closed: let the tick chain die (zero cost when closed)
```

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./internal/ui/ 2>&1 | tail -10`
Expected: clean build, PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/app.go
git commit -m "feat(dock): periodic snapshot refresh while open (dies when closed)"
```

---

# Phase 3 — Executor Dock: Live Pane Promotion

Adds the expensive-once, warm-thereafter live pane. Builds on detail.go's proven join/break/shift-binding code. This phase requires a real tmux server to verify; unit tests cover the state machine via the fake controller, and a manual QA script verifies the tmux behavior.

### Task 3.1: State-machine transitions (promote / demote) — unit tested

**Files:**
- Modify: `internal/ui/executor_dock.go`
- Test: `internal/ui/executor_dock_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDock_PromoteJoinsAndFocuses(t *testing.T) {
	f := &fakePaneController{snapshot: "x"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 7}

	d.Promote(task, 40)
	if d.mode != dockLive {
		t.Fatal("promote should switch to live mode")
	}
	if len(f.joined) != 1 || f.joined[0] != 7 {
		t.Fatalf("joined = %v, want [7]", f.joined)
	}
	if len(f.focused) != 1 || f.focused[0] != "%99" {
		t.Fatalf("focused = %v, want [%%99]", f.focused)
	}
	if d.liveTaskID != 7 || d.livePaneID != "%99" {
		t.Fatalf("live state = (%d,%q)", d.liveTaskID, d.livePaneID)
	}
}

func TestDock_PromoteNoopWhenClosedOrAlreadyLive(t *testing.T) {
	f := &fakePaneController{}
	d := NewDockModel(f)
	task := &db.Task{ID: 7}
	d.Promote(task, 40) // closed
	if len(f.joined) != 0 {
		t.Fatal("promote while closed must not join")
	}
	d.Toggle()
	d.Promote(task, 40)
	d.Promote(task, 40) // already live
	if len(f.joined) != 1 {
		t.Fatalf("joined %d times, want 1", len(f.joined))
	}
}

func TestDock_DemoteBreaksBackToSnapshot(t *testing.T) {
	f := &fakePaneController{snapshot: "y"}
	d := NewDockModel(f)
	d.Toggle()
	task := &db.Task{ID: 7}
	d.Promote(task, 40)

	d.Demote(task)
	if d.mode != dockSnapshot {
		t.Fatal("demote should return to snapshot mode")
	}
	if len(f.broken) != 1 || f.broken[0] != 7 {
		t.Fatalf("broken = %v, want [7]", f.broken)
	}
	if d.livePaneID != "" || d.liveTaskID != 0 {
		t.Fatal("live state should be cleared after demote")
	}
}

func TestDock_SelectionChangeWhileLiveDemotes(t *testing.T) {
	f := &fakePaneController{snapshot: "z"}
	d := NewDockModel(f)
	d.Toggle()
	a := &db.Task{ID: 1}
	b := &db.Task{ID: 2}
	d.Promote(a, 40)

	// Refresh now targets a different task -> must demote A first, then snapshot B.
	d.RefreshOrDemote(b, 40)
	if d.mode != dockSnapshot {
		t.Fatal("moving selection off a live task should demote it")
	}
	if len(f.broken) != 1 || f.broken[0] != 1 {
		t.Fatalf("broken = %v, want [1]", f.broken)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: FAIL — `Promote/Demote/RefreshOrDemote undefined`.

- [ ] **Step 3: Implement** — add to `internal/ui/executor_dock.go`:

```go
// tuiHeightPercentFor returns the TUI pane height percent when the dock holds a
// live pane occupying dockHeightPercent of the screen.
func tuiHeightPercentFor() int { return 100 - dockHeightPercent }

// Promote joins the highlighted task's executor pane below the board and moves
// focus into it. Expensive on cold tasks, cheap (warm) on revisit. No-op unless
// open and currently in snapshot mode.
func (d *DockModel) Promote(task *db.Task, termHeight int) {
	if !d.open || d.mode != dockSnapshot || task == nil {
		return
	}
	paneID, err := d.ctl.JoinBelow(task, tuiHeightPercentFor())
	if err != nil || paneID == "" {
		return // stay in snapshot mode on failure
	}
	d.livePaneID = paneID
	d.liveTaskID = task.ID
	d.mode = dockLive
	_ = d.ctl.FocusPane(paneID)
}

// Demote returns the live pane to its daemon window and reverts to snapshot mode.
func (d *DockModel) Demote(task *db.Task) {
	if d.mode != dockLive {
		return
	}
	_ = d.ctl.BreakBack(task, d.livePaneID)
	d.ctl.ResizeTUIFull()
	d.livePaneID = ""
	d.liveTaskID = 0
	d.mode = dockSnapshot
	d.snapshot = ""
	d.contentVersion++
}

// RefreshOrDemote is called on selection change. If a live pane is up for a
// different task, demote it first (the region must show the new task's snapshot),
// then refresh. If live for the SAME task, leave it. Otherwise just refresh.
func (d *DockModel) RefreshOrDemote(task *db.Task, termHeight int) {
	if !d.open || task == nil {
		return
	}
	if d.mode == dockLive {
		if d.liveTaskID == task.ID {
			return // same task still live; leave it
		}
		// Different task selected: demote the old live pane.
		// We pass a synthetic task carrying the live task's id so the controller
		// can target the right daemon window.
		d.Demote(&db.Task{ID: d.liveTaskID})
	}
	d.Refresh(task, termHeight)
}
```

NOTE: `Demote` needs the live task to break back to the right window. The controller's `BreakBack` should resolve the daemon window from the task id (see Task 3.3). Passing `&db.Task{ID: d.liveTaskID}` is sufficient because resolution is by id/`TmuxWindowName`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/ -run TestDock_ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_dock.go internal/ui/executor_dock_test.go
git commit -m "feat(dock): promote/demote state machine for live pane"
```

### Task 3.2: Wire promotion keys + focus tracking into AppModel

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Intercept shift+down/right to promote** — in the dashboard key switch, BEFORE the plain `Up/Down/Left/Right` cases (so shift variants win), add:

```go
	case msg.Type == tea.KeyShiftDown || msg.Type == tea.KeyShiftRight:
		if m.dock != nil && m.dock.IsOpen() {
			if task := m.kanban.SelectedTask(); task != nil {
				m.dock.Promote(task, m.height)
				return m, dockFocusPoll() // start watching for focus return
			}
		}
		// Dock closed: fall through to normal behavior (no-op today).
		return m, nil
```

NOTE: confirm Bubble Tea exposes `tea.KeyShiftDown`/`tea.KeyShiftRight` in the vendored version:
Run: `grep -rn "KeyShiftDown\|KeyShiftRight" $(go env GOMODCACHE)/github.com/charmbracelet/bubbletea*/key.go | head`
If those constants do not exist, match on `msg.String() == "shift+down" || msg.String() == "shift+right"` instead.

- [ ] **Step 2: Update nav handlers to use `RefreshOrDemote`** — change `refreshDockForSelection` (Task 2.5 Step 7) to delegate:

```go
func (m *AppModel) refreshDockForSelection() {
	if m.dock == nil || !m.dock.IsOpen() {
		return
	}
	m.dock.RefreshOrDemote(m.kanban.SelectedTask(), m.height)
}
```

- [ ] **Step 3: Add a focus poll** — when live, the user presses tmux-handled `shift+up` to return to the board. Poll focus to detect it and demote. Mirror detail.go's `checkFocusState` cadence (200ms):

```go
type dockFocusMsg struct{}

func dockFocusPoll() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return dockFocusMsg{} })
}
```

Handle it in the top-level `Update` switch:

```go
	case dockFocusMsg:
		if m.dock == nil || !m.dock.IsOpen() || !m.dock.IsLive() {
			return m, nil // stop polling
		}
		if m.dock.ctl.TUIPaneFocused() {
			// User shift-up'd back to the board. Demote and resume snapshot.
			m.dock.Demote(m.kanban.SelectedTask())
			return m, dockTick()
		}
		return m, dockFocusPoll() // keep watching while focus is in the live pane
```

- [ ] **Step 4: Expose `IsLive`** — add to `executor_dock.go`:

```go
// IsLive reports whether a live executor pane is currently joined.
func (d *DockModel) IsLive() bool { return d.mode == dockLive }
```

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./internal/ui/ 2>&1 | tail -10`
Expected: clean, PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/app.go internal/ui/executor_dock.go
git commit -m "feat(dock): shift-arrow promotes; focus poll demotes on return"
```

### Task 3.3: Implement the real tmux JoinBelow / BreakBack / focus

**Files:**
- Modify: `internal/ui/executor_dock_tmux.go`

This adapts `detail.go:joinTmuxPanes` (Claude pane only — no shell) and `detail.go:breakTmuxPanes` (join back to daemon). Reuse the same `os/exec` + context-timeout discipline.

- [ ] **Step 1: Implement window resolution** — add a helper that finds the task's daemon window target, mirroring `detail.go:findTaskWindow` (`list-windows -a`, match `task-<id>`, prefer stored `TmuxWindowID`):

```go
import (
	"context"
	osExec "os/exec"
	"strings"
	"time"
)

func (c *tmuxPaneController) findWindow(task *db.Task) string {
	if task == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	want := executor.TmuxWindowName(task.ID)
	out, err := osExec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F",
		"#{session_name}:#{window_id}:#{window_name}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		session, windowID, name := parts[0], parts[1], parts[2]
		if !strings.HasPrefix(session, "task-daemon-") {
			continue
		}
		if name == want {
			return session + ":" + windowID
		}
	}
	return ""
}
```

- [ ] **Step 2: Implement JoinBelow** (Claude pane only):

```go
func (c *tmuxPaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowTarget := c.findWindow(task)
	if windowTarget == "" {
		return "", fmt.Errorf("dock: window not found for task %d", task.ID)
	}

	// Current (TUI) pane id, to refocus and resize afterwards.
	tuiOut, err := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	tuiPaneID := strings.TrimSpace(string(tuiOut))

	// Choose the executor pane: stored ClaudePaneID if still in the window, else first.
	panesOut, err := osExec.CommandContext(ctx, "tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	paneIDs := strings.Split(strings.TrimSpace(string(panesOut)), "\n")
	source := ""
	if task.ClaudePaneID != "" {
		for _, p := range paneIDs {
			if p == task.ClaudePaneID {
				source = p
				break
			}
		}
	}
	if source == "" && len(paneIDs) > 0 {
		source = paneIDs[0]
	}
	if source == "" {
		return "", fmt.Errorf("dock: no panes in window for task %d", task.ID)
	}

	// Join below the TUI pane (vertical split).
	if out, err := osExec.CommandContext(ctx, "tmux", "join-pane", "-v", "-s", source).CombinedOutput(); err != nil {
		return "", fmt.Errorf("dock: join-pane failed: %v: %s", err, string(out))
	}
	// Joined pane is now active.
	joinedOut, err := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	joinedPaneID := strings.TrimSpace(string(joinedOut))

	// Resize TUI pane to leave room for the dock, then set up shift-arrow cycling.
	osExec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y",
		fmt.Sprintf("%d%%", tuiHeightPercent)).Run()
	bindShiftArrowCycle(ctx)
	return joinedPaneID, nil
}

// bindShiftArrowCycle binds Shift+Arrow at the tmux root table to cycle panes,
// matching detail.go's behavior so the user can shift-up back to the board.
func bindShiftArrowCycle(ctx context.Context) {
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Down", "select-pane", "-t", ":.+").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Right", "select-pane", "-t", ":.+").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Up", "select-pane", "-t", ":.-").Run()
	osExec.CommandContext(ctx, "tmux", "bind-key", "-T", "root", "S-Left", "select-pane", "-t", ":.-").Run()
}

func unbindShiftArrowCycle(ctx context.Context) {
	for _, k := range []string{"S-Down", "S-Right", "S-Up", "S-Left"} {
		osExec.CommandContext(ctx, "tmux", "unbind-key", "-T", "root", k).Run()
	}
}
```

Add imports `fmt`.

- [ ] **Step 3: Implement BreakBack** (join the pane back to its daemon window; never kill the executor):

```go
func (c *tmuxPaneController) BreakBack(task *db.Task, paneID string) error {
	if paneID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	unbindShiftArrowCycle(ctx)

	windowTarget := c.findWindow(task)
	if windowTarget == "" {
		// Can't find home window; break to a new window in the daemon session
		// rather than killing the executor.
		_ = osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", paneID).Run()
		return nil
	}
	if err := osExec.CommandContext(ctx, "tmux", "join-pane", "-d", "-s", paneID, "-t", windowTarget).Run(); err != nil {
		// Fall back to break-pane to preserve the running executor.
		_ = osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", paneID).Run()
	}
	return nil
}
```

- [ ] **Step 4: Implement FocusPane / TUIPaneFocused / ResizeTUIFull**:

```go
func (c *tmuxPaneController) FocusPane(paneID string) error {
	return osExec.Command("tmux", "select-pane", "-t", paneID).Run()
}

func (c *tmuxPaneController) TUIPaneFocused() bool {
	// The TUI pane is the one running this process; it is focused when the active
	// pane title/command is the TUI. Simplest reliable check: compare the active
	// pane's id to our own pane id captured at construction is not available here,
	// so use the active pane's current command: the dock's live pane runs the
	// executor (claude/codex/...), the TUI pane runs `ty`. Treat "active pane is
	// NOT the live pane" as TUI-focused.
	out, err := osExec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return true // assume board focus on error (safer: demote)
	}
	active := strings.TrimSpace(string(out))
	return active != c.livePaneID
}

func (c *tmuxPaneController) ResizeTUIFull() {
	osExec.Command("tmux", "resize-pane", "-t", "task-ui:.0", "-y", "100%").Run()
}
```

NOTE: `TUIPaneFocused` references `c.livePaneID`. Add a `livePaneID string` field to `tmuxPaneController`, set it at the end of `JoinBelow` (`c.livePaneID = joinedPaneID`) and clear it in `BreakBack`. Confirm the TUI session/pane target string (`task-ui:.0`) matches what detail.go uses (`detail.go:2276` uses exactly `task-ui:.0`).

- [ ] **Step 5: Build**

Run: `go build ./... 2>&1 | head -20`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/executor_dock_tmux.go
git commit -m "feat(dock): real tmux join/break/focus for live pane (executor only)"
```

### Task 3.4: Wire the real session name + detail-view mutual exclusion

**Files:**
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Pass the real UI session name** — replace the Task 2.5 Step 4 init so the controller knows the UI session for `ResizeTUIFull`/join targeting. Find how detail.go derives `m.uiSessionName` (search `uiSessionName` in detail.go) and reuse the same source when constructing the dock controller. If the value is only known after startup, set it once in the window-size/init handler:

```go
	if m.dock == nil {
		m.dock = NewDockModel(newTmuxPaneController(currentUISessionName()))
	}
```

Where `currentUISessionName()` is the existing helper detail.go uses (reuse it; do not duplicate). If none exists as a standalone function, extract the logic detail.go uses into a small shared helper and call it from both.

- [ ] **Step 2: Demote on leaving the dashboard** — when entering the detail view (the `Enter`/view-detail handler) or quitting, if the dock is live, demote it first so panes don't collide with the detail view's own join. Find the handler that sets `m.currentView = ViewDetail` and add before it:

```go
	if m.dock != nil && m.dock.IsLive() {
		m.dock.Demote(m.kanban.SelectedTask())
	}
```

Also call the same in the quit path (search for where the app tears down / `tea.Quit`).

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./internal/ui/ 2>&1 | tail -10`
Expected: clean, PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/app.go
git commit -m "feat(dock): real UI session wiring + demote before detail view"
```

---

# Phase 4 — QA + Performance

### Task 4.1: Manual QA in an isolated ty instance

Follow the `reference_qa_isolated_ty_tui` runbook (spin up an isolated ty daemon + drive the real TUI via tmux with `--debug-state-file`; do NOT touch the live daemon).

- [ ] **Step 1:** Launch an isolated ty TUI with at least one task whose executor is running and blocked needing input.
- [ ] **Step 2:** Press `tab` → dock opens below the board showing the highlighted task's executor snapshot; board shrinks to make room.
- [ ] **Step 3:** Arrow between tasks → dock snapshot swaps to each highlighted task within ~1s; no visible stall.
- [ ] **Step 4:** Press `shift+↓` → focus drops into a live executor pane; type a character and confirm it reaches the executor.
- [ ] **Step 5:** Press `shift+↑` → focus returns to the board; dock reverts to snapshot within ~200-400ms.
- [ ] **Step 6:** Re-enter the same task with `shift+↓` → promotion is visibly faster than the first (warm path).
- [ ] **Step 7:** Press `tab` again → dock closes; board reclaims full height; executor pane is back in its daemon window (verify with `tmux list-panes`).
- [ ] **Step 8:** Open the detail view (`enter`) while the dock had been live → no pane collision; detail view works normally.
- [ ] **Step 9:** Capture screenshot evidence (per `reference_qa_screenshot_r2_publish` if publishing to a PR).

Record pass/fail for each step. Fix any failures by returning to the relevant Phase 2/3 task.

### Task 4.2: Performance regression — board with dock CLOSED

**Files:**
- Test: `internal/ui/executor_dock_bench_test.go` (create)

- [ ] **Step 1:** Find the existing benchmark harness if any:

Run: `grep -rn "func Benchmark" internal/ui/ | head`

- [ ] **Step 2:** Add a benchmark proving closed-dock navigation does zero tmux work:

```go
func BenchmarkDashboardNav_DockClosed(b *testing.B) {
	m := newBenchAppModel(b) // mirror existing bench/test constructor
	m.width, m.height = 120, 40
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.refreshDockForSelection() // closed -> must be a pure no-op
		_ = m.viewDashboard()
	}
}
```

- [ ] **Step 3:** Run and compare against a baseline rendering bench (dock field present but closed must match pre-feature `viewDashboard` cost within noise):

Run: `go test ./internal/ui/ -bench BenchmarkDashboardNav_DockClosed -benchmem -run x 2>&1 | tail`
Expected: zero allocations attributable to the dock when closed; `refreshDockForSelection` returns immediately.

- [ ] **Step 4:** Assert no captures happen when closed (already covered by `TestDock_RefreshCapturesSelectedTaskWhenOpenSnapshot`, Phase 2). Confirm it passes.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/executor_dock_bench_test.go
git commit -m "test(dock): benchmark proves zero cost when dock closed"
```

### Task 4.3: Performance of the new feature (snapshot + join latency)

Use a real tmux instance (isolated ty). Measure with simple timing wrappers or `time` around the operations.

- [ ] **Step 1: Snapshot latency** — instrument `tmuxPaneController.Capture` (temporary `log.Debug` with elapsed `time.Since`) and confirm a single capture is ≲150ms on a running executor. Confirm rapid arrowing (held arrow key) does not back up: because capture happens on the 750ms tick + one immediate capture per keypress, verify no capture takes >3s (the `CapturePaneContent` timeout) and the UI never blocks (captures run in the `Refresh` path on the update goroutine — if any capture is observed blocking the UI, move `Refresh` into a `tea.Cmd` that returns a `dockSnapshotMsg`; note this as a follow-up only if observed).
- [ ] **Step 2: Cold join latency** — time `JoinBelow` on a task never promoted this session. Target ≲700ms.
- [ ] **Step 3: Warm re-entry latency** — promote task A, demote, promote A again. Time the second `JoinBelow`. Target ≲150ms (window/pane ids resolve fast; styling already applied).
- [ ] **Step 4:** Record the three numbers in the PR description. If cold join exceeds ~1s, profile which tmux call dominates (add per-call `time.Since` logs) and trim (e.g. cache `findWindow` result on the task's `TmuxWindowID`).
- [ ] **Step 5:** Remove temporary timing logs; keep any caching improvements.

```bash
git add -A
git commit -m "perf(dock): verify snapshot/join latency, cache window resolution"
```

### Task 4.4: Final integration pass

- [ ] **Step 1:** `go build ./... && go test ./... 2>&1 | tail -30` — all green.
- [ ] **Step 2:** Run the linter per `reference_ci_lint_gofmt_landmine` (`golangci-lint` pinned version; watch the gofmt 1.25 landmine) and fix findings.
- [ ] **Step 3:** Update the help footer / docs if they enumerate keybindings, to include `tab executor dock` and remove any `tab input` references.
- [ ] **Step 4: Open the PR.**

---

## Self-Review

**Spec coverage:**
- Quick-input removal → Phase 1 (Tasks 1.1-1.5). ✓ (inventory matches the spec's removal section)
- Toggleable dock, stays open until closed → Task 2.1 (`Toggle`), Task 2.5 (key). ✓
- Snapshot follows selection, cheap → Tasks 2.2, 2.5, 2.6. ✓
- shift-arrow promotes to live, fast → Tasks 3.1-3.3. ✓
- shift-up/esc + selection-change demote; one live pane max → Tasks 3.1 (`RefreshOrDemote`), 3.2 (focus poll). ✓
- Physical constraint (region holds one thing) honored by demote-on-different-selection → Task 3.1. ✓
- "Already paid the price" warm re-entry → Task 4.3 Step 3 measures it; relies on tmux/window-id caching. ✓
- Zero cost when closed → Task 2.2 (no-op `Refresh`), Task 2.6 (tick dies), Task 4.2 (bench). ✓
- Performance targets (snapshot ≲150ms, cold join ≲700ms, warm ≲150ms) → Task 4.3. ✓
- QA in isolated ty + screenshots → Task 4.1. ✓
- Perf regression with dock closed → Task 4.2. ✓

**Placeholder scan:** No "TBD"/"handle edge cases" left. Every code step shows code. The two places needing in-repo confirmation (`newTestAppModel`/`newBenchAppModel` helper names; `tea.KeyShiftDown` availability; the `db` import path; `currentUISessionName` source) are flagged with an exact `grep` to resolve, not left vague.

**Type consistency:** `paneController` methods (`Capture`, `JoinBelow`, `BreakBack`, `FocusPane`, `TUIPaneFocused`, `ResizeTUIFull`) are identical across the interface (2.1), the fake (2.1), and the real impl (2.4, 3.3). `DockModel` methods used by app.go (`Toggle`, `IsOpen`, `Height`, `Refresh`, `View`, `Promote`, `Demote`, `RefreshOrDemote`, `IsLive`, `Snapshot`) are all defined. `dockHeightPercent`/`tuiHeightPercentFor` consistent. ✓

**Known risk:** Phase 3's tmux focus tracking (`TUIPaneFocused` by comparing active pane id to `livePaneID`) and the shift-arrow root-binding interplay can only be fully validated against a real tmux server (Task 4.1). The state machine is unit-tested; the tmux glue is QA'd manually. This is called out, not hidden.

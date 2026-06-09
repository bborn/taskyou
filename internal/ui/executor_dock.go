package ui

import (
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/lipgloss"
)

// dockMode is the dock's current display mode.
type dockMode int

const (
	dockSnapshot dockMode = iota // read-only capture-pane text (cheap, follows selection)
	dockLive                     // a real executor pane joined below the TUI (interactive)
)

// dockHeightPercent is the fraction of the terminal height the dock occupies.
const dockHeightPercent = 40

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

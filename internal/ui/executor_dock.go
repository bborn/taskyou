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

// IsLive reports whether a live executor pane is currently joined.
func (d *DockModel) IsLive() bool { return d.mode == dockLive }

// BoardFocused reports whether the board (TUI pane) currently holds focus — i.e.
// the user has shift-up'd out of a live pane. Always true when not live.
func (d *DockModel) BoardFocused() bool {
	if d.mode != dockLive {
		return true
	}
	return d.ctl.TUIPaneFocused()
}

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
		// Different task selected: demote the old live pane back to its daemon
		// window. Resolution is by task id, so a synthetic task carrying the live
		// id is sufficient to target the right window.
		d.Demote(&db.Task{ID: d.liveTaskID})
	}
	d.Refresh(task, termHeight)
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

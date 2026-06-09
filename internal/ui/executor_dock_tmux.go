package ui

import (
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// tmuxPaneController is the real paneController backed by tmux + the executor pkg.
type tmuxPaneController struct {
	uiSessionName string // e.g. "task-ui-<pid>"
	livePaneID    string // the currently-joined live pane id (set by JoinBelow)
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

// The live-pane methods are implemented in Phase 3 (executor_dock_tmux.go grows
// real join/break/focus logic). Stubbed here so the dock compiles in snapshot-only
// builds.
func (c *tmuxPaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	return "", nil
}
func (c *tmuxPaneController) BreakBack(task *db.Task, paneID string) error { return nil }
func (c *tmuxPaneController) FocusPane(paneID string) error               { return nil }
func (c *tmuxPaneController) TUIPaneFocused() bool                        { return true }
func (c *tmuxPaneController) ResizeTUIFull()                              {}

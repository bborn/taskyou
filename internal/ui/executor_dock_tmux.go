package ui

import (
	"context"
	"fmt"
	osExec "os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// tmuxPaneController is the real paneController backed by tmux + the executor pkg.
// It joins a single executor pane below the TUI pane (no shell, unlike the detail
// view) and tracks the live/TUI pane ids so focus can be polled cheaply.
type tmuxPaneController struct {
	uiSessionName string // e.g. "task-ui-<pid>"
	livePaneID    string // the currently-joined live pane id (set by JoinBelow)
	tuiPaneID     string // the TUI pane id captured at join (for resize-back)
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

// findWindow resolves the task's daemon window target ("session:window_id"),
// mirroring detail.go's findTaskWindow: match the task-<id> window in any
// daemon session.
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

// JoinBelow joins the task's executor pane below the (currently active) TUI pane
// and resizes the TUI pane to tuiHeightPercent. Returns the joined pane id.
func (c *tmuxPaneController) JoinBelow(task *db.Task, tuiHeightPercent int) (string, error) {
	if task == nil {
		return "", fmt.Errorf("dock: nil task")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowTarget := c.findWindow(task)
	if windowTarget == "" {
		return "", fmt.Errorf("dock: window not found for task %d", task.ID)
	}

	// Current (TUI) pane id, to refocus/resize afterwards.
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

	c.tuiPaneID = tuiPaneID
	c.livePaneID = joinedPaneID
	return joinedPaneID, nil
}

// BreakBack returns the live executor pane to its daemon window. It never kills
// the executor: if the join fails it breaks the pane to a new daemon window.
func (c *tmuxPaneController) BreakBack(task *db.Task, paneID string) error {
	if paneID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	unbindShiftArrowCycle(ctx)

	windowTarget := c.findWindow(task)
	if windowTarget == "" {
		// Can't find home window; break to a new window in some daemon session
		// rather than killing the running executor.
		_ = osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", paneID).Run()
		c.livePaneID = ""
		return nil
	}
	if err := osExec.CommandContext(ctx, "tmux", "join-pane", "-d", "-s", paneID, "-t", windowTarget).Run(); err != nil {
		// Fall back to break-pane to preserve the running executor.
		_ = osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", paneID).Run()
	}
	c.livePaneID = ""
	return nil
}

// FocusPane gives keyboard focus to the given pane id.
func (c *tmuxPaneController) FocusPane(paneID string) error {
	return osExec.Command("tmux", "select-pane", "-t", paneID).Run()
}

// TUIPaneFocused reports whether the TUI pane currently holds focus. The dock has
// exactly two panes (TUI + live executor); the TUI is focused whenever the active
// pane is not the live pane.
func (c *tmuxPaneController) TUIPaneFocused() bool {
	out, err := osExec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return true // assume board focus on error -> demote (safer than stranding)
	}
	active := strings.TrimSpace(string(out))
	return active != c.livePaneID
}

// ResizeTUIFull resizes the TUI pane back to full height after the live pane
// leaves. Uses the captured TUI pane id; tmux also reflows automatically when a
// pane departs, so this is belt-and-suspenders.
func (c *tmuxPaneController) ResizeTUIFull() {
	if c.tuiPaneID == "" {
		return
	}
	osExec.Command("tmux", "resize-pane", "-t", c.tuiPaneID, "-y", "100%").Run()
}

// bindShiftArrowCycle binds Shift+Arrow at the tmux root table to cycle panes,
// matching detail.go so the user can shift-up back to the board pane.
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

package ui

import (
	"context"
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
//
//	selectedID: task selected in the list (0 if none)
//	selHasExec: whether the selected task has a live executor window to show
//	joinedID:   task whose executor pane is currently joined (0 if none)
//	visible:    whether the preview is enabled (toggled with `P`)
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

// --- shared tmux helpers (package-level; also usable by detail view later) ---

// findTaskWindowTarget returns the "session:windowID" of the daemon window
// running task's executor, or "" if none exists. Updates the stored window ID
// in the DB when found (only when database is non-nil).
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

// --- controller ---

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

// ensureIdentity caches the UI session name and TUI pane id. Call from the main
// loop (not a goroutine); the active pane is the TUI pane because we always
// select-pane back to it after every op.
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

// saveWidth persists the executor pane width to settings if within [10,90].
func (p *listExecutorPane) saveWidth(pct int) {
	if p.database != nil && pct >= 10 && pct <= 90 {
		p.database.SetSetting(config.SettingListExecutorPaneWidth, strconv.Itoa(pct)+"%")
	}
}

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

// breakBack returns the joined executor pane to its daemon window (creating the
// window if tmux destroyed it when the pane was joined out), preserving the
// executor process, and resizes the TUI pane back to full width. Persists the
// width if `save` or the user dragged it. Returns a zeroed joinedState.
func (p *listExecutorPane) breakBack(prev joinedState, save bool) joinedState {
	if prev.paneID == "" {
		p.resizeTuiFull()
		return joinedState{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Persist width: always on explicit teardown, otherwise only if dragged.
	if cur := p.currentWidthPct(prev.paneID); cur > 0 {
		if save {
			p.saveWidth(cur)
		} else if prev.initialWidth > 0 && (cur < prev.initialWidth-2 || cur > prev.initialWidth+2) {
			p.saveWidth(cur)
		}
	}

	// Reset styling/bindings applied at join.
	p.unbindStyleAndBind()

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

	if err := osExec.CommandContext(ctx, "tmux", "join-pane", "-d", "-s", prev.paneID, "-t", windowID).Run(); err != nil {
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

// styleAndBind sets draggable heavy borders + Shift-Left/Right pane focus
// switching (mirrors detail.go). Reset again on breakBack via unbindStyleAndBind.
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

// --- AppModel wiring for the list-view executor preview ---

// listPreviewTickMsg fires after the selection-change debounce; gen guards staleness.
type listPreviewTickMsg struct{ gen int }

// listPreviewUpdatedMsg carries the result of an async preview join/break.
type listPreviewUpdatedMsg struct{ result previewResult }

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
	if m.listPreview == nil || !m.listPreview.ensureIdentity() {
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

// teardownPreviewSync breaks the joined executor pane back to its daemon window
// synchronously (so the executor is re-homed before detail view, board, or
// quit). Safe to call when nothing is joined.
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

package ui

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// The detail view shows a task's executor and shell WITHOUT moving them.
//
// Both panes live in the task's window in the daemon session, and stay there
// for their whole life. Opening a task creates a session grouped with the
// daemon's (it shares the daemon's windows but has its own current window),
// points it at the task's window, and splits a pane under the TUI that runs a
// nested tmux client attached to it. Closing the view kills that pane; tmux
// then disposes of the grouped session. Nothing is borrowed, so nothing has to
// be given back, and nothing can be stranded in the wrong session: a TUI that
// crashes, reloads or is killed leaves every agent exactly where it was.
//
// The nested client works across servers, which is what lets the agent server
// be private even when `ty` runs inside the user's own tmux (see tmuxctl).

// agentTmux runs tmux against the server that holds the agents.
func agentTmux(ctx context.Context, args ...string) *osExec.Cmd { return tmuxctl.Agent(ctx, args...) }

// uiTmux runs tmux against the server the TUI itself sits in.
func uiTmux(ctx context.Context, args ...string) *osExec.Cmd { return tmuxctl.UI(ctx, args...) }

// viewerOption marks a UI pane as one of ty's views, holding the name of the
// session it shows. Cleanup removes only panes carrying it: the TUI may share
// a window with the user's own panes (ty run inside their tmux), and those are
// none of ty's business.
const viewerOption = "@ty_viewer"

// Pane tags on the agent server; see tmuxctl.PaneTaskOption.
const (
	paneTaskOption = tmuxctl.PaneTaskOption
	paneRoleOption = tmuxctl.PaneRoleOption
	paneRoleAgent  = tmuxctl.RoleAgent
	paneRoleShell  = tmuxctl.RoleShell
)

// viewSeq makes every view session name unique, so the cleanup of one view can
// never take down the session a newer view just created.
var viewSeq atomic.Int64

func newViewSessionName() string {
	return fmt.Sprintf("ty-view-%d-%d", os.Getpid(), viewSeq.Add(1))
}

// hiddenShellWindowName is where a hidden shell waits, in the daemon session.
func hiddenShellWindowName(taskID int64) string {
	return fmt.Sprintf("_hidden_shell_%d", taskID)
}

// viewTaskWindow shows the task's daemon window under the TUI. It runs in the
// pane-setup goroutine and reports back through panesJoinedMsg.
func (m *DetailModel) viewTaskWindow() {
	log := GetLogger()
	log.Info("viewTaskWindow: starting for task %d", m.task.ID)
	m.paneAdoptRejected = false

	windowTarget := m.cachedWindowTarget
	daemonSession, windowID, ok := strings.Cut(windowTarget, ":")
	if !ok || daemonSession == "" || windowID == "" {
		log.Warn("viewTaskWindow: no usable window target %q", windowTarget)
		return
	}

	tmuxPaneOpMu.Lock()
	defer tmuxPaneOpMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tuiPaneID := ownPaneID()
	if tuiPaneID == "" {
		log.Error("viewTaskWindow: no $TMUX_PANE; refusing to guess this instance's pane")
		return
	}
	m.tuiPaneID = tuiPaneID
	m.daemonSessionID = daemonSession

	agent, shell, shellInWindow, err := m.taskWindowPanes(ctx, windowTarget)
	if err != nil {
		log.Error("viewTaskWindow: %v", err)
		if m.database != nil && m.task != nil {
			m.database.UpdateTaskWindowID(m.task.ID, "")
			m.task.TmuxWindowID = ""
		}
		m.joinPaneFailedUntil = time.Now().Add(5 * time.Second)
		return
	}

	// Ownership guard: tmux recycles pane IDs, and a window named task-<id> can
	// end up holding another task's pane, so "it is in the window" is not proof
	// the pane is this task's. Showing a foreign agent is how 4324 once ended up
	// typing into 4822's session.
	if !m.paneCwdInWorktree(ctx, agent) {
		log.Error("viewTaskWindow: pane %q is not in task %d's worktree %q; refusing to show it",
			agent, m.task.ID, m.task.WorktreePath)
		if m.database != nil {
			m.database.ClearTaskTmuxIDs(m.task.ID)
		}
		m.task.ClaudePaneID, m.task.ShellPaneID, m.task.TmuxWindowID = "", "", ""
		m.cachedWindowTarget = ""
		m.joinPaneFailedUntil = time.Now().Add(5 * time.Second)
		m.paneAdoptRejected = true
		return
	}

	m.claudePaneID = agent
	m.workdirPaneID = m.applyShellVisibility(ctx, daemonSession, agent, shell, shellInWindow)
	m.tagTaskPanes(ctx)
	if m.database != nil {
		if err := m.database.UpdateTaskPaneIDs(m.task.ID, m.claudePaneID, m.workdirPaneID); err == nil {
			m.task.ClaudePaneID, m.task.ShellPaneID = m.claudePaneID, m.workdirPaneID
		}
	}
	claudeTitle := m.executorDisplayName()
	if m.claudeMemoryMB > 0 {
		claudeTitle = fmt.Sprintf("%s (%d MB)", claudeTitle, m.claudeMemoryMB)
	}
	agentTmux(ctx, "select-pane", "-t", agent, "-T", claudeTitle).Run()

	view, err := m.ensureViewSession(ctx, daemonSession, windowID)
	if err != nil {
		log.Error("viewTaskWindow: view session: %v", err)
		m.joinPaneFailedUntil = time.Now().Add(5 * time.Second)
		return
	}
	viewer, err := m.openViewerPane(ctx, tuiPaneID, view)
	if err != nil {
		log.Error("viewTaskWindow: viewer pane: %v", err)
		agentTmux(ctx, "kill-session", "-t", "="+view).Run()
		m.joinPaneFailedUntil = time.Now().Add(5 * time.Second)
		return
	}
	m.viewSession, m.viewerPaneID = view, viewer

	m.styleDetailLayout(ctx, tuiPaneID)

	m.initialDetailHeight = m.getCurrentDetailPaneHeight(tuiPaneID)
	m.initialShellWidth = m.getCurrentShellPaneWidth()
	if actualHeight := m.getActualPaneHeight(tuiPaneID); actualHeight > 0 {
		m.height = actualHeight
		if m.ready {
			if vpHeight := m.height - m.headerHeight() - m.footerHeight(); vpHeight > 0 {
				m.viewport.Height = vpHeight
				m.setViewportContent()
			}
		}
	}

	log.Info("viewTaskWindow: task %d shown in %s via %s (agent %s, shell %s)",
		m.task.ID, viewer, view, m.claudePaneID, m.workdirPaneID)

	if m.focusExecutorOnJoin {
		m.focusExecutorPane()
	}
}

// taskWindowPanes finds the task's agent and shell panes. The shell may be in
// the window, parked in its hidden window, or not exist yet.
func (m *DetailModel) taskWindowPanes(ctx context.Context, windowTarget string) (agent, shell string, shellInWindow bool, err error) {
	out, err := agentTmux(ctx, "list-panes", "-t", windowTarget,
		"-F", "#{pane_id} #{"+paneRoleOption+"}").Output()
	if err != nil {
		return "", "", false, fmt.Errorf("list panes in %s: %w", windowTarget, err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, role, _ := strings.Cut(strings.TrimSpace(line), " ")
		if id == "" {
			continue
		}
		ids = append(ids, id)
		switch {
		case role == paneRoleAgent && agent == "":
			agent = id
		case role == paneRoleShell && shell == "":
			shell = id
		}
	}
	if len(ids) == 0 {
		return "", "", false, fmt.Errorf("no panes in %s", windowTarget)
	}
	contains := func(id string) bool {
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}
	// Untagged windows (made before tags existed): the stored IDs, else the
	// window's first pane is the agent and the other is the shell.
	if agent == "" {
		if stored := m.task.ClaudePaneID; stored != "" && contains(stored) {
			agent = stored
		} else {
			agent = ids[0]
		}
	}
	if shell == "" || shell == agent {
		shell = ""
		if stored := m.task.ShellPaneID; stored != "" && stored != agent && contains(stored) {
			shell = stored
		} else {
			for _, id := range ids {
				if id != agent {
					shell = id
					break
				}
			}
		}
	}
	if shell != "" {
		return agent, shell, true, nil
	}
	// A hidden shell sits in its own window in the daemon session. Accept the
	// stored ID only if that is where it still is: IDs are reused.
	if stored := m.task.ShellPaneID; stored != "" {
		if out, err := agentTmux(ctx, "display-message", "-t", stored, "-p", "#{window_name}").Output(); err == nil &&
			strings.TrimSpace(string(out)) == hiddenShellWindowName(m.task.ID) {
			return agent, stored, false, nil
		}
	}
	return agent, "", false, nil
}

// applyShellVisibility makes the daemon window match the shell preference, and
// returns the shell pane ("" if there is none). Every move stays inside the
// daemon session.
func (m *DetailModel) applyShellVisibility(ctx context.Context, daemonSession, agent, shell string, shellInWindow bool) string {
	log := GetLogger()
	if m.shellPaneHidden {
		if shell != "" && shellInWindow {
			if err := agentTmux(ctx, "break-pane", "-d", "-s", shell,
				"-n", hiddenShellWindowName(m.task.ID), "-t", daemonSession+":").Run(); err != nil {
				log.Error("applyShellVisibility: hide shell %s: %v", shell, err)
			}
		}
		return shell
	}
	width := m.getShellPaneWidth()
	if shell != "" && !shellInWindow {
		if err := agentTmux(ctx, "join-pane", "-d", "-h", "-l", width, "-s", shell, "-t", agent).Run(); err != nil {
			log.Error("applyShellVisibility: bring back shell %s: %v; starting a new one", shell, err)
			shell = ""
		}
	}
	if shell == "" {
		shell = m.splitShellPane(ctx, agent, width)
	}
	return shell
}

// splitShellPane starts a shell beside the agent, in the task's workdir, with
// the task's environment.
func (m *DetailModel) splitShellPane(ctx context.Context, agent, width string) string {
	userShell := os.Getenv("SHELL")
	if userShell == "" {
		userShell = "/bin/zsh"
	}
	out, err := agentTmux(ctx, "split-window", "-d", "-h", "-l", width,
		"-P", "-F", "#{pane_id}", "-t", agent, "-c", m.getWorkdir(), userShell).Output()
	if err != nil {
		GetLogger().Error("splitShellPane: %v", err)
		return ""
	}
	shell := strings.TrimSpace(string(out))
	if shell == "" {
		return ""
	}
	envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
	agentTmux(ctx, "send-keys", "-t", shell, envCmd, "Enter").Run()
	agentTmux(ctx, "send-keys", "-t", shell, "clear", "Enter").Run()
	return shell
}

// tagTaskPanes labels the task's panes on the agent server, and titles them.
func (m *DetailModel) tagTaskPanes(ctx context.Context) {
	id := strconv.FormatInt(m.task.ID, 10)
	var cmds [][]string
	if m.claudePaneID != "" {
		cmds = append(cmds,
			[]string{"set-option", "-p", "-t", m.claudePaneID, paneTaskOption, id},
			[]string{"set-option", "-p", "-t", m.claudePaneID, paneRoleOption, paneRoleAgent})
	}
	if m.workdirPaneID != "" {
		cmds = append(cmds,
			[]string{"set-option", "-p", "-t", m.workdirPaneID, paneTaskOption, id},
			[]string{"set-option", "-p", "-t", m.workdirPaneID, paneRoleOption, paneRoleShell},
			[]string{"select-pane", "-t", m.workdirPaneID, "-T", "Shell"})
	}
	runTmuxBatchOn(ctx, agentTmux, cmds)
}

// ensureViewSession creates a session grouped with the daemon's and points it
// at the task's window.
func (m *DetailModel) ensureViewSession(ctx context.Context, daemonSession, windowID string) (string, error) {
	view := newViewSessionName()
	if out, err := agentTmux(ctx, "new-session", "-d", "-s", view, "-t", daemonSession).CombinedOutput(); err != nil {
		return "", fmt.Errorf("new-session -t %s: %v: %s", daemonSession, err, strings.TrimSpace(string(out)))
	}
	if err := agentTmux(ctx, "select-window", "-t", view+":"+windowID).Run(); err != nil {
		agentTmux(ctx, "kill-session", "-t", "="+view).Run()
		return "", fmt.Errorf("select %s in %s: %w", windowID, view, err)
	}
	runTmuxBatchOn(ctx, agentTmux, [][]string{
		// A view, not a workspace: no status line, and no prefix — every key goes
		// to the agent. The mouse selects, scrolls and resizes as usual.
		{"set-option", "-t", view, "status", "off"},
		{"set-option", "-t", view, "mouse", "on"},
		{"set-option", "-t", view, "prefix", "None"},
		{"set-option", "-t", view, "prefix2", "None"},
		// If the task's window closes, tmux would move this session to some
		// other window, and the pane under the TUI would quietly start showing a
		// different task's agent. End the view instead.
		{"set-hook", "-t", view, "session-window-changed", "kill-session -t '=" + view + "'"},
		// Size follows whoever is looking; titles name the agent and the shell.
		{"set-option", "-w", "-t", windowID, "window-size", "latest"},
		{"set-option", "-w", "-t", windowID, "pane-border-status", "top"},
		{"set-option", "-w", "-t", windowID, "pane-border-format", " #{pane_title} "},
	})
	return view, nil
}

// openViewerPane splits a pane under the TUI that attaches to view.
func (m *DetailModel) openViewerPane(ctx context.Context, tuiPaneID, view string) (string, error) {
	removeStaleViewers(ctx, tuiPaneID)
	out, err := uiTmux(ctx, "split-window", "-v", "-d", "-t", tuiPaneID,
		"-P", "-F", "#{pane_id}", tmuxctl.ViewAttachScript(view)).Output()
	if err != nil {
		return "", fmt.Errorf("split-window: %w", err)
	}
	viewer := strings.TrimSpace(string(out))
	if viewer == "" {
		return "", fmt.Errorf("split-window printed no pane id")
	}
	uiTmux(ctx, "set-option", "-p", "-t", viewer, viewerOption, view).Run()
	return viewer, nil
}

// paneExists reports whether pane is still there. The exit status alone cannot
// say: display-message may fail to find its target, and then answers for some
// other pane and succeeds. So the answer has to name the pane asked about.
func paneExists(ctx context.Context, tmux func(context.Context, ...string) *osExec.Cmd, pane string) bool {
	if pane == "" {
		return false
	}
	out, err := tmux(ctx, "display-message", "-t", pane, "-p", "#{pane_id}").Output()
	return err == nil && strings.TrimSpace(string(out)) == pane
}

// removeStaleViewers kills view panes a previous view left in the TUI's
// window, and nothing else.
func removeStaleViewers(ctx context.Context, tuiPaneID string) {
	out, err := uiTmux(ctx, "list-panes", "-t", tuiPaneID, "-F", "#{pane_id} #{"+viewerOption+"}").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, marker, _ := strings.Cut(strings.TrimSpace(line), " ")
		if id != "" && id != tuiPaneID && marker != "" {
			uiTmux(ctx, "kill-pane", "-t", id).Run()
		}
	}
}

// styleDetailLayout sets the detail view's chrome on the UI server: the TUI's
// title and share of the window, the status line, borders and dimming.
func (m *DetailModel) styleDetailLayout(ctx context.Context, tuiPaneID string) {
	if m.uiSessionName == "" {
		m.uiSessionName = ownSessionName(ctx)
	}
	s := m.uiSessionName
	runTmuxBatchOn(ctx, uiTmux, [][]string{
		{"select-pane", "-t", tuiPaneID, "-T", m.getPaneTitle()},
		{"select-pane", "-t", tuiPaneID},
		{"set-option", "-t", s, "status", "on"},
		{"set-option", "-t", s, "status-style", "bg=#3b82f6,fg=white"},
		{"set-option", "-t", s, "status-left", " TASK UI "},
		{"set-option", "-t", s, "status-right", " drag borders to resize "},
		{"set-option", "-t", s, "status-right-length", "80"},
		{"set-option", "-t", s, "pane-border-lines", "heavy"},
		{"set-option", "-t", s, "pane-border-indicators", "arrows"},
		{"set-option", "-t", s, "pane-border-style", "fg=#374151"},
		{"set-option", "-t", s, "pane-active-border-style", "fg=#61AFEF"},
		{"set-option", "-t", s, "window-style", "fg=#6b7280"},
		{"set-option", "-t", s, "window-active-style", "fg=terminal"},
		{"resize-pane", "-t", tuiPaneID, "-y", m.getDetailPaneHeight()},
	})
	m.focused = true
	m.bindPaneNavigation(ctx)
}

// closeTaskWindowView ends the view. The agent and shell are not touched: they
// never left the daemon session. With saveLayout the TUI's height and the
// shell's width become the new defaults; otherwise they are saved only if the
// user resized them.
func (m *DetailModel) closeTaskWindowView(saveLayout bool) {
	if m.viewerPaneID == "" && m.viewSession == "" && m.claudePaneID == "" {
		return
	}
	tmuxPaneOpMu.Lock()
	defer tmuxPaneOpMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.saveLayout(ctx, saveLayout)
	if m.viewerPaneID != "" {
		uiTmux(ctx, "kill-pane", "-t", m.viewerPaneID).Run()
	}
	if m.viewSession != "" {
		agentTmux(ctx, "kill-session", "-t", "="+m.viewSession).Run()
	}
	GetLogger().Info("closeTaskWindowView: closed %s (%s) for task %d", m.viewerPaneID, m.viewSession, m.task.ID)
	m.viewerPaneID, m.viewSession = "", ""
	m.claudePaneID, m.workdirPaneID, m.daemonSessionID = "", "", ""
}

// saveLayout records the TUI's height and the shell's width as preferences:
// always when force is set, otherwise only if the user resized them.
func (m *DetailModel) saveLayout(ctx context.Context, force bool) {
	if m.database == nil {
		return
	}
	if height := m.getCurrentDetailPaneHeight(m.tuiPaneID); height >= 1 && height <= 50 {
		changed := m.initialDetailHeight > 0 && (height < m.initialDetailHeight-2 || height > m.initialDetailHeight+2)
		if force || changed {
			m.database.SetSetting(config.SettingDetailPaneHeight, fmt.Sprintf("%d%%", height))
		}
	}
	if width := m.getCurrentShellPaneWidth(); width >= 10 && width <= 90 {
		changed := m.initialShellWidth > 0 && (width < m.initialShellWidth-2 || width > m.initialShellWidth+2)
		if force || changed {
			m.database.SetSetting(config.SettingShellPaneWidth, fmt.Sprintf("%d%%", width))
		}
	}
}

// hideShellPane parks the shell in its hidden window in the daemon session.
// Its process keeps running.
func (m *DetailModel) hideShellPane(ctx context.Context) {
	log := GetLogger()
	if m.workdirPaneID == "" || m.daemonSessionID == "" {
		m.shellPaneHidden = true
		return
	}
	m.saveShellPaneWidth()
	if err := agentTmux(ctx, "break-pane", "-d", "-s", m.workdirPaneID,
		"-n", hiddenShellWindowName(m.task.ID), "-t", m.daemonSessionID+":").Run(); err != nil {
		log.Error("hideShellPane: break-pane %s: %v", m.workdirPaneID, err)
		return
	}
	m.shellPaneHidden = true
	log.Info("hideShellPane: shell %s parked in %s", m.workdirPaneID, hiddenShellWindowName(m.task.ID))
}

// showShellPane brings the shell back beside the agent, or starts one.
func (m *DetailModel) showShellPane(ctx context.Context) {
	if m.claudePaneID == "" {
		return
	}
	width := m.getShellPaneWidth()
	if m.workdirPaneID != "" {
		if err := agentTmux(ctx, "join-pane", "-d", "-h", "-l", width, "-s", m.workdirPaneID, "-t", m.claudePaneID).Run(); err != nil {
			GetLogger().Error("showShellPane: join-pane %s: %v; starting a new shell", m.workdirPaneID, err)
			m.workdirPaneID = ""
		}
	}
	if m.workdirPaneID == "" {
		m.workdirPaneID = m.splitShellPane(ctx, m.claudePaneID, width)
		m.tagTaskPanes(ctx)
	}
	m.shellPaneHidden = false
	if m.database != nil && m.task != nil {
		m.database.UpdateTaskPaneIDs(m.task.ID, m.claudePaneID, m.workdirPaneID)
	}
}

// focusExecutorPane moves the keyboard to the executor: the viewer pane on
// the UI server, and the agent pane inside the view.
func (m *DetailModel) focusExecutorPane() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if m.remotePaneID != "" {
		if uiTmux(ctx, "select-pane", "-t", m.remotePaneID).Run() == nil {
			m.focused = false
		}
		return
	}
	if m.viewerPaneID == "" || m.claudePaneID == "" {
		return
	}
	agentTmux(ctx, "select-pane", "-t", m.claudePaneID).Run()
	if uiTmux(ctx, "select-pane", "-t", m.viewerPaneID).Run() == nil {
		m.focused = false
	}
}

// tmuxCmdSep separates two commands inside a single tmux command argument (e.g.
// the body of a bind-key). It is the two characters backslash-semicolon: tmux
// strips the backslash and treats the ";" as part of the command being defined.
// A bare ";" would instead terminate the tmux command line itself.
const tmuxCmdSep = `\;`

// bindPaneNavigation installs Shift+arrow navigation on the UI server.
// Up/Down move between the TUI and the pane under it. Left/Right move between
// the agent and the shell inside a view (or cycle panes anywhere else).
func (m *DetailModel) bindPaneNavigation(ctx context.Context) {
	inner := func(dir string) string {
		return fmt.Sprintf(`run-shell -b "%s select-pane -t '#{%s}:%s'"`, tmuxctl.AgentShell(), viewerOption, dir)
	}
	// One invocation per binding, not a batch: each bind-key's body carries its
	// own escaped separator, and keeping them apart keeps that easy to see.
	for _, bind := range [][]string{
		{"bind-key", "-T", "root", "S-Down", "select-pane", "-t", ":.+"},
		{"bind-key", "-T", "root", "S-Up", "select-pane", "-t", ":.-"},
		{"bind-key", "-T", "root", "S-Right", "if-shell", "-F", "#{" + viewerOption + "}", inner(".+"), "select-pane -t :.+"},
		{"bind-key", "-T", "root", "S-Left", "if-shell", "-F", "#{" + viewerOption + "}", inner(".-"), "select-pane -t :.-"},
		// Forward an internal navigation key to the TUI. The task loader focuses
		// the executor after pane setup completes, including slow SSH attachments.
		//
		// The separator MUST be the escaped tmuxCmdSep. A bare ";" is eaten by
		// tmux's own argv parser as a top-level command separator, so this reads
		// as TWO commands: it binds only the select-pane half and RUNS the
		// send-keys immediately — typing C-Up/C-Down into the TUI on every task
		// open, which the detail view treats as prev/next task.
		{"bind-key", "-T", "root", "M-S-Up", "select-pane", "-t", ":.0", tmuxCmdSep, "send-keys", "-t", ":.0", "C-Up"},
		{"bind-key", "-T", "root", "M-S-Down", "select-pane", "-t", ":.0", tmuxCmdSep, "send-keys", "-t", ":.0", "C-Down"},
	} {
		uiTmux(ctx, bind...).Run()
	}
}

// getCurrentShellPaneWidth returns the shell's share of the agent+shell width
// as a percentage, or 0 when there is no shell beside the agent.
func (m *DetailModel) getCurrentShellPaneWidth() int {
	if m.workdirPaneID == "" || m.claudePaneID == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	width := func(pane string) int {
		out, err := agentTmux(ctx, "display-message", "-p", "-t", pane, "#{window_id} #{pane_width}").Output()
		if err != nil {
			return 0
		}
		_, w, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		n, _ := strconv.Atoi(w)
		return n
	}
	// Both must be in the same window, or the shell is hidden and there is no
	// split to measure.
	sameWindow := func() bool {
		a, errA := agentTmux(ctx, "display-message", "-p", "-t", m.claudePaneID, "#{window_id}").Output()
		s, errS := agentTmux(ctx, "display-message", "-p", "-t", m.workdirPaneID, "#{window_id}").Output()
		return errA == nil && errS == nil && strings.TrimSpace(string(a)) == strings.TrimSpace(string(s))
	}
	if !sameWindow() {
		return 0
	}
	shellWidth, agentWidth := width(m.workdirPaneID), width(m.claudePaneID)
	if shellWidth <= 0 || agentWidth <= 0 {
		return 0
	}
	total := shellWidth + agentWidth
	return (shellWidth*100 + total/2) / total
}

// saveShellPaneWidth records the shell's current width as the preference.
func (m *DetailModel) saveShellPaneWidth() {
	if m.database == nil {
		return
	}
	if pct := m.getCurrentShellPaneWidth(); pct >= 10 && pct <= 90 {
		m.database.SetSetting(config.SettingShellPaneWidth, fmt.Sprintf("%d%%", pct))
	}
}

// killPaneWithProcess kills a UI pane and the process in it (remote attach and
// remote shell panes, which hold ssh clients this view started).
func (m *DetailModel) killPaneWithProcess(ctx context.Context, paneID string) {
	if paneID == "" {
		return
	}
	if out, err := uiTmux(ctx, "display-message", "-t", paneID, "-p", "#{pane_pid}").Output(); err == nil {
		if pid := strings.TrimSpace(string(out)); pid != "" {
			osExec.CommandContext(ctx, "kill", "-9", pid).Run()
		}
	}
	uiTmux(ctx, "kill-pane", "-t", paneID).Run()
}

// runTmuxBatch runs several tmux commands on the UI server; see runTmuxBatchOn.
func runTmuxBatch(ctx context.Context, commands [][]string) {
	runTmuxBatchOn(ctx, uiTmux, commands)
}

// runTmuxBatchOn runs several tmux commands as one invocation on the given
// server, falling back to one call each if the batch fails. tmux accepts ";"
// as a separate argv entry; no shell is involved.
func runTmuxBatchOn(ctx context.Context, tmux func(context.Context, ...string) *osExec.Cmd, commands [][]string) {
	var args []string
	for _, command := range commands {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, command...)
	}
	if len(args) == 0 {
		return
	}
	if tmux(ctx, args...).Run() != nil {
		for _, command := range commands {
			if ctx.Err() != nil {
				return
			}
			tmux(ctx, command...).Run()
		}
	}
}

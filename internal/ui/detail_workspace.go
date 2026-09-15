package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// OpenWorkspace places a disposable resource viewer beside the agent. Shell
// processes stay on the daemon server; only the local viewer belongs to the UI.
func (m *DetailModel) OpenWorkspace() tea.Cmd {
	if m.task == nil || m.paneLoading {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if m.workspacePaneID != "" && paneExists(ctx, uiTmux, m.workspacePaneID) {
		_ = uiTmux(ctx, "select-pane", "-t", m.workspacePaneID).Run()
		return nil
	}
	target := m.viewerPaneID
	if m.remotePaneID != "" {
		target = m.remotePaneID
	}
	if target == "" {
		target = ownPaneID()
	}
	if target == "" {
		m.paneError = "Open TaskYou inside tmux to use the workspace"
		m.setViewportContent()
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		m.paneError = err.Error()
		return nil
	}
	// Quote arguments as shell words; database paths and executable paths can
	// contain spaces. None of the task's resource data becomes executable code.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "WORKTREE_DB_PATH=" + quote(m.database.Path()) + " " + quote(binary) + " panel view " + strconv.FormatInt(m.task.ID, 10)
	out, err := uiTmux(ctx, "split-window", "-h", "-d", "-t", target, "-l", "45%", "-P", "-F", "#{pane_id}", diesWithTUI(script)).Output()
	if err != nil {
		m.paneError = fmt.Sprintf("Open workspace: %v", err)
		m.setViewportContent()
		return nil
	}
	m.workspacePaneID = strings.TrimSpace(string(out))
	if m.workspacePaneID == "" {
		return nil
	}
	_ = uiTmux(ctx, "set-option", "-p", "-t", m.workspacePaneID, viewerOption, "workspace").Run()
	_ = uiTmux(ctx, "select-pane", "-t", m.workspacePaneID, "-T", "Workspace").Run()
	// Park the original shell only once the replacement view was created.
	m.workspaceRestoreShell = !m.shellPaneHidden
	if m.remotePaneID != "" {
		if m.remoteShellPaneID != "" {
			_ = uiTmux(ctx, "kill-pane", "-t", m.remoteShellPaneID).Run()
			m.remoteShellPaneID = ""
		}
	} else if m.claudePaneID != "" {
		m.hideShellPane(ctx)
	}
	_ = uiTmux(ctx, "select-pane", "-t", m.workspacePaneID).Run()
	m.setViewportContent()
	return nil
}
func (m *DetailModel) closeWorkspace(ctx context.Context) {
	if m.workspacePaneID == "" {
		return
	}
	_ = uiTmux(ctx, "kill-pane", "-t", m.workspacePaneID).Run()
	m.workspacePaneID = ""
	if m.workspaceRestoreShell && m.remotePaneID == "" && m.claudePaneID != "" {
		m.showShellPane(ctx)
	}
	if m.workspaceRestoreShell && m.remotePaneID != "" {
		if loc, ok := m.remoteTaskLocation(); ok {
			if err := m.showRemoteShellPane(ctx, loc); err != nil {
				m.paneError = err.Error()
			}
		}
	}
	m.workspaceRestoreShell = false
}

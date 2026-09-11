package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/executor"
)

// showRemoteShellPane is called while holding tmuxPaneOpMu, just like a local
// join. Only the SSH view is local; shell discovery and creation live in core.
func (m *DetailModel) showRemoteShellPane(ctx context.Context, loc executor.RemoteTaskLocation) error {
	if m.remotePaneID == "" {
		return fmt.Errorf("open the remote executor session before opening its shell")
	}
	if m.remoteShellPaneID != "" {
		return nil
	}
	if _, err := executor.InspectRemoteTerminal(ctx, m.task, loc.WorkDir, true); err != nil {
		return err
	}
	out, err := uiTmux(ctx, "split-window", "-h", "-d",
		"-l", m.getShellPaneWidth(), "-t", m.remotePaneID, "-P", "-F", "#{pane_id}",
		executor.RemoteShellAttachScript(m.task, loc)).Output()
	if err != nil {
		return fmt.Errorf("could not open remote shell view: %w", err)
	}
	m.remoteShellPaneID = strings.TrimSpace(string(out))
	if m.remoteShellPaneID == "" {
		return fmt.Errorf("remote shell view returned no pane ID")
	}
	_ = uiTmux(ctx, "select-pane", "-t", m.remoteShellPaneID, "-T", "Shell — "+loc.Host).Run()
	return nil
}

type remoteShellToggledMsg struct {
	err    error
	hidden bool
}

func (m *DetailModel) toggleRemoteShellPane(loc executor.RemoteTaskLocation) error {
	tmuxPaneOpMu.Lock()
	defer tmuxPaneOpMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if m.remoteShellPaneID == "" {
		if err := m.showRemoteShellPane(ctx, loc); err != nil {
			return err
		}
	} else {
		// Detach the view without killing the shell process on the host.
		if err := uiTmux(ctx, "kill-pane", "-t", m.remoteShellPaneID).Run(); err != nil {
			return fmt.Errorf("could not hide remote shell: %w", err)
		}
		m.remoteShellPaneID = ""
	}
	return m.database.SetSetting(config.SettingShellPaneHidden, fmt.Sprint(m.remoteShellPaneID == ""))
}

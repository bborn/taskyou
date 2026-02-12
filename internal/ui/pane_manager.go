package ui

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// CreateNewShellPane creates a new shell pane for the current task.
// It splits from the Claude pane and returns the new pane ID.
func (m *DetailModel) CreateNewShellPane() (string, error) {
	log := GetLogger()
	if m.claudePaneID == "" {
		return "", fmt.Errorf("no Claude pane available to split from")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workdir := m.getWorkdir()
	userShell := os.Getenv("SHELL")
	if userShell == "" {
		userShell = "/bin/zsh"
	}

	shellWidth := m.getShellPaneWidth()

	log.Info("CreateNewShellPane: creating new shell pane, workdir=%q", workdir)
	err := osExec.CommandContext(ctx, "tmux", "split-window",
		"-h", "-l", shellWidth,
		"-t", m.claudePaneID,
		"-c", workdir,
		userShell,
	).Run()
	if err != nil {
		log.Error("CreateNewShellPane: split-window failed: %v", err)
		return "", fmt.Errorf("failed to create shell pane: %w", err)
	}

	// Get the new pane ID
	paneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	paneOut, err := paneCmd.Output()
	if err != nil {
		log.Error("CreateNewShellPane: failed to get pane ID: %v", err)
		return "", fmt.Errorf("failed to get pane ID: %w", err)
	}
	paneID := strings.TrimSpace(string(paneOut))

	// Set pane title
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", paneID, "-T", "Shell").Run()

	// Set environment variables
	if m.task != nil {
		envCmd := fmt.Sprintf("export WORKTREE_TASK_ID=%d WORKTREE_PORT=%d WORKTREE_PATH=%q", m.task.ID, m.task.Port, m.task.WorktreePath)
		osExec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, envCmd, "Enter").Run()
		osExec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, "clear", "Enter").Run()
	}

	// Store in database
	if m.database != nil && m.task != nil {
		pane := &db.TaskPane{
			TaskID:   m.task.ID,
			PaneID:   paneID,
			PaneType: db.PaneTypeShellExtra,
			Title:    "Shell",
		}
		if err := m.database.CreateTaskPane(pane); err != nil {
			log.Error("CreateNewShellPane: failed to save pane to db: %v", err)
		} else {
			log.Info("CreateNewShellPane: saved pane %q to database", paneID)
		}
	}

	// Select back to TUI pane
	if m.tuiPaneID != "" {
		osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
	}

	log.Info("CreateNewShellPane: created pane %q", paneID)
	return paneID, nil
}

// CreateNewClaudePane creates a new Claude pane for the current task.
// It splits from the existing Claude pane and starts a new Claude session.
func (m *DetailModel) CreateNewClaudePane() (string, error) {
	log := GetLogger()
	if m.claudePaneID == "" {
		return "", fmt.Errorf("no Claude pane available to split from")
	}

	if m.task == nil {
		return "", fmt.Errorf("no task available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workdir := m.getWorkdir()

	log.Info("CreateNewClaudePane: creating new Claude pane, workdir=%q", workdir)

	// Split vertically to create a new pane
	err := osExec.CommandContext(ctx, "tmux", "split-window",
		"-v", // vertical split
		"-t", m.claudePaneID,
		"-c", workdir,
		"sleep", "0.1", // temporary command to prevent immediate close
	).Run()
	if err != nil {
		log.Error("CreateNewClaudePane: split-window failed: %v", err)
		return "", fmt.Errorf("failed to create Claude pane: %w", err)
	}

	// Get the new pane ID
	paneCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	paneOut, err := paneCmd.Output()
	if err != nil {
		log.Error("CreateNewClaudePane: failed to get pane ID: %v", err)
		return "", fmt.Errorf("failed to get pane ID: %w", err)
	}
	paneID := strings.TrimSpace(string(paneOut))

	// Set pane title
	osExec.CommandContext(ctx, "tmux", "select-pane", "-t", paneID, "-T", "Claude").Run()

	// Start Claude in the new pane
	claudeCmd := m.buildClaudeCommand()
	osExec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, claudeCmd, "Enter").Run()

	// Store in database
	if m.database != nil {
		pane := &db.TaskPane{
			TaskID:   m.task.ID,
			PaneID:   paneID,
			PaneType: db.PaneTypeClaudeExtra,
			Title:    "Claude",
		}
		if err := m.database.CreateTaskPane(pane); err != nil {
			log.Error("CreateNewClaudePane: failed to save pane to db: %v", err)
		} else {
			log.Info("CreateNewClaudePane: saved pane %q to database", paneID)
		}
	}

	// Select back to TUI pane
	if m.tuiPaneID != "" {
		osExec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
	}

	log.Info("CreateNewClaudePane: created pane %q", paneID)
	return paneID, nil
}

// buildClaudeCommand constructs the Claude command to run in a pane.
func (m *DetailModel) buildClaudeCommand() string {
	// Build the same command that the executor would use
	workdir := m.getWorkdir()
	cmd := fmt.Sprintf("cd %q && claude", workdir)
	return cmd
}

// GetAllTaskPanes returns all pane IDs for the current task (including primary panes).
func (m *DetailModel) GetAllTaskPanes() []string {
	var panes []string

	if m.claudePaneID != "" {
		panes = append(panes, m.claudePaneID)
	}
	if m.workdirPaneID != "" {
		panes = append(panes, m.workdirPaneID)
	}

	// Get additional panes from database
	if m.database != nil && m.task != nil {
		dbPanes, err := m.database.GetTaskPanes(m.task.ID)
		if err == nil {
			for _, p := range dbPanes {
				// Only include extra panes (not primary ones already tracked)
				if p.PaneType == db.PaneTypeClaudeExtra || p.PaneType == db.PaneTypeShellExtra {
					panes = append(panes, p.PaneID)
				}
			}
		}
	}

	return panes
}

// breakExtraPanes breaks all extra panes (not the primary Claude and Shell panes).
// This is called during Cleanup to ensure all additional panes are broken.
func (m *DetailModel) breakExtraPanes() {
	log := GetLogger()
	if m.task == nil {
		return
	}

	if m.database == nil {
		return
	}

	// Get extra panes from database
	panes, err := m.database.GetTaskPanes(m.task.ID)
	if err != nil {
		log.Error("breakExtraPanes: failed to get task panes: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, pane := range panes {
		// Only break extra panes (not primary ones)
		if pane.PaneType != db.PaneTypeClaudeExtra && pane.PaneType != db.PaneTypeShellExtra {
			continue
		}

		// Check if pane exists
		checkCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-t", pane.PaneID, "-p", "#{pane_id}")
		if _, err := checkCmd.Output(); err != nil {
			log.Debug("breakExtraPanes: pane %q doesn't exist, skipping", pane.PaneID)
			continue
		}

		// Break the pane
		breakCmd := osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", pane.PaneID)
		if err := breakCmd.Run(); err != nil {
			log.Error("breakExtraPanes: failed to break pane %q: %v", pane.PaneID, err)
		} else {
			log.Debug("breakExtraPanes: broke pane %q", pane.PaneID)
		}
	}
}

// CleanupAllPanes breaks all panes associated with this task.
func (m *DetailModel) CleanupAllPanes(saveHeight bool) {
	log := GetLogger()
	if m.task == nil {
		return
	}

	log.Info("CleanupAllPanes: breaking all panes for task %d", m.task.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get all panes
	allPanes := m.GetAllTaskPanes()

	for _, paneID := range allPanes {
		if paneID == "" {
			continue
		}

		// Check if pane exists
		checkCmd := osExec.CommandContext(ctx, "tmux", "display-message", "-t", paneID, "-p", "#{pane_id}")
		if _, err := checkCmd.Output(); err != nil {
			log.Debug("CleanupAllPanes: pane %q doesn't exist, skipping", paneID)
			continue
		}

		// Break the pane
		breakCmd := osExec.CommandContext(ctx, "tmux", "break-pane", "-d", "-s", paneID)
		if err := breakCmd.Run(); err != nil {
			log.Error("CleanupAllPanes: failed to break pane %q: %v", paneID, err)
		} else {
			log.Debug("CleanupAllPanes: broke pane %q", paneID)
		}
	}

	// Clear the cached pane IDs
	m.claudePaneID = ""
	m.workdirPaneID = ""
}

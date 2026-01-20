package ui

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TiledModel represents the tiled view showing multiple Claude panes.
type TiledModel struct {
	tasks       []*db.Task // Active tasks (processing/queued)
	database    *db.DB
	width       int
	height      int
	selectedIdx int
	ready       bool

	// Track the grid layout
	gridCols int
	gridRows int

	// Track pane IDs for each task (indexed by position in grid)
	paneIDs []string

	// Track if panes have been set up
	panesSetup bool

	// TUI pane ID (for restoring later)
	tuiPaneID string

	// Loading state
	loading      bool
	loadingStart time.Time
}

// tiledPanesSetupMsg is sent when panes are set up.
type tiledPanesSetupMsg struct {
	paneIDs []string
	err     error
}

// tiledTickMsg is sent for spinner animation.
type tiledTickMsg struct{}

// NewTiledModel creates a new tiled model.
func NewTiledModel(tasks []*db.Task, database *db.DB, width, height int) *TiledModel {
	// Filter to only active tasks (queued or processing)
	var activeTasks []*db.Task
	for _, t := range tasks {
		if t.Status == db.StatusQueued || t.Status == db.StatusProcessing {
			activeTasks = append(activeTasks, t)
		}
	}

	// Sort by ID for consistent ordering
	sort.Slice(activeTasks, func(i, j int) bool {
		return activeTasks[i].ID < activeTasks[j].ID
	})

	m := &TiledModel{
		tasks:    activeTasks,
		database: database,
		width:    width,
		height:   height,
		ready:    true,
	}

	// Calculate optimal grid layout
	m.calculateGridLayout()

	return m
}

// calculateGridLayout determines the grid dimensions based on task count.
func (m *TiledModel) calculateGridLayout() {
	n := len(m.tasks)
	if n == 0 {
		m.gridCols = 0
		m.gridRows = 0
		return
	}

	// Try to create a roughly square grid
	// Prefer wider layouts since terminals are typically wider than tall
	switch {
	case n == 1:
		m.gridCols = 1
		m.gridRows = 1
	case n == 2:
		m.gridCols = 2
		m.gridRows = 1
	case n == 3:
		m.gridCols = 3
		m.gridRows = 1
	case n == 4:
		m.gridCols = 2
		m.gridRows = 2
	case n <= 6:
		m.gridCols = 3
		m.gridRows = 2
	case n <= 9:
		m.gridCols = 3
		m.gridRows = 3
	case n <= 12:
		m.gridCols = 4
		m.gridRows = 3
	default:
		// For many tasks, use 4 columns
		m.gridCols = 4
		m.gridRows = (n + 3) / 4
	}
}

// Init initializes the tiled model.
func (m *TiledModel) Init() tea.Cmd {
	if len(m.tasks) == 0 {
		return nil
	}

	m.loading = true
	m.loadingStart = time.Now()

	return tea.Batch(m.setupPanes(), m.tickCmd())
}

// tickCmd returns a command for spinner animation.
func (m *TiledModel) tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tiledTickMsg{}
	})
}

// setupPanes creates the tmux pane grid asynchronously.
func (m *TiledModel) setupPanes() tea.Cmd {
	tasks := m.tasks
	gridCols := m.gridCols
	gridRows := m.gridRows

	return func() tea.Msg {
		log := GetLogger()
		log.Info("TiledModel.setupPanes: setting up %d tasks in %dx%d grid", len(tasks), gridCols, gridRows)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Get current pane ID (TUI pane)
		currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
		currentPaneOut, err := currentPaneCmd.Output()
		if err != nil {
			log.Error("TiledModel.setupPanes: failed to get current pane: %v", err)
			return tiledPanesSetupMsg{err: err}
		}
		tuiPaneID := strings.TrimSpace(string(currentPaneOut))
		log.Debug("TiledModel.setupPanes: tuiPaneID=%s", tuiPaneID)

		// Create the grid of panes
		paneIDs := make([]string, len(tasks))

		// First, resize TUI to a smaller portion at top (10%)
		exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", "10%").Run()

		// Create the first row of panes below TUI
		// Split vertically first to create room for the grid
		if len(tasks) > 0 {
			// Find the daemon window for the first task
			firstTask := tasks[0]
			windowTarget := findTaskWindowStatic(firstTask.DaemonSession, firstTask.TmuxWindowID)

			if windowTarget != "" {
				// Get the pane index in the daemon window
				listPanesCmd := exec.CommandContext(ctx, "tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_index}")
				out, err := listPanesCmd.Output()
				if err == nil {
					indices := strings.Split(strings.TrimSpace(string(out)), "\n")
					if len(indices) > 0 {
						source := windowTarget + "." + indices[0]
						log.Info("TiledModel.setupPanes: joining first task pane from %s", source)

						err = exec.CommandContext(ctx, "tmux", "join-pane",
							"-v", "-l", "90%",
							"-s", source,
							"-t", tuiPaneID).Run()
						if err != nil {
							log.Error("TiledModel.setupPanes: failed to join first pane: %v", err)
						} else {
							// Get the pane ID of the joined pane
							paneIDCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
							paneIDOut, _ := paneIDCmd.Output()
							paneIDs[0] = strings.TrimSpace(string(paneIDOut))
							log.Debug("TiledModel.setupPanes: first pane ID=%s", paneIDs[0])

							// Set pane title
							title := fmt.Sprintf("#%d: %s", firstTask.ID, truncateTitle(firstTask.Title, 20))
							exec.CommandContext(ctx, "tmux", "select-pane", "-t", paneIDs[0], "-T", title).Run()
						}
					}
				}
			}
		}

		// Now create additional panes by splitting the first one
		for i := 1; i < len(tasks); i++ {
			task := tasks[i]
			windowTarget := findTaskWindowStatic(task.DaemonSession, task.TmuxWindowID)

			if windowTarget == "" {
				log.Warn("TiledModel.setupPanes: no window for task %d", task.ID)
				continue
			}

			// Get the pane index in the daemon window
			listPanesCmd := exec.CommandContext(ctx, "tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_index}")
			out, err := listPanesCmd.Output()
			if err != nil {
				log.Warn("TiledModel.setupPanes: failed to list panes for task %d: %v", task.ID, err)
				continue
			}

			indices := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(indices) == 0 {
				continue
			}

			source := windowTarget + "." + indices[0]

			// Determine if we need horizontal or vertical split based on grid position
			row := i / gridCols
			col := i % gridCols

			var targetPaneID string
			var splitDir string

			if col == 0 && i > 0 {
				// Start of new row - split vertically from a pane in previous row
				prevRowIdx := (row - 1) * gridCols
				if prevRowIdx >= 0 && paneIDs[prevRowIdx] != "" {
					targetPaneID = paneIDs[prevRowIdx]
					splitDir = "-v"
				}
			} else if col > 0 {
				// Continue row - split horizontally from previous pane in same row
				prevIdx := i - 1
				if paneIDs[prevIdx] != "" {
					targetPaneID = paneIDs[prevIdx]
					splitDir = "-h"
				}
			}

			if targetPaneID == "" || splitDir == "" {
				// Fallback: split from first pane
				if paneIDs[0] != "" {
					targetPaneID = paneIDs[0]
					if col == 0 {
						splitDir = "-v"
					} else {
						splitDir = "-h"
					}
				} else {
					continue
				}
			}

			log.Debug("TiledModel.setupPanes: joining task %d pane, split %s from %s", task.ID, splitDir, targetPaneID)

			err = exec.CommandContext(ctx, "tmux", "join-pane",
				splitDir,
				"-s", source,
				"-t", targetPaneID).Run()
			if err != nil {
				log.Warn("TiledModel.setupPanes: failed to join pane for task %d: %v", task.ID, err)
				continue
			}

			// Get the pane ID
			paneIDCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
			paneIDOut, _ := paneIDCmd.Output()
			paneIDs[i] = strings.TrimSpace(string(paneIDOut))
			log.Debug("TiledModel.setupPanes: task %d pane ID=%s", task.ID, paneIDs[i])

			// Set pane title
			title := fmt.Sprintf("#%d: %s", task.ID, truncateTitle(task.Title, 20))
			exec.CommandContext(ctx, "tmux", "select-pane", "-t", paneIDs[i], "-T", title).Run()
		}

		// Select the TUI pane to keep focus there
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", tuiPaneID).Run()

		// Configure tmux to show pane titles
		exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-status", "top").Run()
		exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-format", " #{pane_title} ").Run()

		log.Info("TiledModel.setupPanes: completed, created %d panes", countNonEmpty(paneIDs))

		return tiledPanesSetupMsg{paneIDs: paneIDs}
	}
}

// findTaskWindowStatic is a static version of findTaskWindow for use in goroutines.
func findTaskWindowStatic(daemonSession, windowID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Try window ID first (most reliable)
	if windowID != "" {
		cmd := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{window_id}")
		out, err := cmd.Output()
		if err == nil {
			for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if wid == windowID {
					return windowID
				}
			}
		}
	}

	// Try daemon session
	if daemonSession != "" {
		// Check if session exists
		cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", daemonSession)
		if cmd.Run() == nil {
			return daemonSession + ":0"
		}
	}

	return ""
}

// countNonEmpty counts non-empty strings in a slice.
func countNonEmpty(s []string) int {
	count := 0
	for _, v := range s {
		if v != "" {
			count++
		}
	}
	return count
}

// truncateTitle truncates a title to the given length.
func truncateTitle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// Update handles messages.
func (m *TiledModel) Update(msg tea.Msg) (*TiledModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tiledPanesSetupMsg:
		m.loading = false
		if msg.err == nil {
			m.paneIDs = msg.paneIDs
			m.panesSetup = true
		}
		return m, nil

	case tiledTickMsg:
		if m.loading {
			return m, m.tickCmd()
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("left", "h"))):
			if m.selectedIdx > 0 {
				m.selectedIdx--
				m.focusSelectedPane()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("right", "l"))):
			if m.selectedIdx < len(m.tasks)-1 {
				m.selectedIdx++
				m.focusSelectedPane()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("up", "k"))):
			newIdx := m.selectedIdx - m.gridCols
			if newIdx >= 0 {
				m.selectedIdx = newIdx
				m.focusSelectedPane()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("down", "j"))):
			newIdx := m.selectedIdx + m.gridCols
			if newIdx < len(m.tasks) {
				m.selectedIdx = newIdx
				m.focusSelectedPane()
			}
		}
	}

	return m, nil
}

// focusSelectedPane focuses the tmux pane for the selected task.
func (m *TiledModel) focusSelectedPane() {
	if m.selectedIdx >= 0 && m.selectedIdx < len(m.paneIDs) && m.paneIDs[m.selectedIdx] != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.paneIDs[m.selectedIdx]).Run()
	}
}

// SetSize updates the model dimensions.
func (m *TiledModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// SelectedTask returns the currently selected task.
func (m *TiledModel) SelectedTask() *db.Task {
	if m.selectedIdx >= 0 && m.selectedIdx < len(m.tasks) {
		return m.tasks[m.selectedIdx]
	}
	return nil
}

// Tasks returns all active tasks in the tiled view.
func (m *TiledModel) Tasks() []*db.Task {
	return m.tasks
}

// Cleanup breaks the pane layout and returns panes to their daemon windows.
func (m *TiledModel) Cleanup() {
	log := GetLogger()
	log.Info("TiledModel.Cleanup: breaking panes")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get current pane (TUI)
	currentPaneCmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	currentPaneOut, err := currentPaneCmd.Output()
	if err != nil {
		return
	}
	tuiPaneID := strings.TrimSpace(string(currentPaneOut))

	// Break each pane back to its daemon window
	for i, paneID := range m.paneIDs {
		if paneID == "" || paneID == tuiPaneID {
			continue
		}

		task := m.tasks[i]
		if task.DaemonSession == "" {
			continue
		}

		// Break pane back to daemon session
		log.Debug("TiledModel.Cleanup: breaking pane %s back to %s", paneID, task.DaemonSession)
		exec.CommandContext(ctx, "tmux", "break-pane",
			"-s", paneID,
			"-t", task.DaemonSession+":").Run()
	}

	// Resize TUI back to full size
	exec.CommandContext(ctx, "tmux", "resize-pane", "-t", tuiPaneID, "-y", "100%").Run()

	// Hide pane titles
	exec.CommandContext(ctx, "tmux", "set-option", "-t", "task-ui", "pane-border-status", "off").Run()
}

// View renders the tiled view header.
func (m *TiledModel) View() string {
	if len(m.tasks) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Width(m.width).
			Align(lipgloss.Center).
			Foreground(ColorMuted).
			MarginTop(2)
		return emptyStyle.Render("No active tasks running.\n\nPress 'x' on a task to start it.")
	}

	var content strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary)
	content.WriteString(headerStyle.Render(fmt.Sprintf("⊞ Tiled View - %d Active Tasks", len(m.tasks))))
	content.WriteString("\n")

	if m.loading {
		// Show loading spinner
		spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		frameIdx := int(time.Since(m.loadingStart).Milliseconds()/100) % len(spinnerFrames)
		spinnerStyle := lipgloss.NewStyle().Foreground(ColorInProgress)
		content.WriteString(spinnerStyle.Render(spinnerFrames[frameIdx] + " Setting up Claude panes..."))
		content.WriteString("\n")
	} else if m.panesSetup {
		// Show grid status
		statusStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		content.WriteString(statusStyle.Render(fmt.Sprintf("%d×%d grid • Use arrow keys to navigate • esc to exit", m.gridCols, m.gridRows)))
		content.WriteString("\n")
	}

	// Task list summary
	content.WriteString("\n")
	for i, task := range m.tasks {
		isSelected := i == m.selectedIdx

		// Task row
		var taskLine strings.Builder

		// Selection indicator
		if isSelected {
			taskLine.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("▸ "))
		} else {
			taskLine.WriteString("  ")
		}

		// Task ID and status
		statusIcon := StatusIcon(task.Status)
		statusColor := StatusColor(task.Status)
		taskLine.WriteString(lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon))
		taskLine.WriteString(" ")
		taskLine.WriteString(Dim.Render(fmt.Sprintf("#%d", task.ID)))
		taskLine.WriteString(" ")

		// Project
		if task.Project != "" {
			projectStyle := lipgloss.NewStyle().Foreground(ProjectColor(task.Project))
			taskLine.WriteString(projectStyle.Render("[" + task.Project + "]"))
			taskLine.WriteString(" ")
		}

		// Title (truncated to fit)
		title := task.Title
		maxTitleLen := m.width - 30
		if maxTitleLen < 20 {
			maxTitleLen = 20
		}
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen-1] + "…"
		}

		if isSelected {
			taskLine.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
		} else {
			taskLine.WriteString(title)
		}

		// Grid position
		row := i / m.gridCols
		col := i % m.gridCols
		taskLine.WriteString(Dim.Render(fmt.Sprintf(" [%d,%d]", row+1, col+1)))

		content.WriteString(taskLine.String())
		content.WriteString("\n")
	}

	return content.String()
}

// TiledKeyMap defines key bindings for tiled view.
type TiledKeyMap struct {
	Left   key.Binding
	Right  key.Binding
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Back   key.Binding
	Number key.Binding
}

// DefaultTiledKeyMap returns default key bindings for tiled view.
func DefaultTiledKeyMap() TiledKeyMap {
	return TiledKeyMap{
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "right"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "focus task"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc", "q"),
			key.WithHelp("esc/q", "back to dashboard"),
		),
		Number: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
			key.WithHelp("1-9", "select task"),
		),
	}
}

// SelectByNumber selects a task by its 1-indexed position.
func (m *TiledModel) SelectByNumber(n int) {
	idx := n - 1
	if idx >= 0 && idx < len(m.tasks) {
		m.selectedIdx = idx
		m.focusSelectedPane()
	}
}

// GetPaneID returns the pane ID for a task by index.
func (m *TiledModel) GetPaneID(idx int) string {
	if idx >= 0 && idx < len(m.paneIDs) {
		return m.paneIDs[idx]
	}
	return ""
}

// GetSelectedPaneID returns the pane ID of the selected task.
func (m *TiledModel) GetSelectedPaneID() string {
	return m.GetPaneID(m.selectedIdx)
}

// IsPanesSetup returns whether the panes have been set up.
func (m *TiledModel) IsPanesSetup() bool {
	return m.panesSetup
}

// RefreshTasks updates the task list.
func (m *TiledModel) RefreshTasks(tasks []*db.Task) {
	// Filter to only active tasks
	var activeTasks []*db.Task
	for _, t := range tasks {
		if t.Status == db.StatusQueued || t.Status == db.StatusProcessing {
			activeTasks = append(activeTasks, t)
		}
	}

	// Sort by ID for consistent ordering
	sort.Slice(activeTasks, func(i, j int) bool {
		return activeTasks[i].ID < activeTasks[j].ID
	})

	m.tasks = activeTasks
	m.calculateGridLayout()

	// Clamp selection
	if m.selectedIdx >= len(m.tasks) {
		m.selectedIdx = len(m.tasks) - 1
	}
	if m.selectedIdx < 0 {
		m.selectedIdx = 0
	}
}

// FocusTUIPane focuses the TUI pane.
func (m *TiledModel) FocusTUIPane() {
	if m.tuiPaneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.tuiPaneID).Run()
	}
}

// SelectedIndex returns the currently selected index.
func (m *TiledModel) SelectedIndex() int {
	return m.selectedIdx
}

// GridDimensions returns the grid columns and rows.
func (m *TiledModel) GridDimensions() (cols, rows int) {
	return m.gridCols, m.gridRows
}

// UpdateTaskStatus updates a task's displayed status.
func (m *TiledModel) UpdateTaskStatus(taskID int64, status string) {
	for _, task := range m.tasks {
		if task.ID == taskID {
			task.Status = status
			break
		}
	}
}

// UpdatePaneTitle updates the tmux pane title for a task.
func (m *TiledModel) UpdatePaneTitle(idx int, title string) {
	if idx >= 0 && idx < len(m.paneIDs) && m.paneIDs[idx] != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		exec.CommandContext(ctx, "tmux", "select-pane", "-t", m.paneIDs[idx], "-T", title).Run()
	}
}

// StoreTUIPaneID stores the TUI pane ID for later restoration.
func (m *TiledModel) StoreTUIPaneID(paneID string) {
	m.tuiPaneID = paneID
}

// HasActiveTasks returns true if there are active tasks.
func (m *TiledModel) HasActiveTasks() bool {
	return len(m.tasks) > 0
}

// TaskCount returns the number of active tasks.
func (m *TiledModel) TaskCount() int {
	return len(m.tasks)
}

// GetTask returns the task at the given index.
func (m *TiledModel) GetTask(idx int) *db.Task {
	if idx >= 0 && idx < len(m.tasks) {
		return m.tasks[idx]
	}
	return nil
}

// ValidSelection returns true if the current selection is valid.
func (m *TiledModel) ValidSelection() bool {
	return m.selectedIdx >= 0 && m.selectedIdx < len(m.tasks)
}

// parseTaskNumber parses a number key (1-9) into the number.
func parseTaskNumber(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

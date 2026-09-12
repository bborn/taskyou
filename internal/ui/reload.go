package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/tuireload"
)

// ReloadState contains navigation state only. Forms are never interrupted.
type ReloadState struct {
	TaskID int64  `json:"task_id"`
	Detail bool   `json:"detail"`
	Filter string `json:"filter"`
}

type reloadTokenMsg struct {
	token string
	err   error
}

// EnableReload is called only by the local process that can re-exec itself.
func (m *AppModel) EnableReload(token string)        { m.reloadEnabled = true; m.reloadToken = token }
func (m *AppModel) ReloadState() (ReloadState, bool) { return m.reloadSnapshot, m.reloadReady }
func (m *AppModel) RestoreReloadState(state ReloadState) {
	m.reloadRestoring = &state
	m.filterText = state.Filter
	m.filterInput.SetValue(state.Filter)
	m.pendingFocusTaskID = state.TaskID
	m.pendingPaletteQuery = "" // a reload returns to where the user was, not to `ty open`'s search
	if state.Filter != "" {
		m.reloadSelectionID = state.TaskID
	}
}

func (m *AppModel) checkReload() tea.Cmd {
	if !m.reloadEnabled || m.reloadCheckInFlight {
		return nil
	}
	m.reloadCheckInFlight = true
	database := m.db
	return func() tea.Msg { token, err := tuireload.Token(database); return reloadTokenMsg{token, err} }
}

func (m *AppModel) beginReload() tea.Cmd {
	// transitionInProgress(), not the bare field: a task switch holds that guard
	// until its panes report back, and if that report is ever lost only the
	// deadline reopens it. Reading the field directly let one stuck switch block
	// every future reload, so a TUI left in a detail view silently never picked
	// up a new binary no matter how often the reload was requested.
	if m.reloadWrites.Load() > 0 || !m.reloadPending || m.reloadReady || m.detailCleanupInFlight || m.transitionInProgress() {
		return nil
	}
	if m.currentView != ViewDashboard && m.currentView != ViewDetail {
		return nil
	}
	if !m.reloadPrepared {
		m.reloadSnapshot = ReloadState{Filter: m.filterText, Detail: m.currentView == ViewDetail}
		if task := m.kanban.SelectedTask(); task != nil {
			m.reloadSnapshot.TaskID = task.ID
		}
		if m.currentView == ViewDetail && m.selectedTask != nil {
			m.reloadSnapshot.TaskID = m.selectedTask.ID
		}
		m.reloadPrepared = true
	}
	if m.detailView != nil {
		m.currentView = ViewDashboard
		return m.detachDetail(true)
	}
	m.reloadReady = true
	if m.eventCh != nil && m.executor != nil {
		m.executor.UnsubscribeTaskEvents(m.eventCh)
	}
	m.stopDatabaseWatcher()
	return tea.Quit
}

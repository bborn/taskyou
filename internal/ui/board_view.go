package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// The board remembers how you left it. Display mode (kanban or flat list), the
// filter query, and the saved view that query came from are all written to
// settings as they change and restored on launch, so "show me just in progress
// and blocked" survives quitting the TUI. Without that a filter is a thing you
// retype every morning, which is the complaint the list view exists to answer.

// loadBoardViewState restores the persisted display mode, filter and saved view.
// Called once, before the first task load, so the very first render is already
// filtered rather than flashing the whole board.
func (m *AppModel) loadBoardViewState() {
	if m.db == nil {
		return
	}

	if mode, err := m.db.GetSetting(config.SettingBoardDisplayMode); err == nil {
		m.listMode = mode == config.BoardDisplayList
		m.kanban.SetListMode(m.listMode)
	}

	filter, err := m.db.GetSetting(config.SettingBoardFilter)
	if err != nil || strings.TrimSpace(filter) == "" {
		return
	}
	m.filterText = filter
	m.filterInput.SetValue(filter)
	if name, err := m.db.GetSetting(config.SettingBoardView); err == nil {
		m.activeView = name
	}
	m.syncBoardTitle()
}

// persistBoardViewState writes the current display mode, filter and view name.
// Failures are ignored: a settings write that loses a race is a cosmetic
// regression next launch, never a reason to interrupt what the user is doing.
func (m *AppModel) persistBoardViewState() {
	if m.db == nil {
		return
	}
	mode := config.BoardDisplayBoard
	if m.listMode {
		mode = config.BoardDisplayList
	}
	_ = m.db.SetSetting(config.SettingBoardDisplayMode, mode)
	_ = m.db.SetSetting(config.SettingBoardFilter, m.filterText)
	_ = m.db.SetSetting(config.SettingBoardView, m.activeView)
}

// setListMode switches the board between kanban and list, persisting the choice.
func (m *AppModel) setListMode(on bool) {
	m.listMode = on
	m.kanban.SetListMode(on)
	m.syncBoardTitle()
	m.persistBoardViewState()
}

// toggleListMode flips between the kanban board and the flat list.
func (m *AppModel) toggleListMode() tea.Cmd {
	m.setListMode(!m.listMode)
	if m.listMode {
		m.notification = "List view — press v for the board"
	} else {
		m.notification = "Board view — press v for the list"
	}
	m.notifyUntil = time.Now().Add(3 * time.Second)
	return nil
}

// applySavedView makes a saved view the board's current filter.
func (m *AppModel) applySavedView(v *db.SavedView) tea.Cmd {
	if v == nil {
		return nil
	}
	m.activeView = v.Name
	m.filterText = v.Query
	m.filterInput.SetValue(v.Query)
	m.filterActive = false
	m.showFilterDropdown = false
	m.filterAutocomplete.Reset()
	// applyFilter dispatches the heavy pass as a command when the view carries a
	// keyword or a [project] tag, so the result must be handed back to the
	// runtime rather than dropped.
	cmd := m.applyFilter()
	m.syncBoardTitle()
	m.persistBoardViewState()
	return cmd
}

// clearBoardFilter drops the filter and any saved view behind it.
func (m *AppModel) clearBoardFilter() tea.Cmd {
	m.activeView = ""
	m.filterText = ""
	m.filterInput.SetValue("")
	m.showFilterDropdown = false
	m.filterAutocomplete.Reset()
	cmd := m.applyFilter()
	m.syncBoardTitle()
	m.persistBoardViewState()
	return cmd
}

// noteFilterEdited records that the filter no longer came from a saved view.
// Typing over an applied view makes it an ad-hoc filter — the view is a
// starting point, not a lock.
func (m *AppModel) noteFilterEdited() {
	if m.activeView == "" {
		return
	}
	m.activeView = ""
	m.syncBoardTitle()
}

// boardFilterLabel names what the board is currently showing: the saved view if
// one is applied, otherwise the raw query, otherwise nothing.
func (m *AppModel) boardFilterLabel() string {
	if m.activeView != "" {
		return m.activeView
	}
	return strings.TrimSpace(m.filterText)
}

// syncBoardTitle pushes the current label into the list header.
func (m *AppModel) syncBoardTitle() {
	if m.kanban != nil {
		m.kanban.SetListTitle(m.boardFilterLabel())
	}
}

// openViewPicker opens the saved-views modal.
func (m *AppModel) openViewPicker() tea.Cmd {
	m.viewPicker = NewViewPickerModel(m.db, m.filterText, m.width, m.height)
	m.previousView = m.currentView
	m.currentView = ViewSavedViews
	return m.viewPicker.Init()
}

// updateSavedViews routes input to the saved-views modal and acts on its result.
func (m *AppModel) updateSavedViews(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.viewPicker == nil {
		m.currentView = ViewDashboard
		return m, nil
	}

	picker, cmd := m.viewPicker.Update(msg)
	m.viewPicker = picker

	switch {
	case picker.IsCancelled():
		m.viewPicker = nil
		m.currentView = ViewDashboard
		return m, cmd
	case picker.ClearRequested():
		m.viewPicker = nil
		m.currentView = ViewDashboard
		return m, tea.Batch(cmd, m.clearBoardFilter())
	case picker.Applied() != nil:
		applied := picker.Applied()
		m.viewPicker = nil
		m.currentView = ViewDashboard
		return m, tea.Batch(cmd, m.applySavedView(applied))
	}
	return m, cmd
}

package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func TestPaletteSearchEnterWaitsForLatestQuery(t *testing.T) {
	m := &CommandPaletteModel{searchInput: textinput.New(), allTasks: []*db.Task{
		{ID: 1, Title: "alpha", Status: db.StatusBacklog},
		{ID: 2, Title: "beta", Status: db.StatusBacklog},
	}}
	m.filter()
	m.searchInput.SetValue("alpha")
	first := m.searchAsync()
	m.searchInput.SetValue("beta")
	if m.searchAsync() != nil {
		t.Fatal("overlapping search started")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selectedTask != nil || m.aiCommandRequest {
		t.Fatal("Enter acted on stale results")
	}
	_, next := m.Update(first())
	if next == nil {
		t.Fatal("latest query not scheduled")
	}
	m.Update(next())
	if m.selectedTask == nil || m.selectedTask.ID != 2 {
		t.Fatal("Enter did not select latest query result")
	}
}

func TestPaletteSearchDoesNotAffectReopenedPalette(t *testing.T) {
	old := &CommandPaletteModel{searchInput: textinput.New()}
	old.searchInput.SetValue("missing")
	cmd := old.searchAsync()
	reopened := &CommandPaletteModel{searchInput: textinput.New(), searchInFlight: true}
	reopened.Update(cmd())
	if !reopened.searchInFlight {
		t.Fatal("old result changed a different palette")
	}
}

package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/panel"
)

func TestWorkspaceTabsAndLauncher(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task := &db.Task{Title: "Checkout review", Status: "backlog"}
	if err = d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewWorkspaceModel(ctx, panel.New(d), task.ID)
	cmd := m.load()
	m.Update(cmd())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	if !m.launcher || !strings.Contains(m.View(), "Pull request") {
		t.Fatal("launcher missing")
	}
	cmd = m.open("pr", "")
	if cmd != nil {
		m.Update(cmd())
	}
	if m.launcher || m.content.Kind != "markdown" || !strings.Contains(m.View(), "No cached") {
		t.Fatal(m.View())
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	if cmd != nil {
		m.Update(cmd())
	}
	if len(m.tabs) != 1 || m.tabs[0].ProviderID != "shell" {
		t.Fatal(m.tabs)
	}
}

func TestWorkspaceIgnoresStaleResource(t *testing.T) {
	m := &WorkspaceModel{active: "new", loading: true}
	m.Update(workspaceLoaded{requested: "old", id: "old", content: panel.Content{Kind: "text", Text: "stale"}})
	if m.active != "new" || m.content.Text != "" || m.loading {
		t.Fatalf("stale update: %+v", m)
	}
}
func TestShellKeyboard(t *testing.T) {
	for _, tc := range []struct {
		key     tea.KeyMsg
		text    string
		literal bool
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("echo hello")}, "echo hello", true},
		{tea.KeyMsg{Type: tea.KeyEnter}, "Enter", false},
		{tea.KeyMsg{Type: tea.KeySpace}, " ", true},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "C-c", false},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "BSpace", false},
	} {
		text, literal := shellKey(tc.key)
		if text != tc.text || literal != tc.literal {
			t.Errorf("%v => %q %v", tc.key, text, literal)
		}
	}
}

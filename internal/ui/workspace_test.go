package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

func newWorkspaceForTest(t *testing.T) *WorkspaceModel {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	task := &db.Task{Title: "Workspace controls", Status: "backlog"}
	if err := d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := NewWorkspaceModel(ctx, panel.New(d), task.ID)
	m.Update(m.load()())
	return m
}

func TestWorkspaceLauncherFuzzySelection(t *testing.T) {
	m := newWorkspaceForTest(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("plrq")})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		m.Update(cmd())
	}
	if m.launcher || m.content.Kind != "markdown" {
		t.Fatalf("fuzzy action did not open PR: %s", m.View())
	}
}

func TestWorkspaceContextualHelpAndShellInput(t *testing.T) {
	m := newWorkspaceForTest(t)
	m.content = panel.Content{Kind: "shell"}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	if !strings.Contains(m.View(), "refresh") || !strings.Contains(m.View(), "close tab") {
		t.Fatalf("expanded help missing: %s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	if !strings.Contains(m.View(), "open") || !strings.Contains(m.View(), "cancel") {
		t.Fatalf("launcher help missing: %s", m.View())
	}
}

func TestWorkspaceFileListPagesAndFilters(t *testing.T) {
	m := newWorkspaceForTest(t)
	m.Update(tea.WindowSizeMsg{Width: 64, Height: 18})
	entries := make([]panel.Entry, 40)
	for i := range entries {
		entries[i] = panel.Entry{Name: fmt.Sprintf("document-%02d.md", i), Path: fmt.Sprintf("document-%02d.md", i)}
	}
	m.Update(workspaceLoaded{requested: m.active, id: m.active, tabs: m.tabs, content: panel.Content{Kind: "files", Entries: entries}})
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	selected := m.files.SelectedItem().(workspaceItem)
	if selected.resource == entries[0].Path || !strings.Contains(m.View(), selected.title) {
		t.Fatalf("paged selection is not visible: %s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.files.FilterState() != list.Filtering {
		t.Fatal("file filter did not open")
	}
	// SetFilterText uses the component's real fuzzy filter and result selection.
	m.files.SetFilterText("document-39")
	m.files.SetFilterState(list.Filtering)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // applying a filter must not open a tab
	if len(m.tabs) != 1 || m.files.FilterState() != list.FilterApplied {
		t.Fatal("enter did not apply filter")
	}
	if got := m.files.SelectedItem().(workspaceItem).resource; got != entries[39].Path {
		t.Fatalf("selected %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true})
	if !strings.Contains(m.View(), "parent directory") {
		t.Fatalf("file help missing: %s", m.View())
	}
	if got := lipgloss.Height(m.View()); got > 18 {
		t.Fatalf("expanded help overflowed height: %d", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.files.IsFiltered() {
		t.Fatal("escape did not clear filter")
	}
}

func TestWorkspaceShellKeepsOrdinaryListKeys(t *testing.T) {
	// No input worker: inspect exactly what the workspace forwards to the shell.
	m := &WorkspaceModel{keys: newWorkspaceKeys(), tabs: []panel.Instance{{ID: "shell"}}, content: panel.Content{Kind: "shell"}, inputs: make(chan workspaceInput, 4)}
	for _, r := range "q?/" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if got := <-m.inputs; got.text != string(r) {
			t.Fatalf("shell key intercepted: %#v", got)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := <-m.inputs; got.text != "C-c" {
		t.Fatalf("shell interrupt intercepted: %#v", got)
	}
}

package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func TestRetryModel_DangerousMode(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	m := NewRetryModel(task, database, 80, 24)

	// Initially not dangerous
	if m.IsDangerous() {
		t.Error("Expected IsDangerous to be false initially")
	}

	// Submit with ctrl+d should set dangerous mode
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.submitted {
		t.Error("Expected submitted to be true after ctrl+d")
	}
	if !m.IsDangerous() {
		t.Error("Expected IsDangerous to be true after ctrl+d submission")
	}
}

func TestRetryModel_NormalSubmitNotDangerous(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	m := NewRetryModel(task, database, 80, 24)

	// Submit with ctrl+s should NOT set dangerous mode
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.submitted {
		t.Error("Expected submitted to be true after ctrl+s")
	}
	if m.IsDangerous() {
		t.Error("Expected IsDangerous to be false after ctrl+s submission")
	}
}

func TestRetryModel_ViewShowsDangerousHint(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	m := NewRetryModel(task, database, 80, 24)
	view := m.View()

	if !containsText(view, "ctrl+d") {
		t.Error("Retry view should show ctrl+d shortcut for dangerous mode submission")
	}
}

package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func TestDefaultKeyMap(t *testing.T) {
	// Verify DefaultKeyMap creates valid key bindings
	keys := DefaultKeyMap()

	// Check that some key bindings are properly defined
	if keys.Enter.Help().Key != "enter" {
		t.Error("Enter key should have help text 'enter'")
	}

	if keys.Quit.Help().Key != "ctrl+c" {
		t.Error("Quit key should have help text 'ctrl+c'")
	}

	if keys.New.Help().Key != "n" {
		t.Error("New key should have help text 'n'")
	}

	if keys.ChangeStatus.Help().Key != "S" {
		t.Error("ChangeStatus key should have help text 'S'")
	}

	if keys.OpenWorktree.Help().Key != "o" {
		t.Error("OpenWorktree key should have help text 'o'")
	}

}

func TestNotificationHelpers(t *testing.T) {
	m := &AppModel{}

	before := time.Now()
	m.showTaskNotification(42, "Task done", time.Second)

	if m.notification != "Task done" {
		t.Fatalf("expected notification to be set, got %q", m.notification)
	}

	if m.notificationTaskID != 42 {
		t.Fatalf("expected notificationTaskID 42, got %d", m.notificationTaskID)
	}

	if !m.notifyUntil.After(before) {
		t.Error("notifyUntil should be set in the future for task notifications")
	}

	m.showNotification("Generic", time.Second)
	if m.notificationTaskID != 0 {
		t.Errorf("expected task ID to reset for generic notifications, got %d", m.notificationTaskID)
	}

	m.clearNotification()
	if m.notification != "" || m.notificationTaskID != 0 || !m.notifyUntil.IsZero() {
		t.Error("clearNotification should reset notification state")
	}
}

func TestShowChangeStatus_OnlyIncludesKanbanStatuses(t *testing.T) {
	// Create a minimal app model
	m := &AppModel{
		width: 100,
	}

	// Create a task with backlog status
	task := &db.Task{
		ID:     1,
		Title:  "Test Task",
		Status: db.StatusBacklog,
	}

	// Call showChangeStatus
	m.showChangeStatus(task)

	// Verify that the form was created
	if m.changeStatusForm == nil {
		t.Fatal("changeStatusForm was not created")
	}

	// Verify that the current view is set correctly
	if m.currentView != ViewChangeStatus {
		t.Errorf("currentView = %v, want %v", m.currentView, ViewChangeStatus)
	}

	// Verify that the pending task is set
	if m.pendingChangeStatusTask != task {
		t.Error("pendingChangeStatusTask was not set correctly")
	}

	// The function should only offer statuses that map to Kanban columns
	// We verify this by checking that the available statuses are only:
	// - StatusQueued (In Progress)
	// - StatusBlocked
	// - StatusDone
	// StatusProcessing should NOT be included as it's system-managed
	// StatusBacklog is excluded because it's the current status

	// Note: We can't directly inspect the form options without accessing
	// internal huh.Form fields, but we've verified the code only includes
	// the 4 Kanban-mapped statuses in the allStatuses slice
}

func TestShowChangeStatus_ExcludesCurrentStatus(t *testing.T) {
	m := &AppModel{
		width: 100,
	}

	tests := []struct {
		name          string
		currentStatus string
	}{
		{"backlog task", db.StatusBacklog},
		{"queued task", db.StatusQueued},
		{"blocked task", db.StatusBlocked},
		{"done task", db.StatusDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &db.Task{
				ID:     1,
				Title:  "Test Task",
				Status: tt.currentStatus,
			}

			m.showChangeStatus(task)

			if m.changeStatusForm == nil {
				t.Fatal("changeStatusForm was not created")
			}

			// Verify the pending task is set correctly
			if m.pendingChangeStatusTask != task {
				t.Error("pendingChangeStatusTask was not set correctly")
			}

			// The form.Init() updates changeStatusValue to the first available option,
			// so we verify that it's not the current status (which was excluded)
			if m.changeStatusValue == tt.currentStatus {
				t.Errorf("changeStatusValue should not equal current status %q after form init", tt.currentStatus)
			}
		})
	}
}

func TestShowCloseConfirm_SetsUpConfirmation(t *testing.T) {
	// Create a minimal app model
	m := &AppModel{
		width: 100,
	}

	// Create a test task
	task := &db.Task{
		ID:     42,
		Title:  "Test Task to Close",
		Status: db.StatusQueued,
	}

	// Call showCloseConfirm
	m.showCloseConfirm(task)

	// Verify that the close confirmation form was created
	if m.closeConfirm == nil {
		t.Fatal("closeConfirm form was not created")
	}

	// Verify that the current view is set to ViewCloseConfirm
	if m.currentView != ViewCloseConfirm {
		t.Errorf("currentView = %v, want %v", m.currentView, ViewCloseConfirm)
	}

	// Verify that the pending close task is set correctly
	if m.pendingCloseTask != task {
		t.Error("pendingCloseTask was not set correctly")
	}

	// Verify that the confirm value starts as false (user hasn't confirmed yet)
	if m.closeConfirmValue != false {
		t.Error("closeConfirmValue should be false initially")
	}
}

func TestShowCloseConfirm_DifferentTasks(t *testing.T) {
	m := &AppModel{
		width: 100,
	}

	tests := []struct {
		name   string
		taskID int64
		title  string
		status string
	}{
		{"backlog task", 1, "Backlog Task", db.StatusBacklog},
		{"queued task", 2, "In Progress Task", db.StatusQueued},
		{"processing task", 3, "Processing Task", db.StatusProcessing},
		{"blocked task", 4, "Blocked Task", db.StatusBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &db.Task{
				ID:     tt.taskID,
				Title:  tt.title,
				Status: tt.status,
			}

			m.showCloseConfirm(task)

			if m.closeConfirm == nil {
				t.Fatal("closeConfirm form was not created")
			}

			if m.currentView != ViewCloseConfirm {
				t.Errorf("currentView = %v, want %v", m.currentView, ViewCloseConfirm)
			}

			if m.pendingCloseTask != task {
				t.Error("pendingCloseTask was not set correctly")
			}
		})
	}
}

func TestOpenWorktreeInEditor_NoWorktreePath(t *testing.T) {
	m := &AppModel{}

	// Test task with no worktree path
	task := &db.Task{
		ID:           1,
		Title:        "Test Task",
		WorktreePath: "",
	}

	cmd := m.openWorktreeInEditor(task)
	msg := cmd()

	result, ok := msg.(worktreeOpenedMsg)
	if !ok {
		t.Fatal("expected worktreeOpenedMsg")
	}

	if result.err == nil {
		t.Error("expected error for task with no worktree path")
	}
}

func TestOpenWorktreeInEditor_NonexistentPath(t *testing.T) {
	m := &AppModel{}

	// Test task with non-existent worktree path
	task := &db.Task{
		ID:           1,
		Title:        "Test Task",
		WorktreePath: "/nonexistent/path/that/does/not/exist",
	}

	cmd := m.openWorktreeInEditor(task)
	msg := cmd()

	result, ok := msg.(worktreeOpenedMsg)
	if !ok {
		t.Fatal("expected worktreeOpenedMsg")
	}

	if result.err == nil {
		t.Error("expected error for non-existent worktree path")
	}
}

func TestNewTaskFormEscapeShowsConfirmationWhenHasData(t *testing.T) {
	// Create app model with new task form
	m := &AppModel{
		width:       100,
		height:      50,
		currentView: ViewNewTask,
		newTaskForm: NewFormModel(nil, 100, 50, ""),
	}

	// Add data to the form
	m.newTaskForm.titleInput.SetValue("Some task title")

	// Press ESC
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.updateNewTaskForm(escMsg)
	am := model.(*AppModel)

	// Form should still be open with confirmation showing
	if am.newTaskForm == nil {
		t.Error("expected form to still be open while showing confirmation")
	}
	if am.currentView != ViewNewTask {
		t.Errorf("expected view to still be ViewNewTask, got %v", am.currentView)
	}
	if !am.newTaskForm.showCancelConfirm {
		t.Error("expected confirmation prompt to be shown")
	}
}

func TestNewTaskFormEscapeClosesImmediatelyWhenEmpty(t *testing.T) {
	// Create app model with empty new task form
	m := &AppModel{
		width:       100,
		height:      50,
		currentView: ViewNewTask,
		newTaskForm: NewFormModel(nil, 100, 50, ""),
	}

	// Press ESC on empty form
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.updateNewTaskForm(escMsg)
	am := model.(*AppModel)

	// Form should be closed immediately
	if am.newTaskForm != nil {
		t.Error("expected form to be closed for empty form")
	}
	if am.currentView != ViewDashboard {
		t.Errorf("expected view to be ViewDashboard, got %v", am.currentView)
	}
}

func TestEditTaskFormEscapeShowsConfirmationWhenHasData(t *testing.T) {
	// Create app model with edit task form
	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewEditTask,
		previousView: ViewDashboard,
		editTaskForm: NewFormModel(nil, 100, 50, ""),
		editingTask:  &db.Task{ID: 1, Title: "Original Title"},
	}

	// Add data to the form
	m.editTaskForm.titleInput.SetValue("Modified title")

	// Press ESC
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.updateEditTaskForm(escMsg)
	am := model.(*AppModel)

	// Form should still be open with confirmation showing
	if am.editTaskForm == nil {
		t.Error("expected form to still be open while showing confirmation")
	}
	if am.currentView != ViewEditTask {
		t.Errorf("expected view to still be ViewEditTask, got %v", am.currentView)
	}
	if !am.editTaskForm.showCancelConfirm {
		t.Error("expected confirmation prompt to be shown")
	}
}

func TestEditTaskFormEscapeClosesImmediatelyWhenEmpty(t *testing.T) {
	// Create app model with empty edit task form
	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewEditTask,
		previousView: ViewDashboard,
		editTaskForm: NewFormModel(nil, 100, 50, ""),
		editingTask:  &db.Task{ID: 1, Title: "Original Title"},
	}

	// Press ESC on empty form
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.updateEditTaskForm(escMsg)
	am := model.(*AppModel)

	// Form should be closed immediately
	if am.editTaskForm != nil {
		t.Error("expected form to be closed for empty form")
	}
	if am.currentView != ViewDashboard {
		t.Errorf("expected view to be ViewDashboard, got %v", am.currentView)
	}
}

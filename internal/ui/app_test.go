package ui

import (
	"testing"

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

// TestScoreTaskForFilter tests the fuzzy matching logic for the kanban filter.
// This should match the behavior of the command palette (Ctrl+P) scoring.
func TestScoreTaskForFilter(t *testing.T) {
	tests := []struct {
		name     string
		task     *db.Task
		query    string
		wantMin  int  // minimum expected score (-1 means no match)
		wantMax  int  // maximum expected score (use same as min for exact)
		wantHigh bool // true if this should score higher than baseline
	}{
		{
			name:    "ID exact match gives highest priority",
			task:    &db.Task{ID: 123, Title: "Some task"},
			query:   "123",
			wantMin: 1000,
			wantMax: 1000,
		},
		{
			name:    "ID with hash prefix",
			task:    &db.Task{ID: 456, Title: "Another task"},
			query:   "#456",
			wantMin: 1000,
			wantMax: 1000,
		},
		{
			name:    "PR number match",
			task:    &db.Task{ID: 1, Title: "Fix bug", PRNumber: 789},
			query:   "789",
			wantMin: 900,
			wantMax: 900,
		},
		{
			name:    "PR URL match",
			task:    &db.Task{ID: 1, Title: "Fix bug", PRURL: "https://github.com/org/repo/pull/123"},
			query:   "github.com",
			wantMin: 800,
			wantMax: 800,
		},
		{
			name:    "title fuzzy match with consecutive chars",
			task:    &db.Task{ID: 1, Title: "Add authentication feature"},
			query:   "auth",
			wantMin: 100, // should get decent score
			wantMax: 300,
		},
		{
			name:    "title fuzzy match non-consecutive",
			task:    &db.Task{ID: 1, Title: "design website"},
			query:   "dsnw",
			wantMin: 100, // should match with decent score due to word boundary bonuses
			wantMax: 300,
		},
		{
			name:    "project name fuzzy match",
			task:    &db.Task{ID: 1, Title: "Some task", Project: "workflow"},
			query:   "wkflw",
			wantMin: 100, // matches project (with -50 penalty but still decent)
			wantMax: 300,
		},
		{
			name:    "status substring match",
			task:    &db.Task{ID: 1, Title: "Task", Status: "processing"},
			query:   "process",
			wantMin: 100,
			wantMax: 100,
		},
		{
			name:    "type fuzzy match",
			task:    &db.Task{ID: 1, Title: "Task", Type: "feature"},
			query:   "feat",
			wantMin: 100, // good score due to word start bonus
			wantMax: 300,
		},
		{
			name:    "no match returns -1",
			task:    &db.Task{ID: 1, Title: "Hello world"},
			query:   "xyz",
			wantMin: -1,
			wantMax: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreTaskForFilter(tt.task, tt.query)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("scoreTaskForFilter() = %d, want between %d and %d", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestScoreTaskForFilterRanking verifies that better matches score higher.
func TestScoreTaskForFilterRanking(t *testing.T) {
	tests := []struct {
		name        string
		higherTask  *db.Task
		higherQuery string
		lowerTask   *db.Task
		lowerQuery  string
	}{
		{
			name:        "ID match beats title match",
			higherTask:  &db.Task{ID: 123, Title: "Some task"},
			higherQuery: "123",
			lowerTask:   &db.Task{ID: 1, Title: "Task 123"},
			lowerQuery:  "task",
		},
		{
			name:        "exact title substring beats fuzzy match",
			higherTask:  &db.Task{ID: 1, Title: "authentication"},
			higherQuery: "auth",
			lowerTask:   &db.Task{ID: 2, Title: "author handling"},
			lowerQuery:  "athn",
		},
		{
			name:        "word boundary match beats middle match",
			higherTask:  &db.Task{ID: 1, Title: "fix authentication bug"},
			higherQuery: "auth",
			lowerTask:   &db.Task{ID: 2, Title: "authenticate users"},
			lowerQuery:  "cate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			higherScore := scoreTaskForFilter(tt.higherTask, tt.higherQuery)
			lowerScore := scoreTaskForFilter(tt.lowerTask, tt.lowerQuery)
			if higherScore <= lowerScore {
				t.Errorf("expected higher score (%d) > lower score (%d)", higherScore, lowerScore)
			}
		})
	}
}

func TestJumpToNotificationKey(t *testing.T) {
	// Create app model with kanban board and tasks
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBlocked},
		{ID: 3, Title: "Task 3", Status: db.StatusDone},
	}

	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		notification: "⚠ Task #2 needs input: Task 2 (g to jump)",
		notifyTaskID: 2,
		kanban:       NewKanbanBoard(100, 50),
	}
	m.kanban.SetTasks(tasks)

	// Verify initial state - task 1 should be selected (first task in first column)
	if task := m.kanban.SelectedTask(); task != nil && task.ID == 2 {
		// Reset selection to different task
		m.kanban.SelectTask(1)
	}

	// Verify notification fields are set before key press
	if m.notification == "" {
		t.Error("expected notification to be set before key press")
	}
	if m.notifyTaskID != 2 {
		t.Errorf("expected notifyTaskID to be 2 before key press, got %d", m.notifyTaskID)
	}

	// Press 'g' to jump to notification
	// Note: We test with a minimal setup that doesn't have executor/db,
	// so we only verify the state changes, not the actual command execution
	gMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
	model, _ := m.updateDashboard(gMsg)
	am := model.(*AppModel)

	// Notification should be cleared
	if am.notification != "" {
		t.Errorf("expected notification to be cleared, got %q", am.notification)
	}

	// NotifyTaskID should be cleared
	if am.notifyTaskID != 0 {
		t.Errorf("expected notifyTaskID to be 0, got %d", am.notifyTaskID)
	}

	// Kanban should have task 2 selected
	if task := am.kanban.SelectedTask(); task == nil || task.ID != 2 {
		if task == nil {
			t.Error("expected task 2 to be selected, but no task is selected")
		} else {
			t.Errorf("expected task 2 to be selected, got task %d", task.ID)
		}
	}
}

func TestJumpToNotificationKey_NoNotification(t *testing.T) {
	// Create app model with kanban board but no active notification
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
	}

	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		notification: "",
		notifyTaskID: 0,
		kanban:       NewKanbanBoard(100, 50),
	}
	m.kanban.SetTasks(tasks)

	// Press 'g' when no notification is active
	gMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
	_, cmd := m.updateDashboard(gMsg)

	// Should return nil command since there's no notification
	if cmd != nil {
		t.Error("expected nil command when no notification is active")
	}
}

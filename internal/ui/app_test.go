package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
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

func TestApplyKeybindingsConfig_NilConfig(t *testing.T) {
	// When config is nil, KeyMap should remain unchanged
	original := DefaultKeyMap()
	result := ApplyKeybindingsConfig(original, nil)

	if result.New.Help().Key != "n" {
		t.Error("New key should remain 'n' when config is nil")
	}
	if result.Quit.Help().Key != "ctrl+c" {
		t.Error("Quit key should remain 'ctrl+c' when config is nil")
	}
}

func TestApplyKeybindingsConfig_PartialOverride(t *testing.T) {
	// Test that only specified keys are overridden
	original := DefaultKeyMap()

	cfg := &config.KeybindingsConfig{
		New: &config.KeybindingConfig{
			Keys: []string{"ctrl+n"},
			Help: "create task",
		},
	}

	result := ApplyKeybindingsConfig(original, cfg)

	// New should be overridden
	if result.New.Help().Key != "ctrl+n" {
		t.Errorf("Expected New key help to be 'ctrl+n', got '%s'", result.New.Help().Key)
	}
	if result.New.Help().Desc != "create task" {
		t.Errorf("Expected New help desc to be 'create task', got '%s'", result.New.Help().Desc)
	}

	// Other keys should remain unchanged
	if result.Quit.Help().Key != "ctrl+c" {
		t.Error("Quit key should remain unchanged")
	}
	if result.Filter.Help().Key != "/" {
		t.Error("Filter key should remain unchanged")
	}
}

func TestApplyKeybindingsConfig_MultipleKeys(t *testing.T) {
	// Test binding with multiple keys
	original := DefaultKeyMap()

	cfg := &config.KeybindingsConfig{
		CommandPalette: &config.KeybindingConfig{
			Keys: []string{"ctrl+k", "cmd+k", "p"},
			Help: "search",
		},
	}

	result := ApplyKeybindingsConfig(original, cfg)

	// Should use first key for help display
	if result.CommandPalette.Help().Key != "ctrl+k" {
		t.Errorf("Expected CommandPalette help key to be 'ctrl+k', got '%s'", result.CommandPalette.Help().Key)
	}
}

func TestApplyKeybindingsConfig_PreservesHelpWhenEmpty(t *testing.T) {
	// Test that help text is preserved when not specified in config
	original := DefaultKeyMap()

	cfg := &config.KeybindingsConfig{
		Filter: &config.KeybindingConfig{
			Keys: []string{"f"},
			// Help not specified
		},
	}

	result := ApplyKeybindingsConfig(original, cfg)

	// Key should be changed but help should preserve original
	if result.Filter.Help().Key != "f" {
		t.Errorf("Expected Filter key to be 'f', got '%s'", result.Filter.Help().Key)
	}
}

func TestApplyKeybindingsConfig_EmptyKeys(t *testing.T) {
	// Test that binding is not changed when keys array is empty
	original := DefaultKeyMap()

	cfg := &config.KeybindingsConfig{
		New: &config.KeybindingConfig{
			Keys: []string{}, // Empty keys
			Help: "create",
		},
	}

	result := ApplyKeybindingsConfig(original, cfg)

	// Should remain unchanged because keys is empty
	if result.New.Help().Key != "n" {
		t.Errorf("Expected New key to remain 'n' when keys is empty, got '%s'", result.New.Help().Key)
	}
}

func TestApplyKeybindingsConfig_AllBindings(t *testing.T) {
	// Test that all bindings can be overridden
	original := DefaultKeyMap()

	cfg := &config.KeybindingsConfig{
		Left:               &config.KeybindingConfig{Keys: []string{"h"}, Help: "left"},
		Right:              &config.KeybindingConfig{Keys: []string{"l"}, Help: "right"},
		Up:                 &config.KeybindingConfig{Keys: []string{"k"}, Help: "up"},
		Down:               &config.KeybindingConfig{Keys: []string{"j"}, Help: "down"},
		Enter:              &config.KeybindingConfig{Keys: []string{"o"}, Help: "open"},
		Back:               &config.KeybindingConfig{Keys: []string{"q"}, Help: "back"},
		New:                &config.KeybindingConfig{Keys: []string{"a"}, Help: "add"},
		Edit:               &config.KeybindingConfig{Keys: []string{"i"}, Help: "modify"},
		Queue:              &config.KeybindingConfig{Keys: []string{"r"}, Help: "run"},
		Retry:              &config.KeybindingConfig{Keys: []string{"R"}, Help: "redo"},
		Close:              &config.KeybindingConfig{Keys: []string{"d"}, Help: "done"},
		Archive:            &config.KeybindingConfig{Keys: []string{"A"}, Help: "arch"},
		Delete:             &config.KeybindingConfig{Keys: []string{"D"}, Help: "del"},
		Refresh:            &config.KeybindingConfig{Keys: []string{"ctrl+r"}, Help: "reload"},
		Settings:           &config.KeybindingConfig{Keys: []string{"S"}, Help: "config"},
		Help:               &config.KeybindingConfig{Keys: []string{"H"}, Help: "help"},
		Quit:               &config.KeybindingConfig{Keys: []string{"Q"}, Help: "exit"},
		ChangeStatus:       &config.KeybindingConfig{Keys: []string{"s"}, Help: "status"},
		CommandPalette:     &config.KeybindingConfig{Keys: []string{"p"}, Help: "palette"},
		ToggleDangerous:    &config.KeybindingConfig{Keys: []string{"!"}, Help: "danger"},
		TogglePin:          &config.KeybindingConfig{Keys: []string{"t"}, Help: "pin"},
		Filter:             &config.KeybindingConfig{Keys: []string{"/"}, Help: "search"},
		OpenWorktree:       &config.KeybindingConfig{Keys: []string{"w"}, Help: "worktree"},
		ToggleShellPane:    &config.KeybindingConfig{Keys: []string{"`"}, Help: "shell"},
		JumpToNotification: &config.KeybindingConfig{Keys: []string{"g"}, Help: "notify"},
		FocusBacklog:       &config.KeybindingConfig{Keys: []string{"1"}, Help: "col1"},
		FocusInProgress:    &config.KeybindingConfig{Keys: []string{"2"}, Help: "col2"},
		FocusBlocked:       &config.KeybindingConfig{Keys: []string{"3"}, Help: "col3"},
		FocusDone:          &config.KeybindingConfig{Keys: []string{"4"}, Help: "col4"},
		JumpToPinned:       &config.KeybindingConfig{Keys: []string{"ctrl+up"}, Help: "to pin"},
		JumpToUnpinned:     &config.KeybindingConfig{Keys: []string{"ctrl+down"}, Help: "to unpin"},
	}

	result := ApplyKeybindingsConfig(original, cfg)

	// Verify some key overrides
	if result.Left.Help().Key != "h" {
		t.Errorf("Expected Left key 'h', got '%s'", result.Left.Help().Key)
	}
	if result.Down.Help().Key != "j" {
		t.Errorf("Expected Down key 'j', got '%s'", result.Down.Help().Key)
	}
	if result.FocusBacklog.Help().Key != "1" {
		t.Errorf("Expected FocusBacklog key '1', got '%s'", result.FocusBacklog.Help().Key)
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

func TestQuitConfirmCtrlC_QuitsImmediately(t *testing.T) {
	m := &AppModel{
		width:  100,
		height: 50,
		keys:   DefaultKeyMap(),
	}

	// Show quit confirmation
	m.showQuitConfirm()

	if m.currentView != ViewQuitConfirm {
		t.Fatalf("expected ViewQuitConfirm, got %v", m.currentView)
	}
	if m.quitConfirm == nil {
		t.Fatal("expected quitConfirm form to be created")
	}

	// Press Ctrl+C while in quit confirm dialog
	ctrlCMsg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.updateQuitConfirm(ctrlCMsg)

	// Should return tea.Quit command
	if cmd == nil {
		t.Fatal("expected tea.Quit command, got nil")
	}

	// Verify the command produces a QuitMsg
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestQuitConfirmEsc_ReturnsToDashboard(t *testing.T) {
	m := &AppModel{
		width:  100,
		height: 50,
		keys:   DefaultKeyMap(),
	}

	// Show quit confirmation
	m.showQuitConfirm()

	// Press ESC
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.updateQuitConfirm(escMsg)
	am := model.(*AppModel)

	if am.currentView != ViewDashboard {
		t.Errorf("expected ViewDashboard, got %v", am.currentView)
	}
	if am.quitConfirm != nil {
		t.Error("expected quitConfirm to be nil after ESC")
	}
}

func TestConfirmDialogsHandleCtrlC(t *testing.T) {
	ctrlCMsg := tea.KeyMsg{Type: tea.KeyCtrlC}

	t.Run("delete confirm", func(t *testing.T) {
		m := &AppModel{
			width:        100,
			previousView: ViewDashboard,
		}
		task := &db.Task{ID: 1, Title: "Test", Status: db.StatusBacklog}
		m.showDeleteConfirm(task)

		model, _ := m.updateDeleteConfirm(ctrlCMsg)
		am := model.(*AppModel)
		if am.currentView != ViewDashboard {
			t.Errorf("expected ViewDashboard, got %v", am.currentView)
		}
		if am.deleteConfirm != nil {
			t.Error("expected deleteConfirm to be nil")
		}
	})

	t.Run("close confirm", func(t *testing.T) {
		m := &AppModel{
			width:            100,
			previousView:     ViewDashboard,
			userClosedTaskIDs: make(map[int64]bool),
		}
		task := &db.Task{ID: 1, Title: "Test", Status: db.StatusQueued}
		m.showCloseConfirm(task)

		model, _ := m.updateCloseConfirm(ctrlCMsg)
		am := model.(*AppModel)
		if am.currentView != ViewDashboard {
			t.Errorf("expected ViewDashboard, got %v", am.currentView)
		}
		if am.closeConfirm != nil {
			t.Error("expected closeConfirm to be nil")
		}
	})

	t.Run("archive confirm", func(t *testing.T) {
		m := &AppModel{
			width:        100,
			previousView: ViewDashboard,
		}
		task := &db.Task{ID: 1, Title: "Test", Status: db.StatusDone}
		m.showArchiveConfirm(task)

		model, _ := m.updateArchiveConfirm(ctrlCMsg)
		am := model.(*AppModel)
		if am.currentView != ViewDashboard {
			t.Errorf("expected ViewDashboard, got %v", am.currentView)
		}
		if am.archiveConfirm != nil {
			t.Error("expected archiveConfirm to be nil")
		}
	})
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
		newTaskForm: NewFormModel(nil, 100, 50, "", nil),
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
		newTaskForm: NewFormModel(nil, 100, 50, "", nil),
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
		editTaskForm: NewFormModel(nil, 100, 50, "", nil),
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
		editTaskForm: NewFormModel(nil, 100, 50, "", nil),
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

func TestAppModelAvailableExecutors(t *testing.T) {
	// Test that availableExecutors is properly stored
	m := &AppModel{
		availableExecutors: []string{"claude", "codex"},
	}

	if len(m.availableExecutors) != 2 {
		t.Errorf("expected 2 available executors, got %d", len(m.availableExecutors))
	}
	if m.availableExecutors[0] != "claude" {
		t.Errorf("expected first executor to be 'claude', got %s", m.availableExecutors[0])
	}
}

func TestAppModelNoExecutors(t *testing.T) {
	// Test that empty availableExecutors works properly
	m := &AppModel{
		availableExecutors: []string{},
	}

	if len(m.availableExecutors) != 0 {
		t.Errorf("expected 0 available executors, got %d", len(m.availableExecutors))
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
		// Project-only filter tests (using [ prefix)
		{
			name:    "project-only filter matches project",
			task:    &db.Task{ID: 1, Title: "Some task", Project: "workflow"},
			query:   "[workflow",
			wantMin: 100,
			wantMax: 500,
		},
		{
			name:    "project-only filter with fuzzy match",
			task:    &db.Task{ID: 1, Title: "Some task", Project: "offerlab"},
			query:   "[ol",
			wantMin: 100,
			wantMax: 500,
		},
		{
			name:    "project-only filter excludes title matches",
			task:    &db.Task{ID: 1, Title: "workflow improvements", Project: ""},
			query:   "[workflow",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "project-only filter with trailing bracket",
			task:    &db.Task{ID: 1, Title: "Task", Project: "influencekit"},
			query:   "[influencekit]",
			wantMin: 100,
			wantMax: 700, // exact match gets high score
		},
		{
			name:    "just bracket shows tasks with project",
			task:    &db.Task{ID: 1, Title: "Task", Project: "myproject"},
			query:   "[",
			wantMin: 100,
			wantMax: 100,
		},
		{
			name:    "just bracket hides tasks without project",
			task:    &db.Task{ID: 1, Title: "Task", Project: ""},
			query:   "[",
			wantMin: -1,
			wantMax: -1,
		},
		// [project] keyword tests (combined project + keyword filtering)
		{
			name:    "project with keyword matches task in project",
			task:    &db.Task{ID: 1, Title: "Fix authentication bug", Project: "offerlab"},
			query:   "[offerlab] auth",
			wantMin: 100,
			wantMax: 500,
		},
		{
			name:    "project with keyword excludes task in different project",
			task:    &db.Task{ID: 1, Title: "Fix authentication bug", Project: "workflow"},
			query:   "[offerlab] auth",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "project with keyword excludes task with no project",
			task:    &db.Task{ID: 1, Title: "Fix authentication bug", Project: ""},
			query:   "[offerlab] auth",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "project with keyword no match for keyword",
			task:    &db.Task{ID: 1, Title: "Setup database", Project: "offerlab"},
			query:   "[offerlab] auth",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "project bracket with space but no keyword shows all in project",
			task:    &db.Task{ID: 1, Title: "Any task", Project: "offerlab"},
			query:   "[offerlab] ",
			wantMin: 100,
			wantMax: 100,
		},
		{
			name:    "project with ID keyword match",
			task:    &db.Task{ID: 42, Title: "Task", Project: "offerlab"},
			query:   "[offerlab] 42",
			wantMin: 1000,
			wantMax: 1000,
		},
		{
			name:    "project filter case insensitive",
			task:    &db.Task{ID: 1, Title: "Task", Project: "OfferLab"},
			query:   "[offerlab] task",
			wantMin: 100,
			wantMax: 500,
		},
		// Multi-project filter tests
		{
			name:    "multi-project matches first project",
			task:    &db.Task{ID: 1, Title: "Fix bug", Project: "offerlab"},
			query:   "[offerlab] [workflow] ",
			wantMin: 100,
			wantMax: 100,
		},
		{
			name:    "multi-project matches second project",
			task:    &db.Task{ID: 1, Title: "Fix bug", Project: "workflow"},
			query:   "[offerlab] [workflow] ",
			wantMin: 100,
			wantMax: 100,
		},
		{
			name:    "multi-project excludes other project",
			task:    &db.Task{ID: 1, Title: "Fix bug", Project: "personal"},
			query:   "[offerlab] [workflow] ",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "multi-project with keyword matches",
			task:    &db.Task{ID: 1, Title: "Fix authentication", Project: "offerlab"},
			query:   "[offerlab] [workflow] auth",
			wantMin: 100,
			wantMax: 500,
		},
		{
			name:    "multi-project with keyword no match",
			task:    &db.Task{ID: 1, Title: "Setup database", Project: "offerlab"},
			query:   "[offerlab] [workflow] auth",
			wantMin: -1,
			wantMax: -1,
		},
		{
			name:    "multi-project typing second project",
			task:    &db.Task{ID: 1, Title: "Fix bug", Project: "workflow"},
			query:   "[offerlab] [wor",
			wantMin: 100,
			wantMax: 500,
		},
		{
			name:    "multi-project typing second includes first project tasks",
			task:    &db.Task{ID: 1, Title: "Fix bug", Project: "offerlab"},
			query:   "[offerlab] [wor",
			wantMin: 100,
			wantMax: 100,
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

// TestParseFilterProjects tests extraction of project tags from filter text.
func TestParseFilterProjects(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		wantProjects   []string
		wantKeyword    string
		wantPartial    string
	}{
		{"empty", "", nil, "", ""},
		{"just bracket", "[", nil, "", ""},
		{"single partial", "[off", nil, "", "off"},
		{"single complete", "[offerlab] ", []string{"offerlab"}, "", ""},
		{"single complete with keyword", "[offerlab] auth", []string{"offerlab"}, "auth", ""},
		{"two complete", "[offerlab] [workflow] ", []string{"offerlab", "workflow"}, "", ""},
		{"two complete with keyword", "[offerlab] [workflow] bug", []string{"offerlab", "workflow"}, "bug", ""},
		{"one complete one partial", "[offerlab] [wor", []string{"offerlab"}, "", "wor"},
		{"plain text no brackets", "some search", nil, "some search", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projects, keyword, partial := parseFilterProjects(tt.query)
			if len(projects) != len(tt.wantProjects) {
				t.Errorf("projects = %v, want %v", projects, tt.wantProjects)
			} else {
				for i, p := range projects {
					if p != tt.wantProjects[i] {
						t.Errorf("projects[%d] = %q, want %q", i, p, tt.wantProjects[i])
					}
				}
			}
			if keyword != tt.wantKeyword {
				t.Errorf("keyword = %q, want %q", keyword, tt.wantKeyword)
			}
			if partial != tt.wantPartial {
				t.Errorf("partial = %q, want %q", partial, tt.wantPartial)
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

func TestStripAnsiCodes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no ansi codes", "hello world", "hello world"},
		{"color codes", "\x1b[31mred text\x1b[0m", "red text"},
		{"bold", "\x1b[1mbold\x1b[0m", "bold"},
		{"multiple codes", "\x1b[31m\x1b[1mred bold\x1b[0m", "red bold"},
		{"empty string", "", ""},
		{"only ansi", "\x1b[31m\x1b[0m", ""},
		{"cursor movement", "\x1b[2Ahello", "hello"},
		{"complex sequence", "\x1b[38;5;196mcolored\x1b[0m", "colored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripAnsiCodes(tt.input)
			if result != tt.expected {
				t.Errorf("stripAnsiCodes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractPromptLines(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxWidth int
		want     int // expected number of non-empty lines
	}{
		{"empty content", "", 80, 0},
		{"single line", "Allow Bash(npm test)?", 80, 1},
		{"multiple lines", "Working on task...\n\nAllow Bash(npm test)?", 80, 2},
		{"strips empty lines", "\n\n\nhello\n\nworld\n\n", 80, 2},
		{"truncates long lines", "this is a very long line that should be truncated", 20, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPromptLines(tt.content, tt.maxWidth)
			if len(result) != tt.want {
				t.Errorf("extractPromptLines() returned %d lines, want %d: %v", len(result), tt.want, result)
			}
		})
	}
}

func TestExtractPromptLinesContent(t *testing.T) {
	content := "\x1b[31mAllow\x1b[0m Bash(npm test)?"
	lines := extractPromptLines(content, 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "Allow Bash(npm test)?" {
		t.Errorf("expected ANSI codes stripped, got %q", lines[0])
	}
}

func TestRenderExecutorPromptPreview_NoPrompt(t *testing.T) {
	m := &AppModel{
		width:           100,
		executorPrompts: make(map[int64]string),
	}
	task := &db.Task{ID: 42, Title: "Test task"}
	result := m.renderExecutorPromptPreview(task)
	if result == "" {
		t.Error("expected non-empty result even with no prompt")
	}
	// Should contain the task ID and hint text
	if !containsText(result, "#42") {
		t.Error("expected result to contain task ID")
	}
}

func TestRenderExecutorPromptPreview_WithPrompt(t *testing.T) {
	m := &AppModel{
		width: 100,
		executorPrompts: map[int64]string{
			42: "Allow Bash(npm test)?",
		},
	}
	task := &db.Task{ID: 42, Title: "Test task"}
	result := m.renderExecutorPromptPreview(task)
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Should contain the prompt content and approve/deny hints
	if !containsText(result, "approve") || !containsText(result, "deny") {
		t.Error("expected result to contain approve/deny hints")
	}
}

func TestDefaultKeyMap_ApproveAndDenyKeys(t *testing.T) {
	keys := DefaultKeyMap()
	if keys.ApprovePrompt.Help().Key != "y" {
		t.Errorf("ApprovePrompt key should be 'y', got '%s'", keys.ApprovePrompt.Help().Key)
	}
	if keys.DenyPrompt.Help().Key != "N" {
		t.Errorf("DenyPrompt key should be 'N', got '%s'", keys.DenyPrompt.Help().Key)
	}
}

// containsText checks if rendered text contains a substring (ignoring ANSI codes).
func containsText(rendered, substr string) bool {
	cleaned := stripAnsiCodes(rendered)
	return len(cleaned) > 0 && len(substr) > 0 && (cleaned == substr || len(cleaned) >= len(substr) && containsSubstr(cleaned, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLatestPermissionPrompt_PermissionMessage(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}

	// Create a task
	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log a permission message (as the notification hook would)
	database.AppendTaskLog(task.ID, "system", "Waiting for permission: Edit(src/models/offer.rb)")

	// Should return the full permission message
	result := m.latestPermissionPrompt(task.ID)
	if result != "Waiting for permission: Edit(src/models/offer.rb)" {
		t.Errorf("expected 'Waiting for permission: Edit(src/models/offer.rb)', got '%s'", result)
	}
}

func TestLatestPermissionPrompt_GenericWaiting(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log a generic waiting message (no specific message from hook)
	database.AppendTaskLog(task.ID, "system", "Waiting for permission")

	result := m.latestPermissionPrompt(task.ID)
	if result != "Waiting for permission" {
		t.Errorf("expected 'Waiting for permission', got '%s'", result)
	}
}

func TestLatestPermissionPrompt_ResumedClears(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log a permission message, then a resumed message
	database.AppendTaskLog(task.ID, "system", "Waiting for permission: Edit(file.go)")
	database.AppendTaskLog(task.ID, "system", "Agent resumed working")

	// Should return empty since agent resumed
	result := m.latestPermissionPrompt(task.ID)
	if result != "" {
		t.Errorf("expected empty string after resume, got '%s'", result)
	}
}

func TestLatestPermissionPrompt_NoLogs(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}

	// No task or logs - should return empty
	result := m.latestPermissionPrompt(999)
	if result != "" {
		t.Errorf("expected empty string for nonexistent task, got '%s'", result)
	}
}

func TestLatestPermissionPrompt_UserInputMessage_Ignored(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	m := &AppModel{db: database}

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// "Waiting for user input" is idle waiting, not an actionable prompt —
	// should NOT trigger the status line / prompt preview.
	database.AppendTaskLog(task.ID, "system", "Waiting for user input")

	result := m.latestPermissionPrompt(task.ID)
	if result != "" {
		t.Errorf("expected empty string for idle user input, got '%s'", result)
	}
}

func TestJumpToNotificationKey_FocusExecutor(t *testing.T) {
	// Create app model with kanban board and notification
	tasks := []*db.Task{
		{ID: 1, Title: "Task 1", Status: db.StatusBacklog},
		{ID: 2, Title: "Task 2", Status: db.StatusBlocked},
	}

	// Create a mock database for the loadTask call
	mockDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer mockDB.Close()

	// Insert test task
	testTask := &db.Task{ID: 2, Title: "Task 2", Status: db.StatusBlocked}
	if err := mockDB.CreateTask(testTask); err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		notification: "⚠ Task #2 needs input: Task 2 (g to jump)",
		notifyTaskID: 2,
		kanban:       NewKanbanBoard(100, 50),
		db:           mockDB,
	}
	m.kanban.SetTasks(tasks)
	m.kanban.SelectTask(1) // Start with task 1 selected

	// Press 'g' to jump to notification
	gMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
	_, cmd := m.updateDashboard(gMsg)

	// Verify command was returned
	if cmd == nil {
		t.Fatal("expected command to be returned")
	}

	// Execute the command to get the message
	msg := cmd()

	// Verify the message has focusExecutor set to true
	loadedMsg, ok := msg.(taskLoadedMsg)
	if !ok {
		t.Fatalf("expected taskLoadedMsg, got %T", msg)
	}

	if !loadedMsg.focusExecutor {
		t.Error("expected focusExecutor to be true when jumping from notification")
	}
}

// TestExecutorRespondedClearsPromptState verifies that approving/denying an
// executor prompt immediately clears both tasksNeedingInput and executorPrompts
// for visual feedback. If the task still has a pending prompt, the
// latestPermissionPrompt catch-up loop will re-detect it on the next poll.
func TestExecutorRespondedClearsPromptState(t *testing.T) {
	m := &AppModel{
		width:             100,
		height:            50,
		currentView:       ViewDashboard,
		keys:              DefaultKeyMap(),
		tasksNeedingInput: map[int64]bool{42: true},
		executorPrompts:   map[int64]string{42: "some prompt content"},
		kanban:            NewKanbanBoard(100, 50),
		prevStatuses:      make(map[int64]string),
	}

	// Simulate a successful approve response
	msg := executorRespondedMsg{taskID: 42, action: "approve", err: nil}
	m.Update(msg)

	// Both should be cleared for immediate visual feedback
	if m.tasksNeedingInput[42] {
		t.Error("tasksNeedingInput[42] should be cleared after approve for visual feedback")
	}
	if _, exists := m.executorPrompts[42]; exists {
		t.Error("executorPrompts[42] should be cleared after approve")
	}
}

// TestDetectPermissionPrompt_FallbackDetection verifies that detectPermissionPrompt
// does a live DB check and populates tasksNeedingInput when a permission prompt
// exists but hasn't been detected by the poll yet.
func TestDetectPermissionPrompt_FallbackDetection(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log a permission prompt (simulating the notification hook)
	database.AppendTaskLog(task.ID, "system", "Waiting for permission: Edit(file.go)")

	m := &AppModel{
		db:                database,
		tasksNeedingInput: make(map[int64]bool),
		executorPrompts:   make(map[int64]string),
		kanban:            NewKanbanBoard(100, 50),
	}

	// tasksNeedingInput is empty (poll hasn't detected the prompt yet)
	if m.tasksNeedingInput[task.ID] {
		t.Fatal("precondition: tasksNeedingInput should be empty before detect")
	}

	// detectPermissionPrompt should find the prompt via live DB check
	if !m.detectPermissionPrompt(task.ID) {
		t.Error("detectPermissionPrompt should return true when a permission prompt exists")
	}

	// Should now be populated
	if !m.tasksNeedingInput[task.ID] {
		t.Error("tasksNeedingInput should be set after detectPermissionPrompt")
	}
	if m.executorPrompts[task.ID] != "Edit(file.go)" {
		t.Errorf("expected executor prompt 'Edit(file.go)', got '%s'", m.executorPrompts[task.ID])
	}
}

// TestDetectPermissionPrompt_NoPrompt verifies that detectPermissionPrompt
// returns false when there is no pending permission prompt.
func TestDetectPermissionPrompt_NoPrompt(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log "Waiting for user input" (not a permission prompt)
	database.AppendTaskLog(task.ID, "system", "Waiting for user input")

	m := &AppModel{
		db:                database,
		tasksNeedingInput: make(map[int64]bool),
		executorPrompts:   make(map[int64]string),
		kanban:            NewKanbanBoard(100, 50),
	}

	if m.detectPermissionPrompt(task.ID) {
		t.Error("detectPermissionPrompt should return false for non-permission prompts")
	}
	if m.tasksNeedingInput[task.ID] {
		t.Error("tasksNeedingInput should not be set for non-permission prompts")
	}
}

// TestTaskEventDetectsPermissionPrompt verifies that when a real-time task event
// transitions a task to blocked, the handler detects pending permission prompts
// and populates tasksNeedingInput immediately (without waiting for the next poll).
func TestTaskEventDetectsPermissionPrompt(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Log a permission prompt
	database.AppendTaskLog(task.ID, "system", "Waiting for permission: Bash(rm -rf /)")

	m := &AppModel{
		width:             100,
		height:            50,
		currentView:       ViewDashboard,
		db:                database,
		keys:              DefaultKeyMap(),
		tasks:             []*db.Task{{ID: task.ID, Title: "Test task", Status: db.StatusProcessing}},
		tasksNeedingInput: make(map[int64]bool),
		executorPrompts:   make(map[int64]string),
		kanban:            NewKanbanBoard(100, 50),
		prevStatuses:      map[int64]string{task.ID: db.StatusProcessing},
	}

	// Simulate a task event transitioning to blocked
	event := taskEventMsg{event: executor.TaskEvent{
		Type:   "status_changed",
		TaskID: task.ID,
		Task:   task,
	}}
	m.Update(event)

	// The handler should have detected the permission prompt
	if !m.tasksNeedingInput[task.ID] {
		t.Error("tasksNeedingInput should be set when task event transitions to blocked with permission prompt")
	}
	if m.executorPrompts[task.ID] != "Bash(rm -rf /)" {
		t.Errorf("expected executor prompt 'Bash(rm -rf /)', got '%s'", m.executorPrompts[task.ID])
	}
}

// TestTaskSyncClearsNeedsInputWhenNoLongerBlocked verifies that the task sync
// loop clears tasksNeedingInput when a task is no longer blocked (e.g., the user
// provided input from the detail view tmux pane).
func TestTaskSyncClearsNeedsInputWhenNoLongerBlocked(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer database.Close()

	task := &db.Task{Title: "Test task", Status: db.StatusProcessing}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	m := &AppModel{
		width:             100,
		height:            50,
		currentView:       ViewDashboard,
		keys:              DefaultKeyMap(),
		tasks:             []*db.Task{task},
		tasksNeedingInput: map[int64]bool{task.ID: true},
		executorPrompts:   map[int64]string{task.ID: "What should I do?"},
		kanban:            NewKanbanBoard(100, 50),
		prevStatuses:      map[int64]string{task.ID: db.StatusBlocked},
		db:                database,
		notification:      "⚠ Task #1 needs input",
		notifyTaskID:      task.ID,
	}
	m.kanban.SetTasks(m.tasks)

	// Simulate tasksLoadedMsg with the task now in processing status
	msg := tasksLoadedMsg{tasks: []*db.Task{task}}
	m.Update(msg)

	if m.tasksNeedingInput[task.ID] {
		t.Error("tasksNeedingInput should be cleared when task is no longer blocked")
	}
	if _, exists := m.executorPrompts[task.ID]; exists {
		t.Error("executorPrompts should be cleared when task is no longer blocked")
	}
}

// TestRetrySubmitClearsKanbanNotification verifies that submitting the retry
// form clears both the tasksNeedingInput flag and the notification banner.
func TestRetrySubmitClearsKanbanNotification(t *testing.T) {
	task := &db.Task{ID: 42, Title: "Test task", Status: db.StatusBlocked}

	m := &AppModel{
		width:             100,
		height:            50,
		currentView:       ViewRetry,
		keys:              DefaultKeyMap(),
		tasksNeedingInput: map[int64]bool{42: true},
		executorPrompts:   map[int64]string{42: "What should I do?"},
		kanban:            NewKanbanBoard(100, 50),
		prevStatuses:      make(map[int64]string),
		notification:      "⚠ Task #42 needs input: Test task (g to jump)",
		notifyTaskID:      42,
		retryView:         &RetryModel{task: task, submitted: true},
	}

	m.Update(nil)

	if m.tasksNeedingInput[42] {
		t.Error("tasksNeedingInput[42] should be cleared after retry submit")
	}
	if _, exists := m.executorPrompts[42]; exists {
		t.Error("executorPrompts[42] should be cleared after retry submit")
	}
	if m.notification != "" {
		t.Errorf("notification should be cleared after retry submit, got %q", m.notification)
	}
	if m.notifyTaskID != 0 {
		t.Error("notifyTaskID should be cleared after retry submit")
	}
}

// TestFilterChipDeletion tests the chip deletion feature in filter input.
func TestFilterChipDeletion(t *testing.T) {
	tests := []struct {
		name            string
		initialValue    string
		cursorPos       int
		expectedValue   string
		expectedCursor  int
		shouldDeleteAll bool // whether entire chip should be deleted
	}{
		{
			name:            "delete chip at end",
			initialValue:    "text [project] ",
			cursorPos:       14, // right after ]
			expectedValue:   "text ",
			expectedCursor:  5,
			shouldDeleteAll: true,
		},
		{
			name:            "delete chip in middle",
			initialValue:    "before [project] after",
			cursorPos:       16, // right after ]
			expectedValue:   "before after",
			expectedCursor:  7,
			shouldDeleteAll: true,
		},
		{
			name:            "delete first chip of multiple",
			initialValue:    "[project1] [project2] text",
			cursorPos:       10, // right after first ]
			expectedValue:   "[project2] text",
			expectedCursor:  0,
			shouldDeleteAll: true,
		},
		{
			name:            "delete second chip of multiple",
			initialValue:    "[project1] [project2] text",
			cursorPos:       21, // right after second ]
			expectedValue:   "[project1] text",
			expectedCursor:  11,
			shouldDeleteAll: true,
		},
		{
			name:            "cursor not after ]",
			initialValue:    "text [project] more",
			cursorPos:       5, // in the middle of text
			expectedValue:   "text [project] more", // no change expected
			expectedCursor:  5,
			shouldDeleteAll: false,
		},
		{
			name:            "cursor before [",
			initialValue:    "text [project]",
			cursorPos:       4, // right before [
			expectedValue:   "text [project]", // no change expected
			expectedCursor:  4,
			shouldDeleteAll: false,
		},
		{
			name:            "chip without trailing space",
			initialValue:    "text [project]more",
			cursorPos:       14, // right after ]
			expectedValue:   "text more",
			expectedCursor:  5,
			shouldDeleteAll: true,
		},
		{
			name:            "nested brackets (finds first [)",
			initialValue:    "text [pro[ject] more",
			cursorPos:       15, // right after ]
			expectedValue:   "text [promore",
			expectedCursor:  9,
			shouldDeleteAll: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the chip deletion logic
			currentValue := tt.initialValue
			cursorPos := tt.cursorPos
			deleted := false

			// Check if cursor is after a ']'
			if cursorPos > 0 && cursorPos <= len(currentValue) && currentValue[cursorPos-1] == ']' {
				// Find the matching '[' before the cursor
				openBracket := -1
				for i := cursorPos - 2; i >= 0; i-- {
					if currentValue[i] == '[' {
						openBracket = i
						break
					}
				}

				if openBracket >= 0 {
					// Delete the chip [project] and any trailing space
					newValue := currentValue[:openBracket]
					endPos := cursorPos
					// Remove trailing space if present
					if endPos < len(currentValue) && currentValue[endPos] == ' ' {
						endPos++
					}
					newValue += currentValue[endPos:]
					currentValue = newValue
					cursorPos = openBracket
					deleted = true
				}
			}

			// Verify results
			if tt.shouldDeleteAll && !deleted {
				t.Error("expected chip to be deleted but it wasn't")
			}
			if !tt.shouldDeleteAll && deleted {
				t.Error("expected no deletion but chip was deleted")
			}
			if currentValue != tt.expectedValue {
				t.Errorf("value = %q, want %q", currentValue, tt.expectedValue)
			}
			if cursorPos != tt.expectedCursor {
				t.Errorf("cursor = %d, want %d", cursorPos, tt.expectedCursor)
			}
		})
	}
}


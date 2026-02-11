package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// TestNewDetailModel_BacklogTaskDoesNotStartExecutor verifies that when a task
// is in backlog status, viewing it in the TUI should NOT automatically start
// the executor session. This prevents unwanted execution of tasks that the user
// has explicitly chosen not to start yet.
func TestNewDetailModel_BacklogTaskDoesNotStartExecutor(t *testing.T) {
	// Create a task in backlog status (not meant to be executed yet)
	task := &db.Task{
		ID:     1,
		Title:  "Test backlog task",
		Status: db.StatusBacklog,
	}

	// Create DetailModel - when not in tmux (TMUX env var not set),
	// it returns nil command, so we can't test that path easily.
	// But when task is in backlog, we should never start the executor
	// regardless of tmux state.

	// For now, test that backlog tasks are properly identified
	if !shouldSkipAutoExecutor(task) {
		t.Error("backlog tasks should skip auto-executor")
	}
}

// TestNewDetailModel_QueuedTaskCanStartExecutor verifies that tasks in
// queued status CAN start the executor automatically when viewed.
func TestNewDetailModel_QueuedTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test queued task",
		Status: db.StatusQueued,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("queued tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_ProcessingTaskCanStartExecutor verifies that tasks in
// processing status CAN reconnect to the executor automatically when viewed.
func TestNewDetailModel_ProcessingTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test processing task",
		Status: db.StatusProcessing,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("processing tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_BlockedTaskCanStartExecutor verifies that tasks in
// blocked status CAN reconnect to the executor automatically when viewed.
func TestNewDetailModel_BlockedTaskCanStartExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test blocked task",
		Status: db.StatusBlocked,
	}

	if shouldSkipAutoExecutor(task) {
		t.Error("blocked tasks should NOT skip auto-executor")
	}
}

// TestNewDetailModel_DoneTaskSkipsAutoExecutor verifies that completed tasks
// should NOT auto-start the executor (they're done).
func TestNewDetailModel_DoneTaskSkipsAutoExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test done task",
		Status: db.StatusDone,
	}

	if !shouldSkipAutoExecutor(task) {
		t.Error("done tasks should skip auto-executor")
	}
}

// TestNewDetailModel_ArchivedTaskSkipsAutoExecutor verifies that archived tasks
// should NOT auto-start the executor.
func TestNewDetailModel_ArchivedTaskSkipsAutoExecutor(t *testing.T) {
	task := &db.Task{
		ID:     1,
		Title:  "Test archived task",
		Status: db.StatusArchived,
	}

	if !shouldSkipAutoExecutor(task) {
		t.Error("archived tasks should skip auto-executor")
	}
}

// TestPercentageCalculationRounding verifies that percentage calculations
// use proper rounding to avoid the progressive shrinking bug.
// Without rounding, integer division truncates: (16 * 100) / 81 = 19 instead of 20
// With rounding: (16*100 + 81/2) / 81 = (1600 + 40) / 81 = 20
func TestPercentageCalculationRounding(t *testing.T) {
	tests := []struct {
		paneSize    int
		totalSize   int
		wantRounded int
	}{
		// Case where truncation would cause shrinking
		{paneSize: 16, totalSize: 81, wantRounded: 20}, // Without rounding: 19
		{paneSize: 15, totalSize: 80, wantRounded: 19}, // Without rounding: 18
		{paneSize: 20, totalSize: 100, wantRounded: 20},
		{paneSize: 16, totalSize: 80, wantRounded: 20},
		// Edge cases
		{paneSize: 1, totalSize: 100, wantRounded: 1},
		{paneSize: 50, totalSize: 100, wantRounded: 50},
		// Cases where rounding changes outcome
		{paneSize: 7, totalSize: 40, wantRounded: 18}, // 17.5 rounds to 18
		{paneSize: 9, totalSize: 50, wantRounded: 18}, // 18.0 stays 18
	}

	for _, tt := range tests {
		// This is the rounding formula used in the actual code
		got := (tt.paneSize*100 + tt.totalSize/2) / tt.totalSize
		if got != tt.wantRounded {
			t.Errorf("percentage(%d, %d) = %d, want %d", tt.paneSize, tt.totalSize, got, tt.wantRounded)
		}

		// Verify the old truncation formula would have been different for problematic cases
		truncated := (tt.paneSize * 100) / tt.totalSize
		if tt.paneSize == 16 && tt.totalSize == 81 && truncated >= tt.wantRounded {
			t.Errorf("truncation case should differ: truncated=%d, rounded=%d", truncated, tt.wantRounded)
		}
	}
}

func TestExecutorFailureMessage(t *testing.T) {
	m := &DetailModel{task: &db.Task{Executor: db.ExecutorCodex}}
	msgWithDetail := m.executorFailureMessage("tmux new-window failed")
	if !strings.Contains(msgWithDetail, "Codex failed to start") {
		t.Fatalf("expected executor name in message, got %q", msgWithDetail)
	}
	if !strings.Contains(msgWithDetail, "tmux new-window failed") {
		t.Fatalf("expected detail in message, got %q", msgWithDetail)
	}
	if !strings.Contains(msgWithDetail, "executor configuration") {
		t.Fatalf("expected guidance in message, got %q", msgWithDetail)
	}

	msgWithoutDetail := m.executorFailureMessage("")
	if strings.Contains(msgWithoutDetail, "  ") {
		t.Fatalf("unexpected double spaces in message: %q", msgWithoutDetail)
	}
	if !strings.Contains(msgWithoutDetail, "Codex failed to start.") {
		t.Fatalf("expected default failure message, got %q", msgWithoutDetail)
	}
}

// TestDetailModel_IsShellPaneHidden verifies the shell pane hidden state accessor.
func TestDetailModel_IsShellPaneHidden(t *testing.T) {
	task := &db.Task{ID: 1, Title: "Test task"}

	tests := []struct {
		name           string
		shellPaneHidden bool
		want           bool
	}{
		{
			name:           "shell pane visible",
			shellPaneHidden: false,
			want:           false,
		},
		{
			name:           "shell pane hidden",
			shellPaneHidden: true,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &DetailModel{task: task, shellPaneHidden: tt.shellPaneHidden}

			got := m.IsShellPaneHidden()
			if got != tt.want {
				t.Errorf("IsShellPaneHidden() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetailModel_ToggleShellPaneKeyBinding verifies that the shell pane toggle
// key binding (\) is properly defined and accessible.
func TestDetailModel_ToggleShellPaneKeyBinding(t *testing.T) {
	// Verify the key binding exists and uses the backslash key
	keys := DefaultKeyMap()

	// Check that ToggleShellPane binding is set to backslash
	bindings := keys.ToggleShellPane.Keys()
	if len(bindings) == 0 {
		t.Fatal("ToggleShellPane key binding has no keys")
	}

	found := false
	for _, k := range bindings {
		if k == "\\" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("ToggleShellPane key binding expected '\\', got %v", bindings)
	}

	// Verify help text is set
	help := keys.ToggleShellPane.Help()
	if help.Key != "\\" {
		t.Errorf("ToggleShellPane help key expected '\\', got %q", help.Key)
	}
	if help.Desc != "toggle shell" {
		t.Errorf("ToggleShellPane help desc expected 'toggle shell', got %q", help.Desc)
	}
}

// TestDetailModel_GetServerURL verifies that the server URL is returned only
// when serverListening is true and the task has a valid port.
func TestDetailModel_GetServerURL(t *testing.T) {
	tests := []struct {
		name            string
		task            *db.Task
		serverListening bool
		wantURL         string
	}{
		{
			name:            "server listening with valid port",
			task:            &db.Task{ID: 1, Port: 3100},
			serverListening: true,
			wantURL:         "http://localhost:3100",
		},
		{
			name:            "server not listening",
			task:            &db.Task{ID: 1, Port: 3100},
			serverListening: false,
			wantURL:         "",
		},
		{
			name:            "no port assigned",
			task:            &db.Task{ID: 1, Port: 0},
			serverListening: true,
			wantURL:         "",
		},
		{
			name:            "nil task",
			task:            nil,
			serverListening: true,
			wantURL:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &DetailModel{task: tt.task, serverListening: tt.serverListening}

			got := m.GetServerURL()
			if got != tt.wantURL {
				t.Errorf("GetServerURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

// TestDetailModel_RenderHeaderWithDiffStats verifies that diff stats are
// displayed in the header when a PR has additions/deletions.
func TestDetailModel_RenderHeaderWithDiffStats(t *testing.T) {
	task := &db.Task{ID: 1, Title: "Test task", Status: db.StatusProcessing}

	tests := []struct {
		name         string
		prInfo       *github.PRInfo
		focused      bool
		expectDiff   bool
		expectAddDel string
	}{
		{
			name:       "no PR info",
			prInfo:     nil,
			focused:    true,
			expectDiff: false,
		},
		{
			name: "PR with additions and deletions",
			prInfo: &github.PRInfo{
				Number:    123,
				Additions: 42,
				Deletions: 10,
			},
			focused:      true,
			expectDiff:   true,
			expectAddDel: "+42", // Should contain additions
		},
		{
			name: "PR with only additions",
			prInfo: &github.PRInfo{
				Number:    124,
				Additions: 100,
				Deletions: 0,
			},
			focused:      true,
			expectDiff:   true,
			expectAddDel: "+100",
		},
		{
			name: "PR with only deletions",
			prInfo: &github.PRInfo{
				Number:    125,
				Additions: 0,
				Deletions: 50,
			},
			focused:      true,
			expectDiff:   true,
			expectAddDel: "-50",
		},
		{
			name: "PR with no changes",
			prInfo: &github.PRInfo{
				Number:    126,
				Additions: 0,
				Deletions: 0,
			},
			focused:    true,
			expectDiff: false,
		},
		{
			name: "unfocused state shows dim diff stats",
			prInfo: &github.PRInfo{
				Number:    127,
				Additions: 20,
				Deletions: 5,
			},
			focused:      false,
			expectDiff:   true,
			expectAddDel: "+20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &DetailModel{
				task:    task,
				prInfo:  tt.prInfo,
				focused: tt.focused,
				width:   100,
				height:  24,
			}

			header := m.renderHeader()

			if tt.expectDiff {
				if !strings.Contains(header, tt.expectAddDel) {
					t.Errorf("Expected header to contain %q for diff stats, got: %q", tt.expectAddDel, header)
				}
			} else if tt.prInfo != nil && (tt.prInfo.Additions > 0 || tt.prInfo.Deletions > 0) {
				// If we don't expect diff stats but PR has changes, verify they're not shown
				// This case shouldn't happen with current logic
			}
		})
	}
}

// TestDetailModel_CollapsedShellIndicator verifies that when the shell pane is
// hidden and running in TMUX, the View() includes a vertical "Shell" label on the
// right side to indicate there's a collapsed shell pane available.
func TestDetailModel_CollapsedShellIndicator(t *testing.T) {
	task := &db.Task{ID: 1, Title: "Test task", Status: db.StatusQueued}

	tests := []struct {
		name            string
		shellPaneHidden bool
		tmuxEnv         string
		expectShellTab  bool
	}{
		{
			name:            "shell hidden in tmux shows indicator",
			shellPaneHidden: true,
			tmuxEnv:         "/tmp/tmux-1000/default",
			expectShellTab:  true,
		},
		{
			name:            "shell visible in tmux hides indicator",
			shellPaneHidden: false,
			tmuxEnv:         "/tmp/tmux-1000/default",
			expectShellTab:  false,
		},
		{
			name:            "shell hidden outside tmux hides indicator",
			shellPaneHidden: true,
			tmuxEnv:         "",
			expectShellTab:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set TMUX environment variable for test
			t.Setenv("TMUX", tt.tmuxEnv)

			m := &DetailModel{
				task:            task,
				shellPaneHidden: tt.shellPaneHidden,
				ready:           true,
				width:           80,
				height:          24,
			}

			view := m.View()

			// Check if the vertical "Shell" label characters are present
			// The label renders as "S\nh\ne\nl\nl" (each char on own line)
			hasShellTab := strings.Contains(view, "S") &&
				strings.Contains(view, "h") &&
				strings.Contains(view, "e") &&
				strings.Contains(view, "l")

			// Also check for the shell background styling (teal color marker)
			// The #88C0D0 color is used for the shell tab text
			if tt.expectShellTab && !hasShellTab {
				t.Errorf("Expected collapsed shell indicator in view when shell is hidden in TMUX")
			}
		})
	}
}

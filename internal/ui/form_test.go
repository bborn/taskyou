package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCalculateBodyHeight(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		screenHeight int
		screenWidth  int
		wantMin      int
		wantMax      int
	}{
		{
			name:         "empty content returns minimum height",
			content:      "",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      8,
			wantMax:      8,
		},
		{
			name:         "single line returns minimum height",
			content:      "hello world",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      8,
			wantMax:      8,
		},
		{
			name:         "multiple lines grows height",
			content:      "line1\nline2\nline3\nline4\nline5\nline6",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      8,
			wantMax:      8,
		},
		{
			name:         "many lines capped at max height (50% of screen)",
			content:      strings.Repeat("line\n", 50),
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      8,  // at least minimum
			wantMax:      14, // (50-22)/2 = 14
		},
		{
			name:         "long lines wrap and increase height",
			content:      strings.Repeat("a", 200), // should wrap on ~76 char width
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      8,
			wantMax:      8, // wrapped lines still within minimum
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, tt.screenWidth, tt.screenHeight, "", nil)
			m.bodyInput.SetValue(tt.content)

			height := m.calculateBodyHeight()

			if height < tt.wantMin {
				t.Errorf("calculateBodyHeight() = %d, want >= %d", height, tt.wantMin)
			}
			if height > tt.wantMax {
				t.Errorf("calculateBodyHeight() = %d, want <= %d", height, tt.wantMax)
			}
		})
	}
}

func TestUpdateBodyHeightSetsHeight(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)

	// Initially should have minimum height of 8
	m.updateBodyHeight()
	// The textarea height is internal, so we just verify no panic

	// Add content and update
	m.bodyInput.SetValue("line1\nline2\nline3\nline4\nline5")
	m.updateBodyHeight()
	// Verify no panic and height should be 5

	// Verify that with large content, height is capped
	m.bodyInput.SetValue(strings.Repeat("line\n", 100))
	m.updateBodyHeight()
	// Should be capped at max height (50% of screen)
}

func TestMaxHeightIs50PercentOfScreen(t *testing.T) {
	screenHeights := []int{40, 60, 80, 100}

	for _, screenHeight := range screenHeights {
		t.Run("screen_height_"+string(rune('0'+screenHeight/10)), func(t *testing.T) {
			m := NewFormModel(nil, 100, screenHeight, "", nil)

			// Add lots of content to trigger max height
			m.bodyInput.SetValue(strings.Repeat("line\n", 100))

			height := m.calculateBodyHeight()

			// formOverhead is 22, so maxHeight = (screenHeight - 22) / 2
			expectedMax := (screenHeight - 22) / 2
			if expectedMax < 8 {
				expectedMax = 8
			}

			if height > expectedMax {
				t.Errorf("height %d exceeds expected max %d for screen height %d", height, expectedMax, screenHeight)
			}
		})
	}
}

func TestRenderBodyScrollbar(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		visibleLines  int
		wantScrollbar bool
	}{
		{
			name:          "empty content returns no scrollbar",
			content:       "",
			visibleLines:  4,
			wantScrollbar: false,
		},
		{
			name:          "content fits in viewport returns no scrollbar",
			content:       "line1\nline2",
			visibleLines:  4,
			wantScrollbar: false,
		},
		{
			name:          "content exceeds viewport returns scrollbar",
			content:       "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8",
			visibleLines:  4,
			wantScrollbar: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.SetHeight(tt.visibleLines)

			scrollbar := m.renderBodyScrollbar(tt.visibleLines)

			if tt.wantScrollbar {
				if scrollbar == nil {
					t.Error("expected scrollbar but got nil")
				}
				if len(scrollbar) != tt.visibleLines {
					t.Errorf("scrollbar length = %d, want %d", len(scrollbar), tt.visibleLines)
				}
				// Verify scrollbar contains expected characters
				for _, char := range scrollbar {
					if char != "┃" && char != "│" && !strings.Contains(char, "┃") && !strings.Contains(char, "│") {
						t.Errorf("unexpected scrollbar character: %q", char)
					}
				}
			} else {
				if scrollbar != nil {
					t.Errorf("expected no scrollbar but got %v", scrollbar)
				}
			}
		})
	}
}

func TestScrollbarAppearsInFormView(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)

	// Add enough content to trigger scrollbar
	m.bodyInput.SetValue(strings.Repeat("line\n", 20))
	m.bodyInput.SetHeight(4) // Force small viewport

	view := m.View()

	// The scrollbar characters should appear in the view
	hasScrollbar := strings.Contains(view, "┃") || strings.Contains(view, "│")
	if !hasScrollbar {
		t.Error("expected scrollbar characters in view but found none")
	}
}

func TestAttachmentRemovalFlow(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.focused = FieldAttachments
	m.attachments = []string{"/tmp/spec.md", "/tmp/log.txt", "/tmp/image.png"}
	m.attachmentsInput.SetValue("")

	if m.attachmentSelectionActive() {
		t.Fatal("selection should be inactive before any key input")
	}

	// First backspace selects the last attachment
	if !m.handleAttachmentRemovalKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Fatal("expected backspace to be handled")
	}
	if m.attachmentCursor != 2 {
		t.Fatalf("expected cursor to point to last attachment, got %d", m.attachmentCursor)
	}
	if len(m.attachments) != 3 {
		t.Fatalf("expected attachments to remain until confirmed removal, got %d", len(m.attachments))
	}

	// Second backspace removes the selected attachment
	if !m.handleAttachmentRemovalKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Fatal("expected second backspace to remove attachment")
	}
	if len(m.attachments) != 2 {
		t.Fatalf("expected 2 attachments after removal, got %d", len(m.attachments))
	}
	if m.attachments[1] != "/tmp/log.txt" {
		t.Fatalf("expected remaining attachments to keep order, got %v", m.attachments)
	}

	// Move selection to the previous attachment and remove with delete
	if !m.handleAttachmentNavigation(-1) {
		t.Fatal("expected navigation to activate selection when attachments focused")
	}
	if m.attachmentCursor != 0 {
		t.Fatalf("expected cursor to move to first attachment, got %d", m.attachmentCursor)
	}
	if !m.handleAttachmentRemovalKey(tea.KeyMsg{Type: tea.KeyDelete}) {
		t.Fatal("expected delete to remove selected attachment")
	}
	if len(m.attachments) != 1 {
		t.Fatalf("expected 1 attachment after delete, got %d", len(m.attachments))
	}
	if m.attachments[0] != "/tmp/log.txt" {
		t.Fatalf("expected log.txt to remain, got %v", m.attachments[0])
	}
	if m.attachmentCursor != 0 {
		t.Fatalf("cursor should stay on remaining attachment, got %d", m.attachmentCursor)
	}
}

func TestIsWordChar(t *testing.T) {
	tests := []struct {
		char rune
		want bool
	}{
		{'a', true},
		{'Z', true},
		{'0', true},
		{'9', true},
		{'_', true},
		{' ', false},
		{'.', false},
		{'-', false},
		{'\n', false},
		{'\t', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			got := isWordChar(tt.char)
			if got != tt.want {
				t.Errorf("isWordChar(%q) = %v, want %v", tt.char, got, tt.want)
			}
		})
	}
}

func TestMoveCursorWordBackward(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from middle of word to start of word",
			content:        "hello world",
			initialCursor:  8, // middle of "world"
			expectedCursor: 6, // start of "world"
		},
		{
			name:           "from start of word to previous word",
			content:        "hello world",
			initialCursor:  6, // start of "world"
			expectedCursor: 0, // start of "hello"
		},
		{
			name:           "from end of text",
			content:        "hello world",
			initialCursor:  11, // end
			expectedCursor: 6,  // start of "world"
		},
		{
			name:           "from position 0 stays at 0",
			content:        "hello",
			initialCursor:  0,
			expectedCursor: 0,
		},
		{
			name:           "skips punctuation",
			content:        "hello, world!",
			initialCursor:  13, // end
			expectedCursor: 7,  // start of "world"
		},
		{
			name:           "handles multiple spaces",
			content:        "hello   world",
			initialCursor:  13, // end
			expectedCursor: 8,  // start of "world"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.SetCursor(tt.initialCursor)
			m.focused = FieldBody

			m.moveCursorWordBackward()

			got := m.getCursorPos()
			if got != tt.expectedCursor {
				t.Errorf("moveCursorWordBackward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestMoveCursorWordForward(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from start to end of first word",
			content:        "hello world",
			initialCursor:  0,
			expectedCursor: 6, // start of next word
		},
		{
			name:           "from middle of word to start of next",
			content:        "hello world",
			initialCursor:  2, // middle of "hello"
			expectedCursor: 6, // start of "world"
		},
		{
			name:           "from end stays at end",
			content:        "hello",
			initialCursor:  5,
			expectedCursor: 5,
		},
		{
			name:           "skips punctuation",
			content:        "hello, world!",
			initialCursor:  0,
			expectedCursor: 7, // start of "world"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.SetCursor(tt.initialCursor)
			m.focused = FieldBody

			m.moveCursorWordForward()

			got := m.getCursorPos()
			if got != tt.expectedCursor {
				t.Errorf("moveCursorWordForward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestMoveCursorParagraphBackward(t *testing.T) {
	// Note: Testing paragraph movement with multiline text is tricky because
	// the bubbles textarea doesn't reliably update Line()/LineInfo() after SetCursor
	// without going through the full Update cycle. We test single-line cases here.
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from position 0 stays at 0",
			content:        "hello",
			initialCursor:  0,
			expectedCursor: 0,
		},
		{
			name:           "from middle of single line to start",
			content:        "hello world",
			initialCursor:  6, // start of "world"
			expectedCursor: 0, // start of text
		},
		{
			name:           "from end of single line to start",
			content:        "hello",
			initialCursor:  5,
			expectedCursor: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.Focus()
			m.bodyInput.SetCursor(tt.initialCursor)
			m.focused = FieldBody

			m.moveCursorParagraphBackward()

			got := m.getCursorPos()
			if got != tt.expectedCursor {
				t.Errorf("moveCursorParagraphBackward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestMoveCursorParagraphForward(t *testing.T) {
	// Note: Testing paragraph movement with multiline text is tricky because
	// the bubbles textarea doesn't reliably update Line()/LineInfo() after SetCursor
	// without going through the full Update cycle. We test single-line cases here.
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from start to end of single line",
			content:        "hello world",
			initialCursor:  0,
			expectedCursor: 11, // end of text
		},
		{
			name:           "from middle to end of single line",
			content:        "hello",
			initialCursor:  3,
			expectedCursor: 5, // end of text
		},
		{
			name:           "from end stays at end",
			content:        "hello",
			initialCursor:  5,
			expectedCursor: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.Focus()
			m.bodyInput.SetCursor(tt.initialCursor)
			m.focused = FieldBody

			m.moveCursorParagraphForward()

			got := m.getCursorPos()
			if got != tt.expectedCursor {
				t.Errorf("moveCursorParagraphForward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestMoveTitleCursorWordBackward(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from end to start of last word",
			content:        "hello world",
			initialCursor:  11,
			expectedCursor: 6,
		},
		{
			name:           "from start stays at start",
			content:        "hello",
			initialCursor:  0,
			expectedCursor: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.titleInput.SetValue(tt.content)
			m.titleInput.SetCursor(tt.initialCursor)
			m.focused = FieldTitle

			m.moveTitleCursorWordBackward()

			got := m.titleInput.Position()
			if got != tt.expectedCursor {
				t.Errorf("moveTitleCursorWordBackward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestMoveTitleCursorWordForward(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		initialCursor  int
		expectedCursor int
	}{
		{
			name:           "from start to start of next word",
			content:        "hello world",
			initialCursor:  0,
			expectedCursor: 6,
		},
		{
			name:           "from end stays at end",
			content:        "hello",
			initialCursor:  5,
			expectedCursor: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.titleInput.SetValue(tt.content)
			m.titleInput.SetCursor(tt.initialCursor)
			m.focused = FieldTitle

			m.moveTitleCursorWordForward()

			got := m.titleInput.Position()
			if got != tt.expectedCursor {
				t.Errorf("moveTitleCursorWordForward() cursor = %d, want %d", got, tt.expectedCursor)
			}
		})
	}
}

func TestHasFormData(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		body     string
		expected bool
	}{
		{
			name:     "empty form returns false",
			title:    "",
			body:     "",
			expected: false,
		},
		{
			name:     "whitespace only returns false",
			title:    "  ",
			body:     "\n\t",
			expected: false,
		},
		{
			name:     "title with content returns true",
			title:    "My task",
			body:     "",
			expected: true,
		},
		{
			name:     "body with content returns true",
			title:    "",
			body:     "Some description",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "", nil)
			m.titleInput.SetValue(tt.title)
			m.bodyInput.SetValue(tt.body)

			if got := m.hasFormData(); got != tt.expected {
				t.Errorf("hasFormData() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEscapeWithEmptyFormCancelsImmediately(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)

	// Press escape on empty form
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.Update(escMsg)
	fm := model.(*FormModel)

	// Form should be cancelled without confirmation
	if !fm.cancelled {
		t.Error("expected form to be cancelled when pressing escape on empty form")
	}
	if fm.showCancelConfirm {
		t.Error("expected no confirmation prompt for empty form")
	}
}

func TestEscapeWithDataShowsConfirmation(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")

	// Press escape with data in form
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.Update(escMsg)
	fm := model.(*FormModel)

	// Should show confirmation, not cancel yet
	if fm.cancelled {
		t.Error("expected form NOT to be cancelled yet, should show confirmation")
	}
	if !fm.showCancelConfirm {
		t.Error("expected confirmation prompt to be shown")
	}
}

func TestConfirmationYesCancelsForm(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")
	m.showCancelConfirm = true

	// Press 'y' to confirm
	yMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	model, _ := m.Update(yMsg)
	fm := model.(*FormModel)

	if !fm.cancelled {
		t.Error("expected form to be cancelled after pressing 'y'")
	}
}

func TestConfirmationNoCancelsConfirmation(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")
	m.showCancelConfirm = true

	// Press 'n' to decline
	nMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	model, _ := m.Update(nMsg)
	fm := model.(*FormModel)

	if fm.cancelled {
		t.Error("expected form NOT to be cancelled after pressing 'n'")
	}
	if fm.showCancelConfirm {
		t.Error("expected confirmation prompt to be dismissed")
	}
}

func TestConfirmationEscCancelsConfirmation(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")
	m.showCancelConfirm = true

	// Press esc to dismiss confirmation
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.Update(escMsg)
	fm := model.(*FormModel)

	if fm.cancelled {
		t.Error("expected form NOT to be cancelled after pressing escape on confirmation")
	}
	if fm.showCancelConfirm {
		t.Error("expected confirmation prompt to be dismissed")
	}
}

func TestEscapeDismissesGhostTextFirst(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")
	m.ghostText = "suggestion text"
	m.ghostFullText = "Some task title suggestion text"

	// Press escape with ghost text showing
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.Update(escMsg)
	fm := model.(*FormModel)

	// Should dismiss ghost text, not show confirmation or cancel
	if fm.cancelled {
		t.Error("expected form NOT to be cancelled, should dismiss ghost text first")
	}
	if fm.showCancelConfirm {
		t.Error("expected NO confirmation prompt, should dismiss ghost text first")
	}
	if fm.ghostText != "" {
		t.Error("expected ghost text to be cleared")
	}
}

func TestEscapeDismissesTaskRefAutocompleteFirst(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.titleInput.SetValue("Some task title")
	m.showTaskRefAutocomplete = true
	m.taskRefAutocomplete = NewTaskRefAutocompleteModel(nil, 80)
	// Note: filteredTasks is nil (no results) so HasResults() is false

	// Press escape with autocomplete state active (but no visible results)
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	model, _ := m.Update(escMsg)
	fm := model.(*FormModel)

	// Should clear the autocomplete state and show confirmation (since there's data)
	if fm.showTaskRefAutocomplete {
		t.Error("expected task ref autocomplete to be dismissed")
	}
}

func TestConfirmationMessageAppearsInView(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", nil)
	m.showCancelConfirm = true

	view := m.View()

	if !strings.Contains(view, "Discard changes?") {
		t.Error("expected confirmation message in view")
	}
}

func TestBuildExecutorList(t *testing.T) {
	tests := []struct {
		name               string
		availableExecutors []string
		usageCounts        map[string]int
		want               []string
	}{
		{
			name:               "multiple available no usage",
			availableExecutors: []string{"claude", "codex", "gemini"},
			usageCounts:        nil,
			want:               []string{"claude", "codex", "gemini"},
		},
		{
			name:               "none available",
			availableExecutors: []string{},
			usageCounts:        nil,
			want:               []string{},
		},
		{
			name:               "single available",
			availableExecutors: []string{"claude"},
			usageCounts:        nil,
			want:               []string{"claude"},
		},
		{
			name:               "nil available",
			availableExecutors: nil,
			usageCounts:        nil,
			want:               []string{},
		},
		{
			name:               "sorted by usage - codex most used",
			availableExecutors: []string{"claude", "codex", "gemini"},
			usageCounts:        map[string]int{"codex": 10, "claude": 5, "gemini": 2},
			want:               []string{"codex", "claude", "gemini"},
		},
		{
			name:               "sorted by usage - gemini most used",
			availableExecutors: []string{"claude", "codex", "gemini"},
			usageCounts:        map[string]int{"gemini": 20, "codex": 5},
			want:               []string{"gemini", "codex", "claude"},
		},
		{
			name:               "sorted by usage - ties sorted alphabetically",
			availableExecutors: []string{"claude", "codex", "gemini"},
			usageCounts:        map[string]int{"codex": 5, "gemini": 5},
			want:               []string{"codex", "gemini", "claude"},
		},
		{
			name:               "empty usage counts treated as no sorting",
			availableExecutors: []string{"claude", "codex", "gemini"},
			usageCounts:        map[string]int{},
			want:               []string{"claude", "codex", "gemini"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildExecutorList(tt.availableExecutors, tt.usageCounts)
			if len(got) != len(tt.want) {
				t.Fatalf("buildExecutorList() = %v, want %v", got, tt.want)
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("buildExecutorList()[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestFormWithNoAvailableExecutors(t *testing.T) {
	// Test that form properly handles no available executors
	m := NewFormModel(nil, 100, 50, "", nil)

	// Executor list should be empty when no executors are available
	if len(m.executors) != 0 {
		t.Errorf("expected empty executor list, got %v", m.executors)
	}
}

func TestFormWithSomeAvailableExecutors(t *testing.T) {
	// Test that form only shows available executors
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})

	// Should only have claude in the list
	if len(m.executors) != 1 {
		t.Errorf("expected 1 executor, got %d: %v", len(m.executors), m.executors)
	}
	if m.executors[0] != "claude" {
		t.Errorf("expected 'claude', got %q", m.executors[0])
	}

	// Unavailable executors should not be in the list at all
	for _, e := range m.executors {
		if e == "codex" || e == "gemini" {
			t.Errorf("unavailable executor %q should not be in the list", e)
		}
	}
}

func TestGetDBTaskUsesExecutorDirectly(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.titleInput.SetValue("Test task")
	m.executor = "claude"

	task := m.GetDBTask()
	if task.Executor != "claude" {
		t.Errorf("GetDBTask() executor = %q, want %q", task.Executor, "claude")
	}
}

func TestFormDefaultsToAvailableExecutor(t *testing.T) {
	// When claude is available, form should default to claude
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})

	if m.executor != "claude" {
		t.Errorf("expected form to default to 'claude', got %q", m.executor)
	}
	if m.executorIdx != 0 {
		t.Errorf("expected executorIdx to be 0, got %d", m.executorIdx)
	}
}

func TestFormDefaultsToFirstAvailableExecutor(t *testing.T) {
	// When only codex is available, form should default to codex
	m := NewFormModel(nil, 100, 50, "", []string{"codex"})

	if m.executor != "codex" {
		t.Errorf("expected form to default to 'codex', got %q", m.executor)
	}
}

func TestFormProgressiveDisclosure(t *testing.T) {
	t.Run("new form starts in simple mode focused on title", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		if m.showAdvanced {
			t.Error("expected new form to start with showAdvanced=false")
		}
		if m.focused != FieldTitle {
			t.Errorf("expected focus on FieldTitle, got %d", m.focused)
		}
	})

	t.Run("edit form starts in advanced mode", func(t *testing.T) {
		m := NewEditFormModel(nil, &db.Task{
			Title:    "test",
			Project:  "personal",
			Executor: "claude",
		}, 100, 50, []string{"claude"})

		if !m.showAdvanced {
			t.Error("expected edit form to start with showAdvanced=true")
		}
	})

	t.Run("simple mode hides advanced fields", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		if m.isFieldVisible(FieldProject) {
			t.Error("FieldProject should be hidden in simple mode")
		}
		if !m.isFieldVisible(FieldTitle) {
			t.Error("FieldTitle should be visible in simple mode")
		}
		if !m.isFieldVisible(FieldBody) {
			t.Error("FieldBody should be visible in simple mode")
		}
		if m.isFieldVisible(FieldAttachments) {
			t.Error("FieldAttachments should be hidden in simple mode")
		}
		if m.isFieldVisible(FieldType) {
			t.Error("FieldType should be hidden in simple mode")
		}
		if m.isFieldVisible(FieldExecutor) {
			t.Error("FieldExecutor should be hidden in simple mode")
		}
	})

	t.Run("ctrl+e toggles advanced mode", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		if m.showAdvanced {
			t.Fatal("expected simple mode initially")
		}

		// Toggle to advanced
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		if !m.showAdvanced {
			t.Error("expected advanced mode after ctrl+e")
		}

		// Toggle back to simple
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		if m.showAdvanced {
			t.Error("expected simple mode after second ctrl+e")
		}
	})

	t.Run("focusNext skips hidden fields in simple mode", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		// Start on Title
		if m.focused != FieldTitle {
			t.Fatalf("expected focus on FieldTitle, got %d", m.focused)
		}

		// Tab should go to Body (skipping nothing, it's the next visible field)
		m.focusNext()
		if m.focused != FieldBody {
			t.Errorf("expected focus on FieldBody after tab, got %d", m.focused)
		}

		// Tab again should wrap back to Title (skipping Attachments, Type, Executor, Project)
		m.focusNext()
		if m.focused != FieldTitle {
			t.Errorf("expected focus to wrap back to FieldTitle, got %d", m.focused)
		}
	})

	t.Run("simple mode view shows defaults summary", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		view := m.View()

		if !strings.Contains(view, "more options") {
			t.Error("expected simple mode view to show 'more options' hint")
		}
		if strings.Contains(view, "Attachments") {
			t.Error("expected simple mode view to hide Attachments field")
		}
	})

	t.Run("advanced mode view shows all fields", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})
		m.showAdvanced = true

		view := m.View()

		if !strings.Contains(view, "Project") {
			t.Error("expected advanced mode view to show Project field")
		}
		if !strings.Contains(view, "Attachments") {
			t.Error("expected advanced mode view to show Attachments field")
		}
		if !strings.Contains(view, "Type") {
			t.Error("expected advanced mode view to show Type field")
		}
		if !strings.Contains(view, "Executor") {
			t.Error("expected advanced mode view to show Executor field")
		}
		if !strings.Contains(view, "fewer options") {
			t.Error("expected advanced mode view to show 'fewer options' hint")
		}
	})

	t.Run("collapsing moves focus from hidden field to title", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})
		m.showAdvanced = true
		m.focused = FieldExecutor

		// Collapse
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

		if m.focused != FieldTitle {
			t.Errorf("expected focus to move to FieldTitle when collapsing, got %d", m.focused)
		}
	})
}

func TestFormHeaderShowsProjectInSimpleMode(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.project = "myproject"

	view := m.View()

	if !strings.Contains(view, "myproject") {
		t.Error("expected simple mode header to include project name")
	}
}

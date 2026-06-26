package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
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
			name:         "empty content fills available space",
			content:      "",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      20, // body fills available space (50 - overhead)
			wantMax:      50,
		},
		{
			name:         "single line fills available space",
			content:      "hello world",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      20,
			wantMax:      50,
		},
		{
			name:         "small screen uses minimum height",
			content:      "",
			screenHeight: 20,
			screenWidth:  100,
			wantMin:      8, // minimum height enforced
			wantMax:      20,
		},
		{
			name:         "large screen fills more space",
			content:      "",
			screenHeight: 100,
			screenWidth:  100,
			wantMin:      50, // more screen = more body space
			wantMax:      100,
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

	// Should fill available space regardless of content
	m.updateBodyHeight()
	// The textarea height is internal, so we just verify no panic

	// Add content and update - height stays the same (fills available space)
	m.bodyInput.SetValue("line1\nline2\nline3\nline4\nline5")
	m.updateBodyHeight()

	// Large content - height is the same (fills available space)
	m.bodyInput.SetValue(strings.Repeat("line\n", 100))
	m.updateBodyHeight()
}

func TestBodyHeightFillsAvailableSpace(t *testing.T) {
	screenHeights := []int{40, 60, 80, 100}

	for _, screenHeight := range screenHeights {
		t.Run("screen_height_"+string(rune('0'+screenHeight/10)), func(t *testing.T) {
			m := NewFormModel(nil, 100, screenHeight, "", nil)

			height := m.calculateBodyHeight()

			// Body should fill available space (screen minus overhead)
			// In advanced mode: boxChrome(6) + common(6) + advanced(10) = 22
			expectedHeight := screenHeight - 22
			if expectedHeight < 8 {
				expectedHeight = 8
			}

			if height != expectedHeight {
				t.Errorf("height %d != expected %d for screen height %d", height, expectedHeight, screenHeight)
			}
		})
	}
}

func TestEditFormIsModal(t *testing.T) {
	task := &db.Task{Title: "Test task", Body: "Some body", Project: "proj"}
	m := NewEditFormModel(nil, task, 120, 40, []string{"claude"})

	if !m.modal {
		t.Fatal("expected edit form to be rendered as a modal")
	}

	view := m.View()

	// A centered modal floats within the screen, so the first line should be
	// blank padding rather than the top border of a full-screen box.
	firstLine := strings.SplitN(view, "\n", 2)[0]
	if strings.Contains(firstLine, "╭") {
		t.Errorf("expected modal to be centered with top margin, but first line is the border: %q", firstLine)
	}

	// The modal box should be narrower than the full screen width.
	if !strings.Contains(view, "         ╭") {
		t.Error("expected modal box to be indented (narrower than full width)")
	}
}

func TestEditFormDiscardWarningAtTop(t *testing.T) {
	task := &db.Task{Title: "Test task", Body: "Some body", Project: "proj"}
	m := NewEditFormModel(nil, task, 120, 40, []string{"claude"})
	m.showCancelConfirm = true

	view := m.View()

	discardIdx := strings.Index(view, "Discard changes?")
	if discardIdx == -1 {
		t.Fatal("expected discard warning to be rendered")
	}

	// In modal mode the warning must appear above the editable fields
	// (Title/Details), i.e. at the top of the modal.
	titleIdx := strings.Index(view, "Title")
	if titleIdx == -1 {
		t.Fatal("expected Title field to be rendered")
	}
	if discardIdx > titleIdx {
		t.Errorf("expected discard warning (pos %d) to appear before Title field (pos %d)", discardIdx, titleIdx)
	}
}

func TestEditFormModalBodyHeightBounded(t *testing.T) {
	task := &db.Task{Title: "Test", Body: "one line"}

	// Short body on a tall screen should stay compact (not fill the screen).
	m := NewEditFormModel(nil, task, 120, 80, []string{"claude"})
	if h := m.calculateBodyHeight(); h > 14 {
		t.Errorf("modal body height %d should be bounded to <= 14 for a short body", h)
	}

	// A long body grows but is still capped so every field stays visible.
	task.Body = strings.Repeat("line\n", 100)
	big := NewEditFormModel(nil, task, 120, 80, []string{"claude"})
	if h := big.calculateBodyHeight(); h > 14 {
		t.Errorf("modal body height %d should be capped at 14 even for long bodies", h)
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

// TestApplyToOnlyTouchesEditableFields verifies that overlaying the edit form
// onto an existing task changes only the fields the form exposes and leaves
// every other persisted column untouched (regression guard for #560).
func TestApplyToOnlyTouchesEditableFields(t *testing.T) {
	original := &db.Task{
		Title:           "Old title",
		Body:            "Old body",
		Type:            "code",
		Project:         "proj",
		Executor:        "claude",
		ClaudeSessionID: "sess-abc",
		DaemonSession:   "ty-1",
		Port:            4242,
		PRInfoJSON:      `{"state":"open"}`,
		DangerousMode:   false,
		PermissionMode:  "auto",
		RemoteControl:   true,
		Pinned:          true,
		Tags:            "urgent,backend",
		SourceBranch:    "main",
		Summary:         "a summary",
	}

	m := NewEditFormModel(nil, original, 120, 40, []string{"claude"})
	m.titleInput.SetValue("New title")
	m.bodyInput.SetValue("New body")

	updated := *original
	m.ApplyTo(&updated)

	// Edited fields are applied.
	if updated.Title != "New title" {
		t.Errorf("Title = %q, want %q", updated.Title, "New title")
	}
	if updated.Body != "New body" {
		t.Errorf("Body = %q, want %q", updated.Body, "New body")
	}

	// Persisted fields the edit form doesn't change are preserved. Permission
	// mode IS an editable form field now, so it round-trips through the form
	// (seeded from the task), and DangerousMode stays consistent with it.
	preserved := []struct {
		name string
		got  any
		want any
	}{
		{"ClaudeSessionID", updated.ClaudeSessionID, "sess-abc"},
		{"DaemonSession", updated.DaemonSession, "ty-1"},
		{"Port", updated.Port, 4242},
		{"PRInfoJSON", updated.PRInfoJSON, `{"state":"open"}`},
		{"DangerousMode", updated.DangerousMode, false},
		{"PermissionMode", updated.PermissionMode, "auto"},
		{"RemoteControl", updated.RemoteControl, true},
		{"Pinned", updated.Pinned, true},
		{"Tags", updated.Tags, "urgent,backend"},
		{"SourceBranch", updated.SourceBranch, "main"},
		{"Summary", updated.Summary, "a summary"},
	}
	for _, c := range preserved {
		if c.got != c.want {
			t.Errorf("%s was modified by ApplyTo: got %v, want %v", c.name, c.got, c.want)
		}
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
	t.Run("new form starts in advanced mode focused on project", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})

		if !m.showAdvanced {
			t.Error("expected new form to start with showAdvanced=true")
		}
		if m.focused != FieldProject {
			t.Errorf("expected focus on FieldProject, got %d", m.focused)
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
		m.showAdvanced = false // Switch to simple mode for this test

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

		if !m.showAdvanced {
			t.Fatal("expected advanced mode initially")
		}

		// Toggle to simple
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		if m.showAdvanced {
			t.Error("expected simple mode after ctrl+e")
		}

		// Toggle back to advanced
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		if !m.showAdvanced {
			t.Error("expected advanced mode after second ctrl+e")
		}
	})

	t.Run("focusNext skips hidden fields in simple mode", func(t *testing.T) {
		m := NewFormModel(nil, 100, 50, "", []string{"claude"})
		m.showAdvanced = false // Switch to simple mode for this test
		m.focused = FieldTitle // Manually set focus to first visible field for this test

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
		m.showAdvanced = false // Switch to simple mode for this test

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
	m.showAdvanced = false // Switch to simple mode for this test
	m.project = "myproject"

	view := m.View()

	if !strings.Contains(view, "myproject") {
		t.Error("expected simple mode header to include project name")
	}
}

func TestProjectSearchMode(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.showAdvanced = true
	m.projects = []string{"personal", "workflow", "webapp", "marketing", "data-pipeline"}
	m.project = "personal"
	m.projectIdx = 0
	m.focused = FieldProject

	// Typing a letter should enter search mode
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if !m.projectSearchMode {
		t.Fatal("expected search mode to be active after typing a letter")
	}
	if m.projectSearchQuery != "w" {
		t.Fatalf("expected search query 'w', got %q", m.projectSearchQuery)
	}
	if len(m.projectFiltered) < 1 {
		t.Fatal("expected at least one filtered result for 'w'")
	}
	// "workflow" and "webapp" should match
	found := false
	for _, p := range m.projectFiltered {
		if p == "workflow" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'workflow' to be in filtered results for query 'w'")
	}

	// Continue typing to narrow results
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if m.projectSearchQuery != "wo" {
		t.Fatalf("expected search query 'wo', got %q", m.projectSearchQuery)
	}

	// Remember the top result before selecting
	topResult := m.projectFiltered[0]

	// Select with enter
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.projectSearchMode {
		t.Fatal("expected search mode to be deactivated after enter")
	}
	if m.project != topResult {
		t.Errorf("expected project to be %q, got %q", topResult, m.project)
	}
}

func TestProjectSearchEscape(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.showAdvanced = true
	m.projects = []string{"personal", "workflow"}
	m.project = "personal"
	m.projectIdx = 0
	m.focused = FieldProject

	// Enter search mode
	m.enterProjectSearch()
	if !m.projectSearchMode {
		t.Fatal("expected search mode active")
	}

	// Escape should exit search mode without changing project
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.projectSearchMode {
		t.Fatal("expected search mode to be deactivated after escape")
	}
	if m.project != "personal" {
		t.Errorf("expected project unchanged after escape, got %q", m.project)
	}
}

func TestProjectSearchBackspaceExitsWhenEmpty(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.showAdvanced = true
	m.projects = []string{"personal", "workflow"}
	m.project = "personal"
	m.focused = FieldProject

	// Enter search mode
	m.enterProjectSearch()
	if m.projectSearchQuery != "" {
		t.Fatal("expected empty query on search start")
	}

	// Backspace on empty query should exit search mode
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.projectSearchMode {
		t.Fatal("expected backspace on empty query to exit search mode")
	}
}

func TestProjectSearchViewShowsDropdown(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.showAdvanced = true
	m.projects = []string{"personal", "workflow", "webapp"}
	m.project = "personal"
	m.projectIdx = 0
	m.focused = FieldProject

	// Enter search mode with query
	m.enterProjectSearch()
	m.projectSearchQuery = "w"
	m.filterProjects()

	view := m.View()

	// Should show search results
	if !strings.Contains(view, "workflow") {
		t.Error("expected dropdown to show 'workflow' for query 'w'")
	}
}

func TestProjectSearchFuzzyMatch(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.projects = []string{"personal", "data-pipeline", "design-portal", "deploy-prod"}

	// Test fuzzy matching: "dp" should match "data-pipeline" and "deploy-prod" and "design-portal"
	m.projectSearchQuery = "dp"
	m.filterProjects()

	if len(m.projectFiltered) == 0 {
		t.Fatal("expected fuzzy match results for 'dp'")
	}
}

func TestFilterProjectsEmptyQueryShowsAll(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{"claude"})
	m.projects = []string{"personal", "workflow", "webapp"}
	m.project = "workflow"

	m.projectSearchQuery = ""
	m.filterProjects()

	if len(m.projectFiltered) != 3 {
		t.Errorf("expected all 3 projects, got %d", len(m.projectFiltered))
	}
	// Current project should be pre-selected
	if m.projectFiltered[m.projectFilteredIdx] != "workflow" {
		t.Errorf("expected current project 'workflow' to be pre-selected, got %q", m.projectFiltered[m.projectFilteredIdx])
	}
}

func TestEffortFieldDefaultsToGlobal(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})

	// Effort options start with "" (the global/Claude default).
	if len(m.effortLevels) == 0 || m.effortLevels[0] != "" {
		t.Fatalf("expected first effort option to be the empty default, got %v", m.effortLevels)
	}
	if m.effortLevel != "" {
		t.Errorf("expected default effort level to be empty, got %q", m.effortLevel)
	}

	// GetDBTask should not set an override when the user leaves the default.
	if got := m.GetDBTask().EffortLevel; got != "" {
		t.Errorf("expected GetDBTask to leave effort empty by default, got %q", got)
	}
}

func TestEffortFieldCycleAndPersist(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true
	m.focused = FieldEffort

	// Cycling right from the default lands on the first real effort level.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.effortLevel != db.EffortLow {
		t.Errorf("expected effort to be %q after one right press, got %q", db.EffortLow, m.effortLevel)
	}

	if got := m.GetDBTask().EffortLevel; got != db.EffortLow {
		t.Errorf("expected GetDBTask to carry effort %q, got %q", db.EffortLow, got)
	}

	// Cycling left returns to the default (empty) override.
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.effortLevel != "" {
		t.Errorf("expected effort to return to default, got %q", m.effortLevel)
	}
}

func TestEffortFieldHiddenForNonClaude(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true

	m.executor = db.ExecutorClaude
	if !m.isFieldVisible(FieldEffort) {
		t.Error("expected effort field to be visible for the Claude executor")
	}

	m.executor = db.ExecutorCodex
	if m.isFieldVisible(FieldEffort) {
		t.Error("expected effort field to be hidden for non-Claude executors")
	}
}

func TestModelFieldDefaultsToGlobal(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})

	// Model options start with "" (the global/Claude default).
	if len(m.models) == 0 || m.models[0] != "" {
		t.Fatalf("expected first model option to be the empty default, got %v", m.models)
	}
	if m.model != "" {
		t.Errorf("expected default model to be empty, got %q", m.model)
	}

	// GetDBTask should not set an override when the user leaves the default.
	if got := m.GetDBTask().Model; got != "" {
		t.Errorf("expected GetDBTask to leave model empty by default, got %q", got)
	}
}

func TestModelFieldCycleAndPersist(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true
	m.focused = FieldModel

	// Cycling right from the default lands on the first real model.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.model != db.ModelOpus {
		t.Errorf("expected model to be %q after one right press, got %q", db.ModelOpus, m.model)
	}

	if got := m.GetDBTask().Model; got != db.ModelOpus {
		t.Errorf("expected GetDBTask to carry model %q, got %q", db.ModelOpus, got)
	}

	// Cycling left returns to the default (empty) override.
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.model != "" {
		t.Errorf("expected model to return to default, got %q", m.model)
	}
}

func TestModelFieldHiddenForNonClaude(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true

	m.executor = db.ExecutorClaude
	if !m.isFieldVisible(FieldModel) {
		t.Error("expected model field to be visible for the Claude executor")
	}

	m.executor = db.ExecutorCodex
	if m.isFieldVisible(FieldModel) {
		t.Error("expected model field to be hidden for non-Claude executors")
	}
}

func TestPermissionFieldDefaultsToProjectDefault(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})

	// With no project record, the form falls back to the global default ("auto").
	want := db.GlobalDefaultPermissionMode()
	if m.permissionMode != want {
		t.Errorf("permissionMode = %q, want default %q", m.permissionMode, want)
	}
	// "default" (prompt) is first so the less-restrictive modes follow it.
	if len(m.permissionModes) == 0 || m.permissionModes[0] != db.PermissionModeDefault {
		t.Fatalf("expected first permission option %q, got %v", db.PermissionModeDefault, m.permissionModes)
	}
	// The seeded default is carried onto the task so simple-mode tasks run in it.
	if got := m.GetDBTask().PermissionMode; got != want {
		t.Errorf("GetDBTask permission = %q, want %q", got, want)
	}
}

func TestPermissionFieldCycleAndPersist(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true
	m.focused = FieldPermission

	// Start from "default" for a deterministic sequence regardless of the
	// environment's configured default. The selector cycles in the canonical
	// order: default -> accept-edits -> auto -> dangerous.
	m.permissionIdx = permissionIndexFor(m.permissionModes, db.PermissionModeDefault)
	m.permissionMode = db.PermissionModeDefault

	// Right cycles default -> accept-edits and marks the field as user-chosen.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.permissionMode != db.PermissionModeAcceptEdits {
		t.Errorf("after right, permission = %q, want %q", m.permissionMode, db.PermissionModeAcceptEdits)
	}
	if !m.permissionTouched {
		t.Error("expected permissionTouched after a manual change")
	}

	// Three more rights -> auto -> dangerous; GetDBTask carries it and keeps
	// DangerousMode in sync.
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // -> auto
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // -> dangerous
	task := m.GetDBTask()
	if task.PermissionMode != db.PermissionModeDangerous {
		t.Errorf("GetDBTask permission = %q, want %q", task.PermissionMode, db.PermissionModeDangerous)
	}
	if !task.DangerousMode {
		t.Error("expected DangerousMode true when permission is dangerous")
	}
}

func TestPermissionAndEffortStickyPerProject(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "sticky", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// A task created with a non-default effort + permission becomes the project's
	// last-used choices.
	first := &db.Task{
		Title:          "first",
		Status:         db.StatusQueued,
		Type:           db.TypeCode,
		Project:        "sticky",
		Executor:       db.ExecutorClaude,
		EffortLevel:    db.EffortHigh,
		PermissionMode: db.PermissionModeDangerous,
	}
	if err := database.CreateTask(first); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The next new-task form for that project should default to the same choices.
	m := NewFormModel(database, 100, 50, "", []string{db.ExecutorClaude})
	if m.project != "sticky" {
		t.Fatalf("expected form to default to last-used project 'sticky', got %q", m.project)
	}
	if m.effortLevel != db.EffortHigh {
		t.Errorf("effort = %q, want sticky %q", m.effortLevel, db.EffortHigh)
	}
	if m.permissionMode != db.PermissionModeDangerous {
		t.Errorf("permission = %q, want sticky %q", m.permissionMode, db.PermissionModeDangerous)
	}
}

func TestPermissionFieldVisibility(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "", []string{db.ExecutorClaude})
	m.showAdvanced = true

	// Permission applies to every executor, so it shows for non-Claude too
	// (unlike effort).
	m.executor = db.ExecutorClaude
	if !m.isFieldVisible(FieldPermission) {
		t.Error("expected permission field visible in advanced mode for claude")
	}
	m.executor = db.ExecutorCodex
	if !m.isFieldVisible(FieldPermission) {
		t.Error("expected permission field visible in advanced mode for codex")
	}

	// Hidden in simple mode like the other advanced fields.
	m.showAdvanced = false
	if m.isFieldVisible(FieldPermission) {
		t.Error("expected permission field hidden in simple mode")
	}
}

func TestEditFormSeedsAndPreservesPermission(t *testing.T) {
	t.Run("auto mode preserved", func(t *testing.T) {
		m := NewEditFormModel(nil, &db.Task{
			Title:          "t",
			Project:        "personal",
			Executor:       db.ExecutorClaude,
			PermissionMode: db.PermissionModeAuto,
		}, 100, 50, []string{db.ExecutorClaude})

		if m.permissionMode != db.PermissionModeAuto {
			t.Errorf("permissionMode = %q, want %q", m.permissionMode, db.PermissionModeAuto)
		}
		task := m.GetDBTask()
		if task.PermissionMode != db.PermissionModeAuto {
			t.Errorf("GetDBTask permission = %q, want %q", task.PermissionMode, db.PermissionModeAuto)
		}
		if task.DangerousMode {
			t.Error("did not expect DangerousMode for auto")
		}
	})

	t.Run("legacy dangerous flag preserved", func(t *testing.T) {
		// A legacy task carrying only the boolean (no permission_mode) must not be
		// silently downgraded when edited.
		m := NewEditFormModel(nil, &db.Task{
			Title:         "t",
			Project:       "personal",
			Executor:      db.ExecutorClaude,
			DangerousMode: true,
		}, 100, 50, []string{db.ExecutorClaude})

		if m.permissionMode != db.PermissionModeDangerous {
			t.Errorf("permissionMode = %q, want %q", m.permissionMode, db.PermissionModeDangerous)
		}
		task := m.GetDBTask()
		if task.PermissionMode != db.PermissionModeDangerous || !task.DangerousMode {
			t.Errorf("expected dangerous preserved: mode=%q dangerous=%v", task.PermissionMode, task.DangerousMode)
		}
	})
}

// TestSelectorCollapsesWhenUnfocused verifies the form's single-choice fields
// render as a compact "‹ value ›" stepper when not focused, and expand into the
// full option list only when focused. This keeps the advanced section calm and
// scannable instead of showing every option for every field at once.
func TestSelectorCollapsesWhenUnfocused(t *testing.T) {
	m := NewFormModel(nil, 120, 40, "myproject", nil)
	selStyle := dummyStyle()
	optStyle := dummyStyle()
	dimStyle := dummyStyle()
	options := []string{"default", "accept-edits", "auto", "dangerous"}

	// Collapsed (unfocused): only the chosen value is shown, wrapped in chevrons.
	collapsed := m.renderSelector(options, 3, false, selStyle, optStyle, dimStyle)
	if !strings.Contains(collapsed, "dangerous") {
		t.Errorf("collapsed selector should show the chosen value, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "‹") || !strings.Contains(collapsed, "›") {
		t.Errorf("collapsed selector should include chevron affordance, got %q", collapsed)
	}
	for _, other := range []string{"default", "accept-edits", "auto"} {
		if strings.Contains(collapsed, other) {
			t.Errorf("collapsed selector should hide non-selected option %q, got %q", other, collapsed)
		}
	}

	// Expanded (focused): every option is visible so any choice is one keystroke away.
	expanded := m.renderSelector(options, 3, true, selStyle, optStyle, dimStyle)
	for _, opt := range options {
		if !strings.Contains(expanded, opt) {
			t.Errorf("expanded selector should show option %q, got %q", opt, expanded)
		}
	}
	if strings.Contains(expanded, "‹") {
		t.Errorf("expanded selector should not show the collapsed chevron, got %q", expanded)
	}
}

// TestUnfocusedSelectorEmptyOptionShowsNone verifies an empty-string option
// (e.g. "no type") collapses to the friendly "none" label.
func TestUnfocusedSelectorEmptyOptionShowsNone(t *testing.T) {
	m := NewFormModel(nil, 120, 40, "myproject", nil)
	collapsed := m.renderSelector([]string{"", "code", "writing"}, 0, false, dummyStyle(), dummyStyle(), dummyStyle())
	if !strings.Contains(collapsed, "none") {
		t.Errorf("empty option should render as 'none', got %q", collapsed)
	}
}

func dummyStyle() lipgloss.Style { return lipgloss.NewStyle() }

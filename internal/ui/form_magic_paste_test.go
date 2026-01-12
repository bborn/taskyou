package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFormModel_MagicPaste_GitHubPR(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Simulate pasting a GitHub PR URL into the title field
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("https://github.com/owner/repo/pull/123"),
		Paste: true,
	}

	// Process the paste
	_, _ = form.Update(pasteMsg)

	// Verify the title was set correctly
	expectedTitle := "repo #123"
	if form.titleInput.Value() != expectedTitle {
		t.Errorf("Expected title %q, got %q", expectedTitle, form.titleInput.Value())
	}

	// Verify PR URL and number were extracted
	expectedPRURL := "https://github.com/owner/repo/pull/123"
	if form.prURL != expectedPRURL {
		t.Errorf("Expected PRURL %q, got %q", expectedPRURL, form.prURL)
	}

	expectedPRNumber := 123
	if form.prNumber != expectedPRNumber {
		t.Errorf("Expected PRNumber %d, got %d", expectedPRNumber, form.prNumber)
	}
}

func TestFormModel_MagicPaste_GitHubIssue(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Simulate pasting a GitHub Issue URL into the title field
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("https://github.com/owner/repo/issues/42"),
		Paste: true,
	}

	// Process the paste
	_, _ = form.Update(pasteMsg)

	// Verify the title was set correctly
	expectedTitle := "repo #42"
	if form.titleInput.Value() != expectedTitle {
		t.Errorf("Expected title %q, got %q", expectedTitle, form.titleInput.Value())
	}

	// GitHub issues don't populate PR fields
	if form.prURL != "" {
		t.Errorf("Expected empty PRURL for issue, got %q", form.prURL)
	}

	if form.prNumber != 0 {
		t.Errorf("Expected PRNumber 0 for issue, got %d", form.prNumber)
	}
}

func TestFormModel_MagicPaste_Linear(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Simulate pasting a Linear URL into the title field
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("https://linear.app/myteam/issue/PROJ-456/fix-the-bug"),
		Paste: true,
	}

	// Process the paste
	_, _ = form.Update(pasteMsg)

	// Verify the title was set correctly
	expectedTitle := "PROJ-456"
	if form.titleInput.Value() != expectedTitle {
		t.Errorf("Expected title %q, got %q", expectedTitle, form.titleInput.Value())
	}

	// Linear issues don't populate PR fields
	if form.prURL != "" {
		t.Errorf("Expected empty PRURL for Linear issue, got %q", form.prURL)
	}

	if form.prNumber != 0 {
		t.Errorf("Expected PRNumber 0 for Linear issue, got %d", form.prNumber)
	}
}

func TestFormModel_MagicPaste_RegularText(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Set an initial value
	form.titleInput.SetValue("Initial ")

	// Simulate pasting regular text (not a URL)
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("some regular text"),
		Paste: true,
	}

	// Process the paste
	_, _ = form.Update(pasteMsg)

	// Verify the text was appended (not replaced)
	expectedTitle := "Initial some regular text"
	if form.titleInput.Value() != expectedTitle {
		t.Errorf("Expected title %q, got %q", expectedTitle, form.titleInput.Value())
	}

	// PR fields should remain empty
	if form.prURL != "" {
		t.Errorf("Expected empty PRURL, got %q", form.prURL)
	}

	if form.prNumber != 0 {
		t.Errorf("Expected PRNumber 0, got %d", form.prNumber)
	}
}

func TestFormModel_MagicPaste_OnlyWorksInTitleField(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Focus on body field
	form.focused = FieldBody

	// Simulate pasting a GitHub PR URL into the body field
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("https://github.com/owner/repo/pull/999"),
		Paste: true,
	}

	// Process the paste
	_, _ = form.Update(pasteMsg)

	// Verify magic paste did NOT happen - the URL should be pasted as-is
	expectedBody := "https://github.com/owner/repo/pull/999"
	if form.bodyInput.Value() != expectedBody {
		t.Errorf("Expected body %q, got %q", expectedBody, form.bodyInput.Value())
	}

	// PR fields should remain empty
	if form.prURL != "" {
		t.Errorf("Expected empty PRURL when pasting into body, got %q", form.prURL)
	}

	if form.prNumber != 0 {
		t.Errorf("Expected PRNumber 0 when pasting into body, got %d", form.prNumber)
	}

	// Title should remain empty
	if form.titleInput.Value() != "" {
		t.Errorf("Expected empty title, got %q", form.titleInput.Value())
	}
}

func TestFormModel_GetDBTask_IncludesPRInfo(t *testing.T) {
	// Create a form model
	form := NewFormModel(nil, 100, 30, "")

	// Set PR info via magic paste
	form.titleInput.SetValue("repo #123")
	form.prURL = "https://github.com/owner/repo/pull/123"
	form.prNumber = 123

	// Get the DB task
	task := form.GetDBTask()

	// Verify PR info is included
	if task.PRURL != "https://github.com/owner/repo/pull/123" {
		t.Errorf("Expected task.PRURL %q, got %q", "https://github.com/owner/repo/pull/123", task.PRURL)
	}

	if task.PRNumber != 123 {
		t.Errorf("Expected task.PRNumber %d, got %d", 123, task.PRNumber)
	}

	if task.Title != "repo #123" {
		t.Errorf("Expected task.Title %q, got %q", "repo #123", task.Title)
	}
}

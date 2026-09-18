package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/question"
)

func cacheChoice(kind string) *db.PendingQuestion {
	return &db.PendingQuestion{
		ID:       11,
		TaskID:   5513,
		Question: "Which cache backend should checkout use?",
		Kind:     kind,
		Options: []db.QuestionOption{
			{Label: "Redis", Description: "already running in prod"},
			{Label: "Memcached", Description: "simplest ops"},
			{Label: "In-process LRU", Description: "no network hop"},
			{Label: "No cache yet"},
		},
	}
}

func questionModel(q *db.PendingQuestion, height int) *DetailModel {
	m := &DetailModel{
		task: &db.Task{
			ID:      5513,
			Title:   "Add a cache to checkout",
			Status:  db.StatusBlocked,
			Project: "storefront",
			Body:    strings.Repeat("body line\n", 40),
		},
		focused: true,
		width:   120,
		height:  height,
	}
	m.setQuestion(q)
	m.initViewport()
	return m
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// The panel is sized to the pane, not the other way round: a short pane gets a
// compact panel and the view still fits, so the border and badge row stay on
// screen. Every option stays visible down to a single spare row.
func TestQuestionPanel_FitsThePane(t *testing.T) {
	for _, height := range []int{11, 12, 14, 20, 40} {
		m := questionModel(cacheChoice(db.QuestionChoice), height)
		view := m.View()
		if got := lipgloss.Height(view); got > height {
			t.Errorf("height %d: view is %d rows; it must fit the pane", height, got)
		}
		for _, label := range []string{"Redis", "Memcached", "In-process LRU", "No cache yet"} {
			if !strings.Contains(view, label) {
				t.Errorf("height %d: option %q not shown:\n%s", height, label, view)
			}
		}
	}

	full := questionModel(cacheChoice(db.QuestionChoice), 40).View()
	for _, want := range []string{"Which cache backend", "1. Redis", "already running in prod", "4. No cache yet", "r answer in your own words"} {
		if !strings.Contains(full, want) {
			t.Errorf("full panel lacks %q:\n%s", want, full)
		}
	}
}

// A plain question keeps the view it always had: answered with r, no panel.
func TestQuestionPanel_PlainQuestionHasNoPanel(t *testing.T) {
	m := questionModel(&db.PendingQuestion{ID: 3, Question: "What next?", Kind: db.QuestionText}, 30)
	if m.HasQuestion() {
		t.Fatal("a text question opened the options panel")
	}
	if handled, _ := m.HandleQuestionKey(runeKey('1'), DefaultKeyMap()); handled {
		t.Error("digits were taken with no options to pick")
	}
}

func TestQuestionKeys_ChoiceAnswersOnADigit(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionChoice), 30)
	keys := DefaultKeyMap()

	handled, cmd := m.HandleQuestionKey(runeKey('3'), keys)
	if !handled || cmd == nil {
		t.Fatalf("digit 3 = handled %v, cmd %v; want an answer sent", handled, cmd != nil)
	}
	if !m.answerInFlight || m.questionCursor != 2 {
		t.Errorf("after 3: inFlight %v cursor %d", m.answerInFlight, m.questionCursor)
	}
	// A second key while the first answer is in flight sends nothing more.
	if _, cmd := m.HandleQuestionKey(runeKey('1'), keys); cmd != nil {
		t.Error("a second answer was sent while the first was in flight")
	}
}

func TestQuestionKeys_JKAndEnter(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionChoice), 30)
	keys := DefaultKeyMap()

	m.HandleQuestionKey(runeKey('j'), keys)
	m.HandleQuestionKey(runeKey('j'), keys)
	m.HandleQuestionKey(runeKey('k'), keys)
	if m.questionCursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.questionCursor)
	}
	m.HandleQuestionKey(runeKey('k'), keys)
	m.HandleQuestionKey(runeKey('k'), keys)
	if m.questionCursor != 3 {
		t.Fatalf("k from the top should wrap to the last option, cursor = %d", m.questionCursor)
	}
	if handled, cmd := m.HandleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter}, keys); !handled || cmd == nil {
		t.Fatal("enter did not pick the highlighted option")
	}
}

func TestQuestionKeys_MultiChoiceTogglesThenSends(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionMultiChoice), 30)
	keys := DefaultKeyMap()

	// enter with nothing picked explains itself instead of sending.
	if _, cmd := m.HandleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter}, keys); cmd != nil {
		t.Fatal("sent an empty multi_choice answer")
	}
	if m.questionNotice == "" {
		t.Error("no hint after enter with nothing picked")
	}

	if _, cmd := m.HandleQuestionKey(runeKey('3'), keys); cmd != nil {
		t.Fatal("a digit sent a multi_choice answer instead of toggling")
	}
	m.HandleQuestionKey(runeKey('j'), keys) // cursor 2 → 3
	m.HandleQuestionKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, keys)
	if got := m.questionPicks(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("picks = %v, want [3 4]", got)
	}
	if view := m.View(); !strings.Contains(view, "[x]") {
		t.Errorf("toggled options not marked:\n%s", view)
	}
	if _, cmd := m.HandleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter}, keys); cmd == nil {
		t.Fatal("enter did not send the toggled options")
	}
}

func TestQuestionKeys_OutOfRangeDigitIsSwallowed(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionChoice), 30)
	handled, cmd := m.HandleQuestionKey(runeKey('9'), DefaultKeyMap())
	if !handled || cmd != nil {
		t.Fatalf("9 on a 4-option question = handled %v, cmd %v; want swallowed, nothing sent", handled, cmd != nil)
	}
	if !strings.Contains(m.questionNotice, "1 to 4") {
		t.Errorf("notice = %q", m.questionNotice)
	}
}

// Keys only answer while the TUI pane has the keyboard; otherwise they belong
// to whatever the person is typing into.
func TestQuestionKeys_IgnoredWhenUnfocused(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionChoice), 30)
	m.focused = false
	if handled, _ := m.HandleQuestionKey(runeKey('1'), DefaultKeyMap()); handled {
		t.Error("an unfocused panel took a key")
	}
}

func TestQuestionAnswered(t *testing.T) {
	t.Run("delivered clears the panel", func(t *testing.T) {
		m := questionModel(cacheChoice(db.QuestionChoice), 30)
		m.answerInFlight = true
		m.handleQuestionAnswered(questionAnsweredMsg{owner: m, taskID: 5513, questionID: 11, text: "Selected: Redis"})
		if m.HasQuestion() || m.answerInFlight {
			t.Errorf("after delivery: question %v, inFlight %v", m.HasQuestion(), m.answerInFlight)
		}
	})
	t.Run("answered elsewhere clears the panel", func(t *testing.T) {
		m := questionModel(cacheChoice(db.QuestionChoice), 30)
		m.handleQuestionAnswered(questionAnsweredMsg{owner: m, taskID: 5513, err: question.ErrStale})
		if m.HasQuestion() {
			t.Error("a question answered elsewhere is still up")
		}
	})
	t.Run("busy agent keeps the question and says why", func(t *testing.T) {
		m := questionModel(cacheChoice(db.QuestionChoice), 30)
		m.handleQuestionAnswered(questionAnsweredMsg{owner: m, taskID: 5513, err: &agentsend.BusyError{TaskID: 5513, Status: db.StatusProcessing}})
		if !m.HasQuestion() || !strings.Contains(m.questionNotice, "still working") {
			t.Errorf("question %v, notice %q", m.HasQuestion(), m.questionNotice)
		}
	})
	t.Run("no session", func(t *testing.T) {
		m := questionModel(cacheChoice(db.QuestionChoice), 30)
		m.handleQuestionAnswered(questionAnsweredMsg{owner: m, taskID: 5513, err: &agentsend.NoPaneError{TaskID: 5513}})
		if !m.HasQuestion() || !strings.Contains(m.questionNotice, "not running") {
			t.Errorf("question %v, notice %q", m.HasQuestion(), m.questionNotice)
		}
	})
	t.Run("other failures are shown", func(t *testing.T) {
		m := questionModel(cacheChoice(db.QuestionChoice), 30)
		m.handleQuestionAnswered(questionAnsweredMsg{owner: m, taskID: 5513, err: errors.New("disk full")})
		if !strings.Contains(m.questionNotice, "disk full") {
			t.Errorf("notice = %q", m.questionNotice)
		}
	})
}

// Leaving blocked settles the question, whatever answered it.
func TestQuestionPanel_ClearsWhenTheTaskLeavesBlocked(t *testing.T) {
	m := questionModel(cacheChoice(db.QuestionChoice), 30)
	moved := *m.task
	moved.Status = db.StatusProcessing
	m.UpdateTask(&moved)
	if m.HasQuestion() {
		t.Error("question still shown on a task that is processing")
	}
}

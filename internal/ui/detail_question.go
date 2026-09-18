package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bborn/workflow/internal/agentsend"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/question"
)

// The detail view's question panel: when a blocked task's agent asked a
// structured question (taskyou_needs_input with options), the options are
// listed in the header, numbered, and answered from the keyboard — a digit
// picks, j/k and enter pick the highlighted one, space toggles for
// multi_choice. `r` stays the way to answer in your own words.
//
// Nothing here decides what an answer means or how it travels: that is
// internal/question, the same core the GUI and `ty answer` call.

// questionAnsweredMsg reports an answer's delivery back to the detail view that
// sent it.
type questionAnsweredMsg struct {
	owner      *DetailModel
	taskID     int64
	questionID int64
	text       string
	err        error
}

// setQuestion replaces the pending question the panel shows. A different
// question starts the keyboard state over; the same one keeps it, so a refresh
// does not throw away a half-made multi_choice selection.
func (m *DetailModel) setQuestion(q *db.PendingQuestion) {
	if q != nil && !question.IsStructured(q) {
		q = nil // a plain question is answered with `r`, as it always was
	}
	prevID := int64(0)
	if m.question != nil {
		prevID = m.question.ID
	}
	nextID := int64(0)
	if q != nil {
		nextID = q.ID
	}
	m.question = q
	if prevID == nextID {
		return
	}
	m.questionCursor = 0
	m.questionPicked = nil
	m.questionNotice = ""
	m.reflowViewport() // the panel's height comes out of the viewport's
}

// HasQuestion reports whether the panel is showing a question to answer.
func (m *DetailModel) HasQuestion() bool {
	return m.question != nil
}

// HandleQuestionKey answers the pending question from the keyboard. It reports
// whether it consumed the key; keys it does not want fall through to the rest
// of the detail view (j/k then scroll, as they otherwise do).
func (m *DetailModel) HandleQuestionKey(msg tea.KeyMsg, keys KeyMap) (bool, tea.Cmd) {
	if m.question == nil || !m.focused {
		return false, nil
	}
	choices := question.Choices(m.question)
	if len(choices) == 0 {
		return false, nil
	}
	multi := m.question.Kind == db.QuestionMultiChoice

	switch {
	case key.Matches(msg, keys.AnswerOption):
		n := int(msg.String()[0] - '0')
		if n < 1 || n > len(choices) {
			m.questionNotice = fmt.Sprintf("pick 1 to %d", len(choices))
			return true, nil
		}
		m.questionCursor = n - 1
		if multi {
			m.toggleQuestionPick(n)
			return true, nil
		}
		return true, m.submitAnswer(question.Response{Choices: []int{n}})

	case key.Matches(msg, keys.NextOption):
		m.questionCursor = (m.questionCursor + 1) % len(choices)
		return true, nil

	case key.Matches(msg, keys.PrevOption):
		m.questionCursor = (m.questionCursor - 1 + len(choices)) % len(choices)
		return true, nil

	case key.Matches(msg, keys.ToggleOption):
		if !multi {
			return false, nil
		}
		m.toggleQuestionPick(m.questionCursor + 1)
		return true, nil

	case key.Matches(msg, keys.SubmitAnswer):
		// enter resumes a closed session; there is no agent to answer until then.
		if m.SessionClosed() {
			return false, nil
		}
		if !multi {
			return true, m.submitAnswer(question.Response{Choices: []int{m.questionCursor + 1}})
		}
		picks := m.questionPicks()
		if len(picks) == 0 {
			m.questionNotice = "space or 1-" + fmt.Sprint(len(choices)) + " to pick, then enter"
			return true, nil
		}
		return true, m.submitAnswer(question.Response{Choices: picks})
	}
	return false, nil
}

func (m *DetailModel) toggleQuestionPick(n int) {
	if m.questionPicked == nil {
		m.questionPicked = map[int]bool{}
	}
	m.questionPicked[n] = !m.questionPicked[n]
	m.questionNotice = ""
}

// questionPicks returns the toggled options, in option order.
func (m *DetailModel) questionPicks() []int {
	var picks []int
	for n, on := range m.questionPicked {
		if on {
			picks = append(picks, n)
		}
	}
	sort.Ints(picks)
	return picks
}

// submitAnswer delivers the answer off the UI thread, through the same sender
// every typed reply uses.
func (m *DetailModel) submitAnswer(resp question.Response) tea.Cmd {
	if m.answerInFlight || m.question == nil || m.task == nil {
		return nil
	}
	m.answerInFlight = true
	m.questionNotice = "sending…"
	database, owner := m.database, m
	taskID, questionID := m.task.ID, m.question.ID
	return func() tea.Msg {
		sender := agentSender(database)
		text, err := question.Answer(database, taskID, questionID, resp, sender.Send)
		return questionAnsweredMsg{owner: owner, taskID: taskID, questionID: questionID, text: text, err: err}
	}
}

// handleQuestionAnswered applies a delivery result to the panel.
func (m *DetailModel) handleQuestionAnswered(msg questionAnsweredMsg) {
	m.answerInFlight = false
	if m.task == nil || m.task.ID != msg.taskID {
		return
	}
	var invalid *question.InvalidError
	switch {
	case msg.err == nil, errors.Is(msg.err, question.ErrNoQuestion), errors.Is(msg.err, question.ErrStale):
		// Answered — here, or on another surface first. Either way the panel's
		// question is settled; the next refresh shows whatever the agent asks
		// next.
		m.setQuestion(nil)
	case errors.As(msg.err, &invalid):
		m.questionNotice = invalid.Error()
	case errors.Is(msg.err, agentsend.ErrBusy):
		m.questionNotice = "the agent is still working — answer again when it stops"
	case errors.Is(msg.err, agentsend.ErrNoPane):
		m.questionNotice = "the agent's session is not running — resume it, then answer"
	default:
		m.questionNotice = "could not send: " + msg.err.Error()
	}
}

// questionPanelBudget is how many rows the panel may take: what the pane has
// left after the rest of the header, the box chrome, the help row and one row
// of body. The view must fit the pane (see TestDetailModel_ViewFitsPaneHeight),
// so a short pane gets a shorter panel rather than a scrolled-away border.
func (m *DetailModel) questionPanelBudget(headerRows int) int {
	budget := m.height - headerRows - headerChromeHeight - m.footerHeight() - 1
	if IsGlobalDangerousMode() {
		budget--
	}
	return budget
}

// renderQuestionPanel lays the question out in at most budget rows: the full
// panel (question, one option per row, a hint) when it fits, otherwise the
// question over a single row of numbered options, otherwise that row alone.
func (m *DetailModel) renderQuestionPanel(width, budget int) []string {
	q := m.question
	if q == nil || budget < 1 {
		return nil
	}
	choices := question.Choices(q)
	multi := q.Kind == db.QuestionMultiChoice

	accent := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true)
	text := lipgloss.NewStyle()
	dim := lipgloss.NewStyle().Foreground(ColorMuted)
	num := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	if !m.focused {
		muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
		accent, text, dim, num = muted, muted, muted, muted
	}

	questionLine := accent.Render(truncateRunes("? "+q.Question, width))

	// Compact: every option on one row.
	inline := func(prefix string) string {
		parts := make([]string, len(choices))
		for i, c := range choices {
			mark := ""
			if multi && m.questionPicked[i+1] {
				mark = "✓"
			}
			parts[i] = num.Render(fmt.Sprintf("%d%s", i+1, mark)) + " " + text.Render(c.Label)
		}
		row := prefix + strings.Join(parts, dim.Render(" · "))
		return truncateANSI(row, width)
	}

	full := 1 + len(choices) + 1 // question, options, hint
	switch {
	case budget >= full:
		lines := []string{questionLine}
		for i, c := range choices {
			cursor := "  "
			if i == m.questionCursor {
				cursor = accent.Render("▸ ")
			}
			box := ""
			if multi {
				box = "[ ] "
				if m.questionPicked[i+1] {
					box = "[x] "
				}
			}
			row := cursor + box + num.Render(fmt.Sprintf("%d.", i+1)) + " " + text.Render(c.Label)
			if c.Description != "" {
				row += dim.Render(" — " + c.Description)
			}
			lines = append(lines, truncateANSI(row, width))
		}
		lines = append(lines, dim.Render(truncateRunes(m.questionHint(len(choices)), width)))
		return lines
	case budget >= 2:
		return []string{questionLine, inline("")}
	default:
		return []string{inline(accent.Render("? "))}
	}
}

// questionHint is the panel's last row: the last delivery problem if there is
// one, else how to answer.
func (m *DetailModel) questionHint(n int) string {
	if m.questionNotice != "" {
		return m.questionNotice
	}
	parts := []string{fmt.Sprintf("1-%d pick", n), "j/k move"}
	if m.question.Kind == db.QuestionMultiChoice {
		parts = []string{fmt.Sprintf("1-%d/space toggle", n), "j/k move", "enter send"}
	} else {
		parts = append(parts, "enter pick")
	}
	parts = append(parts, "r answer in your own words")
	return strings.Join(parts, " · ")
}

// truncateANSI cuts a styled row to width cells.
func truncateANSI(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

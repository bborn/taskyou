package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// A real board title. The "·" (U+00B7) is two bytes, so a byte-indexed cut can
// land inside it and emit a lone 0xC2. The terminal and lipgloss then disagree
// about how wide the line is, and the card's background and border stop short of
// the column edge.
const middleDotTitle = "@devdoc83 Claude Code npx allow-list bug · @paolino RubyLLM 2.0 ships +1 more"

func TestTaskCardTitleNeverSplitsARune(t *testing.T) {
	k := NewKanbanBoard(200, 50)
	card := k.renderTaskCard(&db.Task{ID: 5352, Title: middleDotTitle, Status: db.StatusBacklog, Project: "personal"}, 47, false)

	if !utf8.ValidString(card) {
		t.Fatal("rendered card holds invalid UTF-8: a multi-byte rune was cut in half")
	}
	if strings.ContainsRune(card, utf8.RuneError) {
		t.Fatal("rendered card holds U+FFFD from a half-written rune")
	}
}

// Every line of a card must occupy the same number of columns, or the column
// border drawn around it lands in the wrong place.
func TestTaskCardLinesShareOneWidth(t *testing.T) {
	k := NewKanbanBoard(200, 50)
	for _, title := range []string{
		middleDotTitle,
		"Plain ASCII title long enough that it certainly needs truncating",
		"Ünïcödé áccents throughout the title, long enough to be truncated here",
		"emoji 🚀 in a title that also runs long enough to require truncation now",
	} {
		card := k.renderTaskCard(&db.Task{ID: 1, Title: title, Status: db.StatusBacklog, Project: "personal"}, 47, false)
		widths := map[int]bool{}
		for _, line := range strings.Split(card, "\n") {
			widths[lipgloss.Width(line)] = true
		}
		if len(widths) > 1 {
			t.Fatalf("card lines have differing widths %v for title %q", widths, title)
		}
	}
}

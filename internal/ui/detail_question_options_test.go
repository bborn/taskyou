package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// A TUI user answers with `r`, so the execution log shows the answers the agent
// offered on the question's own line — never the raw JSON they are stored as.
func TestExecutionLogShowsOfferedOptionsOnTheQuestion(t *testing.T) {
	m := &DetailModel{
		task:    &db.Task{ID: 7, Title: "Add a cache", Status: db.StatusBlocked, Project: "storefront"},
		focused: true,
		width:   160,
		height:  40,
		logs: []*db.TaskLog{ // newest first, as the detail view loads them
			{ID: 3, LineType: "question", Content: "Which cache backend?"},
			{ID: 2, LineType: db.LogQuestionOptions, Content: `[{"label":"Redis"},{"label":"Memcached"}]`},
			{ID: 1, LineType: "tool", Content: "Bash: go test ./..."},
		},
	}
	m.initViewport()
	out := m.renderContent()
	if !strings.Contains(out, "Which cache backend?  (1. Redis · 2. Memcached)") {
		t.Errorf("question line lacks its options:\n%s", out)
	}
	if strings.Contains(out, `"label"`) {
		t.Errorf("options JSON leaked into the log:\n%s", out)
	}
}

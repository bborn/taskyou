package ui

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func TestSetUserVarSeqEncodesValue(t *testing.T) {
	value := "#1 a;b=c\x07"
	got := setUserVarSeq("taskyouTask", value)
	want := "\x1b]1337;SetUserVar=taskyouTask=" + base64.StdEncoding.EncodeToString([]byte(value)) + "\a"
	if got != want {
		t.Errorf("setUserVarSeq = %q, want %q", got, want)
	}
}

func TestTmuxPassthroughDoublesEscapes(t *testing.T) {
	got := tmuxPassthrough("\x1b]1337;x\a\x1b]1337;y\a")
	want := "\x1bPtmux;\x1b\x1b]1337;x\a\x1b\x1b]1337;y\a\x1b\\"
	if got != want {
		t.Errorf("tmuxPassthrough = %q, want %q", got, want)
	}
}

func TestTerminalTaskReporterWritesOnlyOnChange(t *testing.T) {
	var writes []string
	r := &terminalTaskReporter{write: func(s string) { writes = append(writes, s) }}
	task := &db.Task{ID: 5202, Title: "Fix\n  login"}

	r.report(nil) // first report always writes, clearing a previous run's values
	r.report(nil)
	r.report(task)
	r.report(task)
	r.report(&db.Task{ID: 5202, Title: "Fix login flow"})
	r.report(nil)

	if len(writes) != 4 {
		t.Fatalf("got %d writes, want 4: %q", len(writes), writes)
	}
	for i, want := range []string{
		setUserVarSeq(termVarTask, ""),
		setUserVarSeq(termVarTask, "#5202 Fix login") + setUserVarSeq(termVarTaskID, "5202") + setUserVarSeq(termVarTaskTitle, "Fix login"),
		setUserVarSeq(termVarTask, "#5202 Fix login flow"),
		setUserVarSeq(termVarTask, "") + setUserVarSeq(termVarTaskID, "") + setUserVarSeq(termVarTaskTitle, ""),
	} {
		if !strings.Contains(writes[i], want) {
			t.Errorf("write %d = %q, want it to contain %q", i, writes[i], want)
		}
	}
}

func TestTerminalTaskReporterWrapsForTmux(t *testing.T) {
	var got string
	r := &terminalTaskReporter{inTmux: true, write: func(s string) { got = s }}
	r.report(&db.Task{ID: 7, Title: "Seven"})
	if !strings.HasPrefix(got, "\x1bPtmux;") || !strings.HasSuffix(got, "\x1b\\") {
		t.Errorf("write = %q, want tmux passthrough", got)
	}
}

func TestReportTerminalTaskFollowsFocus(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "One", Status: db.StatusBacklog},
		{ID: 2, Title: "Two", Status: db.StatusBacklog},
	}
	var last string
	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		kanban:       NewKanbanBoard(100, 50),
		terminalTask: &terminalTaskReporter{write: func(s string) { last = s }},
	}
	m.kanban.SetTasks(tasks)
	before := m.kanban.SelectedTask()
	if before == nil {
		t.Fatal("expected a highlighted task")
	}

	// Moving the highlight through Update publishes the new card.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	after := m.kanban.SelectedTask()
	if after == nil || after.ID == before.ID {
		t.Fatalf("down did not move the highlight off task %d", before.ID)
	}
	if want := setUserVarSeq(termVarTask, fmt.Sprintf("#%d %s", after.ID, after.Title)); !strings.Contains(last, want) {
		t.Errorf("write = %q, want it to contain %q", last, want)
	}

	// The detail view reports the open task, whatever the board highlights.
	m.currentView = ViewDetail
	m.selectedTask = before
	m.reportTerminalTask()
	if want := setUserVarSeq(termVarTask, fmt.Sprintf("#%d %s", before.ID, before.Title)); !strings.Contains(last, want) {
		t.Errorf("in detail view, write = %q, want it to contain %q", last, want)
	}
}

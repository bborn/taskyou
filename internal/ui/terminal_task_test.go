package ui

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

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

// Inside tmux, a sequence written while no client is attached is thrown away.
// `ty open <task>` publishes its task in exactly that gap, and the task on
// screen never changes afterwards — so publishing on change alone left the tab
// title blank for the whole session.
func TestTerminalTaskReporterRepublishesWhenAClientAttaches(t *testing.T) {
	var writes []string
	attached := false
	clock := time.Now()
	r := &terminalTaskReporter{
		inTmux:   true,
		write:    func(s string) { writes = append(writes, s) },
		attached: func() bool { return attached },
		now:      func() time.Time { return clock },
	}
	task := &db.Task{ID: 1234, Title: "Fix login"}
	want := setUserVarSeq(termVarTask, "#1234 Fix login")

	// Published into a detached session: written, and dropped by tmux.
	r.report(task)
	if len(writes) != 1 || !strings.Contains(writes[0], want) {
		t.Fatalf("first report wrote %q, want it to contain %q", writes, want)
	}

	// Nothing has changed and nobody is watching yet: no point rewriting.
	clock = clock.Add(2 * attachProbeInterval)
	r.report(task)
	if len(writes) != 1 {
		t.Fatalf("got %d writes while still detached, want 1: %q", len(writes), writes)
	}

	// A client attaches. The next report republishes the task it already sent,
	// because that send went nowhere.
	attached = true
	clock = clock.Add(2 * attachProbeInterval)
	r.report(task)
	if len(writes) != 2 || !strings.Contains(writes[1], want) {
		t.Fatalf("after attach, writes = %q, want a republish containing %q", writes, want)
	}

	// And it is published once, not on every Update from then on.
	clock = clock.Add(2 * attachProbeInterval)
	r.report(task)
	r.report(task)
	if len(writes) != 2 {
		t.Errorf("got %d writes after the republish, want 2: %q", len(writes), writes)
	}
}

// tmux is asked at most once per interval while nobody is attached: report runs
// on every key, mouse and tick message, and each answer costs a round trip.
func TestTerminalTaskReporterRateLimitsAttachProbe(t *testing.T) {
	probes := 0
	clock := time.Now()
	r := &terminalTaskReporter{
		inTmux:   true,
		write:    func(string) {},
		attached: func() bool { probes++; return false },
		now:      func() time.Time { return clock },
	}
	task := &db.Task{ID: 7, Title: "Seven"}
	for i := 0; i < 20; i++ {
		r.report(task)
		clock = clock.Add(attachProbeInterval / 10)
	}
	if probes > 3 {
		t.Errorf("asked tmux %d times over %v, want at most 3", probes, 2*attachProbeInterval)
	}
	if probes == 0 {
		t.Error("never asked tmux whether a client had attached")
	}
}

// Outside tmux the write reaches the terminal directly, so nothing is ever
// republished and tmux is never consulted.
func TestTerminalTaskReporterOutsideTmuxNeverProbes(t *testing.T) {
	var writes []string
	r := &terminalTaskReporter{
		write:    func(s string) { writes = append(writes, s) },
		attached: func() bool { t.Fatal("asked tmux about a reporter outside tmux"); return false },
	}
	task := &db.Task{ID: 3, Title: "Three"}
	r.report(task)
	r.report(task)
	if len(writes) != 1 {
		t.Errorf("got %d writes, want 1: %q", len(writes), writes)
	}
}

// End to end for the reported bug: `ty open <id>` must leave the terminal naming
// the task, even though the board and the detail view both loaded before the
// tmux client attached.
func TestOpenTaskOnLoadPublishesTaskAfterAttach(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "One", Status: db.StatusBacklog},
		{ID: 1234, Title: "Fix login", Status: db.StatusBacklog},
	}
	var last string
	attached := false
	clock := time.Now()
	m := &AppModel{
		width:             100,
		height:            50,
		currentView:       ViewDashboard,
		keys:              DefaultKeyMap(),
		kanban:            NewKanbanBoard(100, 50),
		prevStatuses:      map[int64]string{},
		tasksNeedingInput: map[int64]bool{},
		executorPrompts:   map[int64]string{},
		questionPrompts:   map[int64]bool{},
		promptRevisions:   map[int64]uint64{},
		terminalTask: &terminalTaskReporter{
			inTmux:   true,
			write:    func(s string) { last = s },
			attached: func() bool { return attached },
			now:      func() time.Time { return clock },
		},
	}
	m.OpenTaskOnLoad(1234)
	m.Update(tasksLoadedMsg{tasks: tasks})

	// Everything so far went into a session with no client: tmux dropped it.
	last = ""
	attached = true
	clock = clock.Add(2 * attachProbeInterval)
	m.Update(tickMsg(clock))

	if want := setUserVarSeq(termVarTask, "#1234 Fix login"); !strings.Contains(last, want) {
		t.Errorf("after attach, write = %q, want it to contain %q", last, want)
	}
}

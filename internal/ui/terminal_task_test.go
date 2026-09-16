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

// The variables name the task the user is working in, which is the one whose
// detail view is open. The board publishes nothing: its highlight follows the
// cursor, and leaving the last visited task published there left every terminal
// named after a task the user had already backed out of.
func TestReportTerminalTaskOnlyInDetailView(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "One", Status: db.StatusBacklog},
		{ID: 2, Title: "Two", Status: db.StatusBacklog},
	}
	var writes []string
	m := &AppModel{
		width:        100,
		height:       50,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		kanban:       NewKanbanBoard(100, 50),
		terminalTask: &terminalTaskReporter{write: func(s string) { writes = append(writes, s) }},
	}
	m.kanban.SetTasks(tasks)
	blank := setUserVarSeq(termVarTask, "")

	// The board publishes nothing but the opening blank, however the highlight
	// moves.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if len(writes) != 1 || !strings.Contains(writes[0], blank) {
		t.Fatalf("on the board, writes = %q, want one blanking write", writes)
	}

	// Opening a task's detail view names it.
	open := tasks[1]
	m.currentView, m.selectedTask, m.detailView = ViewDetail, open, &DetailModel{}
	m.reportTerminalTask()
	want := setUserVarSeq(termVarTask, fmt.Sprintf("#%d %s", open.ID, open.Title))
	if len(writes) != 2 || !strings.Contains(writes[1], want) {
		t.Fatalf("in detail view, writes = %q, want one containing %q", writes, want)
	}

	// A modal over the detail view is not leaving it: the detail view is still
	// behind it, and blanking the variables would only flicker a tab title.
	m.currentView = ViewChangeStatus
	m.reportTerminalTask()
	if len(writes) != 2 {
		t.Fatalf("a modal over the detail view wrote %q, want nothing", writes[2:])
	}

	// Backing out to the board blanks them again, even though the detail model
	// is still attached — some paths back to the board leave one behind.
	m.currentView = ViewDashboard
	m.reportTerminalTask()
	if len(writes) != 3 || !strings.Contains(writes[2], blank) {
		t.Fatalf("back on the board, writes = %q, want a third blanking write", writes)
	}
	for _, name := range []string{termVarTaskID, termVarTaskTitle} {
		if w := setUserVarSeq(name, ""); !strings.Contains(writes[2], w) {
			t.Errorf("leaving the detail view left %s set: %q", name, writes[2])
		}
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
	// The detail load tasksLoadedMsg asked for lands: `ty open` is in the task.
	m.currentView, m.selectedTask, m.detailView = ViewDetail, tasks[1], &DetailModel{}
	m.Update(tickMsg(clock))

	// Everything so far went into a session with no client: tmux dropped it.
	last = ""
	attached = true
	clock = clock.Add(2 * attachProbeInterval)
	m.Update(tickMsg(clock))

	if want := setUserVarSeq(termVarTask, "#1234 Fix login"); !strings.Contains(last, want) {
		t.Errorf("after attach, write = %q, want it to contain %q", last, want)
	}
}

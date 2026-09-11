package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	osExec "os/exec"

	"github.com/bborn/workflow/internal/db"
)

// iTerm2 user variables naming the task on screen. A tab title, badge or status
// bar can reference them as interpolated strings, e.g. a tab titled
// `ty \(currentSession.user.taskyouTask)` reads "ty #5202 Fix login" (tab
// titles are evaluated in tab scope, hence currentSession). Terminals that don't
// understand OSC 1337 ignore the sequence, so publishing them changes nothing
// unless the user opts in by referencing a variable.
const (
	termVarTask      = "taskyouTask"      // "#5202 Fix login"
	termVarTaskID    = "taskyouTaskId"    // "5202"
	termVarTaskTitle = "taskyouTaskTitle" // "Fix login"
)

// terminalTaskReporter publishes the focused task to the terminal, writing only
// when it changes: Update runs on every key and mouse event.
type terminalTaskReporter struct {
	inTmux bool
	write  func(string)
	last   string
	sent   bool
}

func (r *terminalTaskReporter) report(task *db.Task) {
	var id, title, label string
	if task != nil {
		id = fmt.Sprint(task.ID)
		title = strings.Join(strings.Fields(task.Title), " ")
		label = "#" + id + " " + title
	}
	// The first report always goes out, clearing values left by a previous run.
	if r.sent && label == r.last {
		return
	}
	r.sent = true
	r.last = label
	seq := setUserVarSeq(termVarTask, label) +
		setUserVarSeq(termVarTaskID, id) +
		setUserVarSeq(termVarTaskTitle, title)
	if r.inTmux {
		seq = tmuxPassthrough(seq)
	}
	r.write(seq)
}

// setUserVarSeq is iTerm2's OSC 1337 SetUserVar; the value is base64 so any
// title survives the trip.
func setUserVarSeq(name, value string) string {
	return "\x1b]1337;SetUserVar=" + name + "=" + base64.StdEncoding.EncodeToString([]byte(value)) + "\a"
}

// tmuxPassthrough wraps seq in tmux's DCS passthrough so it reaches the outer
// terminal instead of being swallowed by tmux. ESCs inside are doubled.
func tmuxPassthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// EnableTerminalTaskReport makes the TUI publish its focused task as iTerm2
// user variables. Local TUI only: over SSH, /dev/tty is the server's terminal,
// not the viewer's.
func (m *AppModel) EnableTerminalTaskReport() {
	inTmux := os.Getenv("TMUX") != ""
	if pane := ownPaneID(); inTmux && pane != "" {
		// tmux drops passthrough unless the pane allows it. Scoped to this
		// process's own pane so no other program gains the ability.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		osExec.CommandContext(ctx, "tmux", "set-option", "-p", "-t", pane, "allow-passthrough", "on").Run()
		cancel()
	}
	m.terminalTask = &terminalTaskReporter{inTmux: inTmux, write: writeTTY}
}

// ClearTerminalTask blanks the published variables so a tab title does not keep
// naming a task after ty exits.
func (m *AppModel) ClearTerminalTask() {
	if m.terminalTask != nil {
		m.terminalTask.report(nil)
	}
}

// reportTerminalTask publishes the task the user is looking at: the open task
// in the detail view, otherwise the highlighted card on the board.
func (m *AppModel) reportTerminalTask() {
	if m.terminalTask == nil {
		return
	}
	var task *db.Task
	switch {
	case m.currentView == ViewDetail && m.selectedTask != nil:
		task = m.selectedTask
	case m.kanban != nil:
		task = m.kanban.SelectedTask()
	}
	m.terminalTask.report(task)
}

// writeTTY writes to /dev/tty directly, like ringBellNow, so the sequence
// reaches the terminal rather than Bubble Tea's renderer.
func writeTTY(s string) {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	tty.WriteString(s)
}

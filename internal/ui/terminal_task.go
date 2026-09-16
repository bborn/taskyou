package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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

// attachProbeInterval bounds how often a reporter asks tmux whether anyone is
// attached yet. The answer costs a tmux round trip and only changes once.
const attachProbeInterval = time.Second

// terminalTaskReporter publishes the focused task to the terminal, writing only
// when it changes: Update runs on every key and mouse event.
//
// "When it changes" is not enough on its own inside tmux, which drops a
// passthrough sequence written while no client is attached: see clientAppeared.
type terminalTaskReporter struct {
	inTmux bool
	write  func(string)
	// attached reports whether a tmux client can receive a passthrough
	// sequence now. nil means the question cannot be asked, and a write is
	// assumed to land.
	attached func() bool
	now      func() time.Time // nil: time.Now

	last      string
	sent      bool
	watched   bool      // a client has been seen; every write from now on lands
	nextProbe time.Time // earliest time to ask tmux again
}

func (r *terminalTaskReporter) report(task *db.Task) {
	var id, title, label string
	if task != nil {
		id = fmt.Sprint(task.ID)
		title = strings.Join(strings.Fields(task.Title), " ")
		label = "#" + id + " " + title
	}
	// The first report always goes out, clearing values left by a previous run.
	// So does the first report after a client appears, whose predecessors tmux
	// threw away.
	if r.sent && label == r.last && !r.clientAppeared() {
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

// clientAppeared reports whether a tmux client has just become able to see the
// sequences this reporter writes, meaning the task has to be published again.
//
// tmux hands a passthrough sequence to the clients attached to the pane's
// session and drops it when there are none. `ty open <task>` publishes its task
// in the gap between `new-session -d` and `attach-session`, so every variable it
// set went into a session nobody was watching — and because the task on screen
// never changed afterwards, the change-only write above never published it
// again. The tab title stayed blank for the whole session unless the selection
// was moved by hand — which is why opening a task from the board named it and
// `ty open` did not.
//
// Asked at most once per attachProbeInterval, and never again once answered:
// this runs on the UI thread, and a tmux round trip per task switch would be a
// poor trade for a fact that stops changing after startup.
func (r *terminalTaskReporter) clientAppeared() bool {
	if r.watched {
		return false
	}
	if !r.inTmux || r.attached == nil {
		// Outside tmux the write goes straight to the terminal: nothing to wait
		// for, and nothing published so far was lost.
		r.watched = true
		return false
	}
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	if now.Before(r.nextProbe) {
		return false
	}
	r.nextProbe = now.Add(attachProbeInterval)
	if !r.attached() {
		return false
	}
	r.watched = true
	// Nothing published yet? The caller's own first-report rule covers it.
	return r.sent
}

// tmuxClientAttached reports whether any client is attached to the session
// holding this process's pane. Scoped to that pane for the same reason
// ownSessionName is: an unscoped query answers about the foremost client's
// session, which may belong to another ty.
//
// Unanswerable counts as attached, so a reporter that cannot ask degrades to
// publishing on change rather than shelling out to tmux forever.
func tmuxClientAttached() bool {
	pane := ownPaneID()
	if pane == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := uiTmux(ctx, "display-message", "-t", pane, "-p", "#{session_attached}").Output()
	if err != nil {
		return true
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return true
	}
	return n > 0
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
		uiTmux(ctx, "set-option", "-p", "-t", pane, "allow-passthrough", "on").Run()
		cancel()
	}
	m.terminalTask = &terminalTaskReporter{inTmux: inTmux, write: writeTTY, attached: tmuxClientAttached}
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

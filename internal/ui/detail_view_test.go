package ui

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// These tests drive the real view code against a real, private tmux server:
// a daemon session whose task window holds an agent pane and a shell pane, and
// a UI session whose one pane plays the TUI.

const (
	fixtureDaemon = "task-daemon-qa"
	fixtureUI     = "task-ui-qa"
)

type viewFixture struct {
	agent, shell, window, tui string
	m                         *DetailModel
}

func viewTmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func viewTmuxOK(args ...string) bool { return exec.Command("tmux", args...).Run() == nil }

// dumpTmux logs every pane on the server, for a failing test to show what it saw.
func dumpTmux(t *testing.T) {
	t.Helper()
	out, _ := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{session_name} #{window_id} #{window_name} #{pane_id} #{pane_current_command} viewer=#{@ty_viewer}").CombinedOutput()
	t.Logf("tmux panes:\n%s", out)
}

func waitForTmux(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	dumpTmux(t)
	t.Fatal(msg)
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	tmuxtest.Isolate(t)
	wt := t.TempDir()
	f := &viewFixture{}
	f.agent = viewTmux(t, "new-session", "-d", "-s", fixtureDaemon, "-n", "task-7", "-x", "200", "-y", "50",
		"-c", wt, "-P", "-F", "#{pane_id}", "sleep 600")
	f.window = viewTmux(t, "display-message", "-p", "-t", f.agent, "#{window_id}")
	f.shell = viewTmux(t, "split-window", "-d", "-h", "-t", f.agent, "-c", wt, "-P", "-F", "#{pane_id}", "sleep 600")
	f.tui = viewTmux(t, "new-session", "-d", "-s", fixtureUI, "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "sleep 600")
	t.Setenv("TMUX_PANE", f.tui)

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	f.m = &DetailModel{
		task:               &db.Task{ID: 7, Title: "View fixture", WorktreePath: wt},
		database:           database,
		width:              200,
		height:             50,
		cachedWindowTarget: fixtureDaemon + ":" + f.window,
	}
	return f
}

func paneAlive(pane string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return paneExists(ctx, uiTmux, pane)
}

// paneInSession reports whether pane is in one of session's windows. A window
// linked into a grouped session belongs to both, so #{session_name} alone is
// ambiguous; list the session's panes instead.
func paneInSession(t *testing.T, session, pane string) bool {
	t.Helper()
	for _, id := range strings.Fields(viewTmux(t, "list-panes", "-s", "-t", "="+session, "-F", "#{pane_id}")) {
		if id == pane {
			return true
		}
	}
	return false
}

// assertInDaemonWindow checks pane is in the daemon session's window named
// windowName, and not anywhere in the TUI's session, which dies with the TUI.
func assertInDaemonWindow(t *testing.T, pane, windowName string) {
	t.Helper()
	if got := viewTmux(t, "display-message", "-p", "-t", pane, "#{window_name}"); got != windowName {
		dumpTmux(t)
		t.Fatalf("pane %s is in window %q, want %q", pane, got, windowName)
	}
	if !paneInSession(t, fixtureDaemon, pane) {
		dumpTmux(t)
		t.Fatalf("pane %s is not in the daemon session", pane)
	}
	if paneInSession(t, fixtureUI, pane) {
		dumpTmux(t)
		t.Fatalf("pane %s is in the TUI's session", pane)
	}
}

func (f *viewFixture) waitForClient(t *testing.T) {
	t.Helper()
	waitForTmux(t, func() bool {
		out, err := exec.Command("tmux", "list-clients", "-t", f.m.viewSession).Output()
		return err == nil && strings.TrimSpace(string(out)) != ""
	}, "no client attached to the view session")
}

func TestViewShowsTheTaskWindowWithoutMovingPanes(t *testing.T) {
	f := newViewFixture(t)
	f.m.viewTaskWindow()
	m := f.m
	if m.viewerPaneID == "" || m.viewSession == "" {
		dumpTmux(t)
		t.Fatalf("no view: viewer %q, session %q, error %q", m.viewerPaneID, m.viewSession, m.paneError)
	}

	// The viewer sits in the TUI's window, marked as one of ty's.
	if got, want := viewTmux(t, "display-message", "-p", "-t", m.viewerPaneID, "#{window_id}"),
		viewTmux(t, "display-message", "-p", "-t", f.tui, "#{window_id}"); got != want {
		t.Errorf("viewer in window %s, TUI in %s", got, want)
	}
	if got := viewTmux(t, "show-options", "-pqv", "-t", m.viewerPaneID, viewerOption); got != m.viewSession {
		t.Errorf("viewer marker = %q, want %q", got, m.viewSession)
	}
	// The view session looks at the task's window.
	if got := viewTmux(t, "display-message", "-p", "-t", m.viewSession+":", "#{window_id}"); got != f.window {
		t.Errorf("view shows window %s, want %s", got, f.window)
	}
	// Nothing moved: both panes are still in the daemon's task window.
	assertInDaemonWindow(t, f.agent, "task-7")
	assertInDaemonWindow(t, f.shell, "task-7")
	if m.claudePaneID != f.agent || m.workdirPaneID != f.shell {
		t.Errorf("agent/shell = %s/%s, want %s/%s", m.claudePaneID, m.workdirPaneID, f.agent, f.shell)
	}
	if got := viewTmux(t, "show-options", "-pqv", "-t", f.agent, paneRoleOption); got != paneRoleAgent {
		t.Errorf("agent pane role = %q", got)
	}
	// Shift+arrow navigation finds the view from the TUI pane and back.
	if got := viewTmux(t, "show-options", "-pqv", "-t", f.tui, viewPaneOption); got != m.viewerPaneID {
		t.Errorf("TUI pane's view pane = %q, want %q", got, m.viewerPaneID)
	}
	if got := viewTmux(t, "show-options", "-pqv", "-t", m.viewerPaneID, viewTUIOption); got != f.tui {
		t.Errorf("view pane's TUI pane = %q, want %q", got, f.tui)
	}
	f.waitForClient(t)
}

func TestPaneCycleScriptFitsInSingleQuotes(t *testing.T) {
	for _, next := range []bool{true, false} {
		s := paneCycleScript(next)
		if strings.Contains(s, "'") {
			t.Errorf("paneCycleScript(%v) contains a single quote: %s", next, s)
		}
		if !strings.Contains(s, "##{pane_at_") {
			t.Errorf("paneCycleScript(%v) lets tmux expand the edge test on the wrong server: %s", next, s)
		}
	}
}

func TestClosingTheViewLeavesTheTaskRunning(t *testing.T) {
	f := newViewFixture(t)
	f.m.viewTaskWindow()
	viewer, view := f.m.viewerPaneID, f.m.viewSession
	f.waitForClient(t)

	f.m.closeTaskWindowView(false)

	waitForTmux(t, func() bool { return !paneAlive(viewer) }, "viewer pane survived closing the view")
	if got := viewTmux(t, "show-options", "-pqv", "-t", f.tui, viewPaneOption); got != "" {
		t.Errorf("TUI pane still points at a closed view: %q", got)
	}
	waitForTmux(t, func() bool { return !viewTmuxOK("has-session", "-t", "="+view) }, "view session survived closing the view")
	assertInDaemonWindow(t, f.agent, "task-7")
	assertInDaemonWindow(t, f.shell, "task-7")
}

// The shell's process must outlive the TUI, so while hidden it waits in the
// daemon's session, never in the TUI's.
func TestHiddenShellStaysInTheDaemonSession(t *testing.T) {
	f := newViewFixture(t)
	f.m.viewTaskWindow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	f.m.hideShellPane(ctx)
	assertInDaemonWindow(t, f.shell, "_hidden_shell_7")
	f.m.showShellPane(ctx)
	assertInDaemonWindow(t, f.shell, "task-7")
}

func TestHiddenShellPreferenceAppliesWhenTheViewOpens(t *testing.T) {
	f := newViewFixture(t)
	f.m.shellPaneHidden = true
	f.m.viewTaskWindow()
	assertInDaemonWindow(t, f.shell, "_hidden_shell_7")
	if f.m.workdirPaneID != f.shell {
		t.Errorf("hidden shell not tracked: %q", f.m.workdirPaneID)
	}
}

// If the task's window closes, tmux moves a session to another of its windows.
// A view that followed would put another task's agent under the TUI; it must
// end instead.
func TestViewEndsWhenTheTaskWindowCloses(t *testing.T) {
	f := newViewFixture(t)
	viewTmux(t, "new-window", "-d", "-t", fixtureDaemon+":", "-n", "task-8", "sleep 600")
	f.m.viewTaskWindow()
	view := f.m.viewSession
	f.waitForClient(t)

	viewTmux(t, "kill-window", "-t", f.window)

	waitForTmux(t, func() bool { return !viewTmuxOK("has-session", "-t", "="+view) },
		"the view kept running after the task's window closed")
}

// A TUI that dies without cleaning up (a crash, kill -9) takes its view pane
// with it, so the terminal is not left showing an agent through a pane nothing
// manages, and the view session goes too. The agent stays where it is.
func TestViewGoesWhenTheTUIDies(t *testing.T) {
	f := newViewFixture(t)
	tui := exec.Command("sleep", "600")
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tui.Process.Kill(); _ = tui.Wait() })
	orig := tuiPID
	tuiPID = func() int { return tui.Process.Pid }
	t.Cleanup(func() { tuiPID = orig })

	f.m.viewTaskWindow()
	viewer, view := f.m.viewerPaneID, f.m.viewSession
	f.waitForClient(t)

	_ = tui.Process.Kill()
	_ = tui.Wait()

	waitForTmux(t, func() bool { return !paneAlive(viewer) }, "the view pane outlived the TUI")
	waitForTmux(t, func() bool { return !viewTmuxOK("has-session", "-t", "="+view) }, "the view session outlived the TUI")
	assertInDaemonWindow(t, f.agent, "task-7")
	assertInDaemonWindow(t, f.shell, "task-7")
}

// When the task's window closes under the view, ty sets the task up again the
// way opening it does, from the status the database has now: a running task
// waits for the daemon's executor (the full wait, not what is left of the
// first one), and a finished task is left alone.
func TestWindowClosingUnderTheViewSetsUpAgain(t *testing.T) {
	for _, status := range []string{db.StatusProcessing, db.StatusDone} {
		t.Run(status, func(t *testing.T) {
			tmuxtest.Isolate(t)
			stale := &db.Task{ID: 7, Title: "View fixture", Status: db.StatusProcessing, WorktreePath: t.TempDir()}
			fresh := *stale
			fresh.Status = status
			m := &DetailModel{task: stale, claudePaneID: "%1", viewerPaneID: "%2", paneLoadingStart: time.Now().Add(-time.Hour)}

			cmd := m.applyPaneHealth(paneHealthMsg{claudePaneID: "%1", viewerPaneID: "%2", task: &fresh})
			if cmd == nil {
				t.Fatal("nothing was set up after the window closed")
			}
			if m.task.Status != status {
				t.Errorf("decided from status %q, want the database's %q", m.task.Status, status)
			}
			if time.Since(m.paneLoadingStart) > time.Minute {
				t.Error("the wait for the daemon's executor did not start over")
			}
			switch res := cmd().(detailPaneResultMsg).result.(type) {
			case paneWaitForExecutorMsg:
				if status != db.StatusProcessing {
					t.Errorf("a %s task waits for an executor", status)
				}
			case panesJoinedMsg:
				if status != db.StatusDone || res.claudePaneID != "" || res.err != nil {
					t.Errorf("a %s task got %+v", status, res)
				}
			default:
				t.Errorf("a %s task got %T", status, res)
			}
		})
	}
}

// A task the database no longer returns (deleted along with its window) gets
// no agent.
func TestWindowClosingForADeletedTaskStartsNothing(t *testing.T) {
	m := &DetailModel{task: &db.Task{ID: 7, Status: db.StatusProcessing, WorktreePath: t.TempDir()}, claudePaneID: "%1"}
	if cmd := m.applyPaneHealth(paneHealthMsg{claudePaneID: "%1"}); cmd != nil {
		t.Fatal("set up a task the database did not return")
	}
}

func TestPaneProbeReadsTheTaskWhenItsWindowIsGone(t *testing.T) {
	tmuxtest.Isolate(t)
	t.Setenv("TMUX", "qa-test") // the probe only runs inside tmux
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	task := &db.Task{Title: "Finished while viewed", Status: db.StatusDone}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	stale := *task
	stale.Status = db.StatusProcessing
	m := &DetailModel{task: &stale, database: database, claudePaneID: "%1"}

	msg := m.paneHealthCmd()().(detailPaneResultMsg).result.(paneHealthMsg)
	if msg.alive || msg.hasWindow {
		t.Fatalf("the probe found a pane or window on an empty server: %+v", msg)
	}
	if msg.task == nil || msg.task.Status != db.StatusDone {
		t.Fatalf("the probe read %+v, want the database's done task", msg.task)
	}
}

// Stale view panes are cleared; the user's own panes in the same window are
// not (ty may run inside the user's tmux).
func TestStaleViewersGoButUserPanesStay(t *testing.T) {
	f := newViewFixture(t)
	userPane := viewTmux(t, "split-window", "-d", "-t", f.tui, "-P", "-F", "#{pane_id}", "sleep 600")
	stale := viewTmux(t, "split-window", "-d", "-t", f.tui, "-P", "-F", "#{pane_id}", "sleep 600")
	viewTmux(t, "set-option", "-p", "-t", stale, viewerOption, "ty-view-old")

	f.m.viewTaskWindow()

	waitForTmux(t, func() bool { return !paneAlive(stale) }, "a stale view pane survived")
	if !paneAlive(userPane) {
		dumpTmux(t)
		t.Error("the user's own pane was killed")
	}
}

package ui

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/tmuxtest"
)

// Shift+arrow asks the ACTIVE pane whether a local task view is involved
// (bindPaneNavigation's if-shell), so what these tests check is that condition:
// 1 sends the key through paneCycleScript, 0 through the plain pane cycle.
func inViewCondition(t *testing.T, pane string) string {
	t.Helper()
	return viewTmux(t, "display-message", "-p", "-t", pane,
		"#{||:#{"+viewTUIOption+"},#{"+viewPaneOption+"}}")
}

// remoteNavFixture is a UI session whose single pane plays the TUI, with the
// environment a remote attach expects.
func remoteNavFixture(t *testing.T) (*DetailModel, string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	tmuxtest.Isolate(t)
	wt := t.TempDir()
	tui := viewTmux(t, "new-session", "-d", "-s", "task-ui-remote-qa", "-x", "200", "-y", "50",
		"-c", wt, "-P", "-F", "#{pane_id}", "sleep 600")
	t.Setenv("TMUX_PANE", tui)
	// attachRemotePane refuses to touch tmux unless the TUI is inside it, and
	// uiTmux follows $TMUX's socket — this test server's.
	t.Setenv("TMUX", viewTmux(t, "display-message", "-p", "-t", tui, "#{socket_path}")+",1,1")

	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	return &DetailModel{
		task: &db.Task{ID: 9, Title: "Remote task", WorktreePath: wt,
			DaemonSession: "task-daemon-far", PlacementTarget: "buildbox"},
		database:        database,
		width:           200,
		height:          50,
		shellPaneHidden: true, // the shell view needs a reachable host
	}, tui
}

// A remotely placed task is shown by a local ssh pane, not by a task-window
// view, so the TUI pane must stop claiming one. While it still did, every
// Shift+arrow pressed in the TUI went through paneCycleScript and tried to
// select a view pane and a view session that had both been disposed of: the key
// did nothing at all and the keyboard could not leave the TUI.
func TestRemoteAttachDropsALeftOverViewPairing(t *testing.T) {
	m, tui := remoteNavFixture(t)

	// What a local task leaves on this pane, once its view is gone.
	viewTmux(t, "set-option", "-p", "-t", tui, viewPaneOption, "%999")
	viewTmux(t, "set-option", "-p", "-t", tui, viewSessionOption, "ty-view-gone")
	if got := inViewCondition(t, tui); got != "1" {
		t.Fatalf("fixture did not reproduce a stale pairing: condition = %q", got)
	}

	pane := m.attachRemotePane(executor.RemoteTaskLocation{Host: "buildbox", WorkDir: m.task.WorktreePath})
	if pane == "" {
		dumpTmux(t)
		t.Fatal("no remote pane")
	}

	if got := viewTmux(t, "show-options", "-pqv", "-t", tui, viewPaneOption); got != "" {
		t.Errorf("TUI pane still points at a view pane: %q", got)
	}
	if got := viewTmux(t, "show-options", "-pqv", "-t", tui, viewSessionOption); got != "" {
		t.Errorf("TUI pane still points at a view session: %q", got)
	}
	if got := inViewCondition(t, tui); got != "0" {
		dumpTmux(t)
		t.Fatalf("Shift+arrow from the TUI still routes into a task view: condition = %q", got)
	}
	// The remote pane is a plain pane of the TUI's window, which is what the
	// unpaired binding cycles.
	if got := inViewCondition(t, pane); got != "0" {
		t.Errorf("Shift+arrow from the remote pane routes into a task view: condition = %q", got)
	}
	if got, want := viewTmux(t, "display-message", "-p", "-t", pane, "#{window_id}"),
		viewTmux(t, "display-message", "-p", "-t", tui, "#{window_id}"); got != want {
		t.Errorf("remote pane in window %s, TUI in %s", got, want)
	}
}

// Opening a view writes the pairing again when it succeeds, so clearing it up
// front costs nothing — and every path in between can fail and return.
func TestViewSetupDropsALeftOverViewPairingBeforeRebuilding(t *testing.T) {
	f := newViewFixture(t)
	viewTmux(t, "set-option", "-p", "-t", f.tui, viewPaneOption, "%999")
	viewTmux(t, "set-option", "-p", "-t", f.tui, viewSessionOption, "ty-view-gone")

	// No window to show: viewTaskWindow gives up early, and must not leave the
	// dead pairing behind when it does.
	f.m.cachedWindowTarget = fixtureDaemon + ":@9999"
	f.m.viewTaskWindow()

	if got := inViewCondition(t, f.tui); got != "0" {
		dumpTmux(t)
		t.Fatalf("a failed view setup left Shift+arrow aimed at the old view: condition = %q", got)
	}
}

// The health check is the one place a view is declared gone without
// closeTaskWindowView running, and what follows may never build another one (a
// blocked or finished task gets no view). It has to say so on the TUI pane too.
func TestPaneHealthDropsThePairingWhenTheViewDies(t *testing.T) {
	f := newViewFixture(t)
	f.m.viewTaskWindow()
	if f.m.viewerPaneID == "" {
		dumpTmux(t)
		t.Fatalf("no view to lose: %q", f.m.paneError)
	}
	if got := inViewCondition(t, f.tui); got != "1" {
		t.Fatalf("view did not pair with the TUI pane: condition = %q", got)
	}

	f.m.applyPaneHealth(paneHealthMsg{
		claudePaneID: f.m.claudePaneID,
		viewerPaneID: f.m.viewerPaneID,
	})
	f.m.paneWork.Wait()

	if got := inViewCondition(t, f.tui); got != "0" {
		dumpTmux(t)
		t.Fatalf("Shift+arrow still aims at the view that died: condition = %q", got)
	}
}

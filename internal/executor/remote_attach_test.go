package executor

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func attachTask() *db.Task {
	return &db.Task{ID: 5250, DaemonSession: "task-daemon-2543136", PlacementTarget: "ol-agents"}
}

// attachChain is the script the PLACED HOST runs, unwrapped from the two layers
// of shell quoting it travels through.
func attachChain(t *testing.T) string {
	t.Helper()
	return remoteAttachChain(attachTask(), remoteViewSession(5250))
}

func attachScript(t *testing.T) string {
	t.Helper()
	task := attachTask()
	loc := RemoteTaskLocation{
		Host:    "ol-agents",
		WorkDir: "/home/olgm/projects/x/.task-worktrees/5250-thing",
		Branch:  "task/5250-thing",
		Attach:  "ssh ol-agents -t tmux attach -t task-daemon-2543136:task-5250",
	}
	return RemoteAttachScript(task, loc)
}

// A view takes no prefix, exactly as the local detail view's does: every key
// belongs to what is running on the host. An earlier version gave the inner
// session C-a to work around the nested-prefix collision, which handed the user
// a second prefix to learn and took start-of-line away from every shell and
// agent input box in the pane.
//
// Whatever is set, it must be set on the disposable VIEW session and not on the
// daemon session the agent lives in.
func TestRemoteAttachScriptLeavesEveryKeyToTheAgent(t *testing.T) {
	script := attachChain(t)

	if !strings.Contains(script, "prefix None") {
		t.Errorf("the view keeps a prefix of its own, so keys are eaten before the agent sees them:\n%s", script)
	}
	if !strings.Contains(script, "prefix2 None") {
		t.Error("the view keeps its second prefix, so C-b is still eaten")
	}
	if strings.Contains(script, "prefix C-") {
		t.Error("the view binds a prefix key, which the agent and the remote shell need for themselves")
	}
	view := remoteViewSession(5250)
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "prefix") && !strings.Contains(line, view) {
			t.Errorf("prefix is set on something other than the view session: %q", line)
		}
	}
}

// The daemon session on a host holds one window per placed task. If the task's
// window ends, tmux moves the grouped view to a neighbouring window — another
// task's agent — and the pane under the TUI goes on rendering as if nothing had
// happened. The local view guards this with the same hook.
func TestRemoteAttachScriptEndsTheViewRatherThanShowAnotherTask(t *testing.T) {
	script := attachChain(t)
	view := remoteViewSession(5250)

	hook := -1
	for i, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "session-window-changed") {
			hook = i
			if !strings.Contains(line, "kill-session") || !strings.Contains(line, view) {
				t.Errorf("the window-changed hook does not end this view: %q", line)
			}
		}
	}
	if hook < 0 {
		t.Fatalf("nothing stops the view from following the daemon session to another task's window:\n%s", script)
	}
	// The hook must be installed AFTER the view is pointed at the task's window,
	// or ty's own select-window fires it and kills the session it just made.
	selectAt := strings.Index(script, "select-window")
	hookAt := strings.Index(script, "session-window-changed")
	if selectAt < 0 || hookAt < selectAt {
		t.Error("the hook is installed before select-window, so the view kills itself on open")
	}
}

// The window is shared with a daemon session nobody is attached to. Without
// this the agent can render for a size that is not the pane's.
func TestRemoteAttachScriptSizesTheWindowToWhoeverIsLooking(t *testing.T) {
	script := attachChain(t)
	if !strings.Contains(script, "window-size latest") {
		t.Errorf("the viewed window does not follow the viewer's size:\n%s", script)
	}
}

// A grouped view session, disposed of when the pane closes: attaching straight
// to the daemon session would resize it for every other task placed on that host.
func TestRemoteAttachScriptUsesADisposableGroupedSession(t *testing.T) {
	script := attachChain(t)
	if !strings.Contains(script, "new-session -d -s '"+remoteViewSession(5250)+"' -t 'task-daemon-2543136'") {
		t.Errorf("attach script does not create a grouped view session:\n%s", script)
	}
	if !strings.Contains(script, "attach-session") || !strings.Contains(script, "destroy-unattached on") {
		t.Error("the view session is never marked disposable, so it outlives the pane")
	}
	// destroy-unattached must be chained onto the attach, not set before it, or
	// tmux disposes of the session before the client connects.
	attachAt := strings.Index(script, "attach-session")
	destroyAt := strings.Index(script, "destroy-unattached")
	if destroyAt < attachAt {
		t.Error("destroy-unattached is set before the client attaches; the session dies first")
	}
}

// The pane must never blank or hang. Whatever ends the ssh client — the agent
// finishing, the link dropping — the pane says what happened and keeps the
// manual attach command in front of the user.
func TestRemoteAttachScriptExplainsItselfWhenTheSessionEnds(t *testing.T) {
	script := attachScript(t)
	for _, want := range []string{
		"the remote session ended",
		"disconnected from the remote session",
		"Reattach by hand",
		"ssh ol-agents -t tmux attach -t task-daemon-2543136:task-5250",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("attach script never says %q:\n%s", want, script)
		}
	}
	if !strings.Contains(script, "read _") {
		t.Error("the pane exits immediately after printing, so nobody can read it")
	}
}

// An attach that stopped to ask for a passphrase inside a tmux pane would simply
// look frozen.
func TestRemoteAttachScriptRunsSSHNonInteractively(t *testing.T) {
	script := attachScript(t)
	if !strings.Contains(script, "BatchMode=yes") {
		t.Error("the attach can drop into an interactive ssh prompt inside the pane")
	}
	if !strings.Contains(script, "-t 'ol-agents'") {
		t.Error("ssh is not asked for a TTY, so tmux refuses to attach")
	}
}

// The notice shares one right-aligned header line with the status, project and
// PR badges. It says where the pane is, and it stays a badge: prose there wraps
// and leaves its own tail stranded on a line by itself. There is no prefix to
// document — the view answers to the same keys a local task's pane does.
func TestRemoteAttachNoticeNamesWhereThePaneIsAndStaysShort(t *testing.T) {
	notice := RemoteAttachNotice(RemoteTaskLocation{Host: "ol-agents", Branch: "task/5250-x"})
	if !strings.Contains(notice, "ol-agents") {
		t.Errorf("notice does not name the host: %q", notice)
	}
	if !strings.Contains(notice, "task/5250-x") {
		t.Errorf("notice does not name the branch: %q", notice)
	}
	if len([]rune(notice)) > 60 {
		t.Errorf("notice is prose, not a badge, and will wrap the header line: %q", notice)
	}
	if strings.Contains(strings.ToLower(notice), "prefix") {
		t.Errorf("a view has no prefix of its own, so the badge must not teach one: %q", notice)
	}
}

// "The window is gone" and "I cannot see the host" must not read the same way:
// one is a finished session, the other is a task that is probably still running.
func TestRemoteFallbackMessagesKeepTheAttachCommandAndDoNotBlameEachOther(t *testing.T) {
	loc := RemoteTaskLocation{Host: "ol-agents", WorkDir: "/w", Attach: "ssh ol-agents -t tmux attach -t s:w"}

	ended := RemoteEndedMessage(loc)
	if !strings.Contains(ended, "has ended") || !strings.Contains(ended, loc.Attach) {
		t.Errorf("ended message = %q", ended)
	}
	unreachable := RemoteUnreachableMessage(loc)
	if !strings.Contains(unreachable, "Cannot reach") || !strings.Contains(unreachable, "may still be running") {
		t.Errorf("unreachable message = %q", unreachable)
	}
	if strings.Contains(unreachable, "has ended") {
		t.Error("an unreachable host is reported as a finished session")
	}
}

// Task 5245 spent its last turns calling `ty complete` and `ty artifact list`
// and getting "task not found" — because ty's own prompt told it to, and the ty
// on that host talks to that host's task store. A placed task must not be given
// instructions that cannot work where it runs.
func TestRemoteGuidanceDoesNotTellTheAgentToCallCommandsThatCannotWorkThere(t *testing.T) {
	guidance := remoteUniversalGuidance(&db.Task{ID: 5245, PlacementTarget: "ol-agents"}, true)

	for _, forbidden := range []string{
		"call taskyou_complete with a one-paragraph summary",
		"ty complete --summary",
		"ty artifact set",
		"ty artifact get",
	} {
		if strings.Contains(guidance, forbidden) {
			t.Errorf("remote guidance still instructs the agent to run %q", forbidden)
		}
	}
	// It must instead say, once, that those calls are unavailable here.
	if !strings.Contains(guidance, "Do not call taskyou_complete") {
		t.Error("remote guidance never tells the agent the completion tools are not there")
	}
	// It must say what DOES finish a remote run, or the agent is left guessing.
	for _, want := range []string{"ol-agents", "open a PR", "STOP", "watching this session"} {
		if !strings.Contains(guidance, want) {
			t.Errorf("remote guidance never mentions %q:\n%s", want, guidance)
		}
	}
}

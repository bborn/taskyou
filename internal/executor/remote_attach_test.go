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

// The nested-prefix collision is the thing that makes or breaks this feature:
// two tmux servers both listening for C-b means the inner one never sees a
// prefix key. The attach must rebind the inner session, and must do it on the
// disposable VIEW session rather than on the daemon session the agent lives in.
func TestRemoteAttachScriptRebindsTheInnerPrefix(t *testing.T) {
	script := attachChain(t)

	if !strings.Contains(script, "prefix "+RemoteInnerPrefix) {
		t.Errorf("attach script never sets the inner prefix:\n%s", script)
	}
	if !strings.Contains(script, "prefix2 None") {
		t.Error("the inner session keeps its second prefix, so C-b still collides")
	}
	view := remoteViewSession(5250)
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "prefix") && !strings.Contains(line, view) {
			t.Errorf("prefix is set on something other than the view session: %q", line)
		}
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

// The prefix is documented where the user is looking, not in a commit message.
func TestRemoteAttachNoticeDocumentsTheKeybinding(t *testing.T) {
	notice := RemoteAttachNotice(RemoteTaskLocation{Host: "ol-agents", Branch: "task/5250-x"})
	if !strings.Contains(notice, RemoteInnerPrefixHuman) {
		t.Errorf("notice does not name the inner prefix: %q", notice)
	}
	if !strings.Contains(notice, "ol-agents") {
		t.Errorf("notice does not name the host: %q", notice)
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

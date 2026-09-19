package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func codeTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// The coordinator usually HAS a directory at a placed task's path — its own
// checkout of the same project, or the worktree the task had before it moved —
// so a surface that reads worktree_path and opens it does not fail, it opens the
// wrong repository. Resolution has to answer with the host as well as the path.
func TestCodeLocationSaysWhichMachineTheCodeIsOn(t *testing.T) {
	database := codeTestDB(t)
	e := &Executor{db: database}

	local := &db.Task{Title: "local", WorktreePath: "/home/me/p/.task-worktrees/1-x"}
	if err := database.CreateTask(local); err != nil {
		t.Fatal(err)
	}
	if loc := e.CodeLocation(local); loc.Remote() || loc.Path != local.WorktreePath {
		t.Errorf("local task resolved to %+v", loc)
	}

	// A task that ran here and was then placed keeps its old local worktree path.
	placed := &db.Task{Title: "placed", WorktreePath: "/home/me/p/.task-worktrees/2-x", PlacementTarget: "ol-agents"}
	if err := database.CreateTask(placed); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(placed.ID, "/home/olgm/p/.task-worktrees/2-x", "task/2-x"); err != nil {
		t.Fatal(err)
	}
	loc := e.CodeLocation(placed)
	if !loc.Remote() || loc.Host != "ol-agents" {
		t.Errorf("placed task is not reported as remote: %+v", loc)
	}
	if loc.Path != "/home/olgm/p/.task-worktrees/2-x" {
		t.Errorf("placed task resolved to the stale local worktree: %+v", loc)
	}
}

func TestEditorCommandOpensRemoteWorktreesOverSSHAndRefusesToGuess(t *testing.T) {
	remote := CodeLocation{Host: "ol-agents", Path: "/srv/w/5250"}

	argv, ok := EditorCommand("/usr/local/bin/code", remote)
	if !ok {
		t.Fatal("VS Code cannot be asked to open a directory on another machine")
	}
	if got := strings.Join(argv, " "); got != "/usr/local/bin/code --remote ssh-remote+ol-agents /srv/w/5250" {
		t.Errorf("remote open command = %q", got)
	}

	// An editor with no ssh support gets nothing rather than the same path here.
	if argv, ok := EditorCommand("vim", remote); ok {
		t.Errorf("vim was pointed at a path on this machine instead: %v", argv)
	}

	local := CodeLocation{Path: "/home/me/w/5250"}
	if argv, ok := EditorCommand("vim", local); !ok || strings.Join(argv, " ") != "vim /home/me/w/5250" {
		t.Errorf("local open command = %v (ok=%v)", argv, ok)
	}
	if _, ok := EditorCommand("", local); ok {
		t.Error("an unset editor produced a command")
	}
}

func TestRemoteCodeURIIsOnlyBuiltForRemoteWork(t *testing.T) {
	uri := RemoteCodeURI(CodeLocation{Host: "ol-agents", Path: "/srv/w/5250"})
	if uri != "vscode://vscode-remote/ssh-remote+ol-agents/srv/w/5250" {
		t.Errorf("remote code URI = %q", uri)
	}
	if got := RemoteCodeURI(CodeLocation{Path: "/srv/w/5250"}); got != "" {
		t.Errorf("a local worktree produced a remote URI: %q", got)
	}
	// A checkout under a directory with a space in it is ordinary on a Mac, and
	// an unescaped one produces a URI `open` rejects and window.open truncates.
	spaced := RemoteCodeURI(CodeLocation{Host: "ol-agents", Path: "/Users/me/My Projects/app"})
	if strings.Contains(spaced, " ") {
		t.Errorf("remote code URI is not URL-escaped: %q", spaced)
	}
	if spaced != "vscode://vscode-remote/ssh-remote+ol-agents/Users/me/My%20Projects/app" {
		t.Errorf("escaped URI = %q", spaced)
	}
}

// An ssh destination is not necessarily a hostname: fleets name their hosts in
// ~/.ssh/config, and a URL built from such a name is one no browser can open.
func TestHostAddressAsksSSHWhatItWouldActuallyDial(t *testing.T) {
	stubSSH(t, "#!/bin/sh\ncase \"$*\" in *-G*) echo 'user olgm'; echo 'hostname 10.0.0.4'; echo 'port 22' ;; esac\nexit 0\n")
	if got := HostAddress(context.Background(), "alias-in-ssh-config"); got != "10.0.0.4" {
		t.Errorf("host address = %q, want the hostname ssh resolves", got)
	}
	// Asked again, without ssh: the answer is memoized, not re-derived.
	t.Cleanup(setSSHBinary(filepath.Join(t.TempDir(), "no-ssh-here")))
	if got := HostAddress(context.Background(), "alias-in-ssh-config"); got != "10.0.0.4" {
		t.Errorf("second lookup = %q", got)
	}
	// When ssh cannot answer, the destination itself is the best guess — minus
	// the user, which is not part of an address.
	if got := HostAddress(context.Background(), "olgm@build-box"); got != "build-box" {
		t.Errorf("fallback address = %q", got)
	}
	if got := HostAddress(context.Background(), "local"); got != "" {
		t.Errorf("a local task produced a host address: %q", got)
	}
}

// A lookup that failed must not be remembered. The fallback is an ssh alias, and
// an alias is exactly what a browser cannot resolve — so caching one bad moment
// (ssh missing from PATH, a failing Match exec block, a slow config) would pin a
// useless address for as long as ty runs.
func TestHostAddressDoesNotRememberAFailedLookup(t *testing.T) {
	stubSSH(t, "#!/bin/sh\nexit 255\n")
	if got := HostAddress(context.Background(), "flaky-lookup-host"); got != "flaky-lookup-host" {
		t.Fatalf("failed lookup = %q, want the destination itself", got)
	}
	stubSSH(t, "#!/bin/sh\ncase \"$*\" in *-G*) echo 'hostname 10.1.2.3' ;; esac\nexit 0\n")
	if got := HostAddress(context.Background(), "flaky-lookup-host"); got != "10.1.2.3" {
		t.Errorf("second lookup = %q; the earlier failure was cached", got)
	}
}

func TestTaskServerURLFollowsTheTaskToItsHost(t *testing.T) {
	stubSSH(t, "#!/bin/sh\ncase \"$*\" in *-G*) echo 'hostname agents.internal' ;; esac\nexit 0\n")

	local := &db.Task{ID: 1, Port: 3010}
	if got := TaskServerURL(context.Background(), local, "http://localhost"); got != "http://localhost:3010" {
		t.Errorf("local server URL = %q", got)
	}
	placed := &db.Task{ID: 2, Port: 3010, PlacementTarget: "agents-alias-for-url"}
	if got := TaskServerURL(context.Background(), placed, "http://localhost"); got != "http://agents.internal:3010" {
		t.Errorf("placed server URL = %q, want the host's address", got)
	}
	if got := TaskServerURL(context.Background(), &db.Task{ID: 3}, "http://localhost"); got != "" {
		t.Errorf("a task with no port produced a URL: %q", got)
	}
}

// The port a placed task's server listens on is a port on the host. Asked here,
// the answer is about this machine: "no server" while one is running, or "yes"
// about something else that happens to hold the same port locally.
func TestPortListeningIsAskedOnTheMachineTheTaskRunsOn(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	stubSSH(t, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+shellQuote(calls)+"\nexit 0\n")

	if !PortListening(context.Background(), &db.Task{ID: 1, Port: 3010, PlacementTarget: "ol-agents"}) {
		t.Error("a listening port on the host was reported as closed")
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the port was never checked on the host: %v", err)
	}
	asked := unquoteRemoteCommands(string(recorded))
	if !strings.Contains(asked, "ol-agents") || !strings.Contains(asked, ":3010") {
		t.Errorf("the host was not asked about the task's port:\n%s", asked)
	}
	// A minimal fleet image has no lsof, and "no lsof" must not read as
	// "no server".
	if !strings.Contains(asked, "ss -ltnH") {
		t.Errorf("no fallback for a host without lsof:\n%s", asked)
	}
}

package executor

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Where a task's code is, and how to reach it from here.
//
// Every surface has actions that assume the answer is "in a directory on this
// machine": open the worktree in an editor, open the directory, open the dev
// server the task started. For a placed task the answer is "in a directory on
// another machine", and the difference is not cosmetic — the coordinator often
// has a directory at the very same path (its own checkout of the same project,
// or the worktree the task had before it was moved), so acting on the path
// without asking where it is does not fail, it silently acts on the wrong
// repository.

// CodeLocation is the directory a task's work is in.
type CodeLocation struct {
	// Host is the ssh destination the task was placed on, or "" when the code is
	// on this machine.
	Host string
	Path string
}

// Remote reports whether the code is on another machine. "local" is a
// destination a placement decision can hold, and it means this machine.
func (l CodeLocation) Remote() bool { return IsRemotePlacement(l.Host) }

// CodeLocation resolves where a task's checkout is: the remote worktree on the
// host it was placed on, or the local worktree.
func (e *Executor) CodeLocation(task *db.Task) CodeLocation {
	var database *db.DB
	if e != nil {
		database = e.db
	}
	return TaskCodeLocation(database, task)
}

// TaskCodeLocation is CodeLocation for callers that hold a database but no
// executor, which is every HTTP handler.
func TaskCodeLocation(database *db.DB, task *db.Task) CodeLocation {
	if task == nil {
		return CodeLocation{}
	}
	if IsRemotePlacement(task.PlacementTarget) {
		loc := CodeLocation{Host: task.PlacementTarget}
		if database != nil {
			loc.Path, _, _ = database.GetTaskRemoteWorktree(task.ID)
		}
		return loc
	}
	return CodeLocation{Path: task.WorktreePath}
}

// remoteCapableEditors are the editors that can open a directory on another
// machine over ssh. VS Code and its forks all take the same two flags.
var remoteCapableEditors = map[string]bool{
	"code": true, "code-insiders": true, "codium": true, "vscodium": true,
	"cursor": true, "windsurf": true, "positron": true, "trae": true,
}

// EditorCommand is the argv that opens loc in editor, and whether that editor
// can open it at all.
//
// A local directory is what every editor already takes. A directory on another
// machine is only openable by the VS Code family, through its ssh remote
// support — and when the editor cannot do it, the answer has to be "no", not
// "open the same path here": the path exists on this machine often enough, with
// a different repository in it, that opening it would be the worst outcome
// available.
func EditorCommand(editor string, loc CodeLocation) ([]string, bool) {
	if editor == "" || loc.Path == "" {
		return nil, false
	}
	if !loc.Remote() {
		return []string{editor, loc.Path}, true
	}
	if !remoteCapableEditors[strings.ToLower(filepath.Base(editor))] {
		return nil, false
	}
	return []string{editor, "--remote", "ssh-remote+" + loc.Host, loc.Path}, true
}

// RemoteCodeURI opens a placed task's worktree in VS Code (or a fork) from a
// surface that can only hand a URL to the OS, which is what the desktop app and
// the browser have. It is the URL form of EditorCommand's --remote flag.
func RemoteCodeURI(loc CodeLocation) string {
	if !loc.Remote() || loc.Path == "" {
		return ""
	}
	return "vscode://vscode-remote/ssh-remote+" + loc.Host + strings.TrimSuffix(loc.Path, "/")
}

// ShellLine is the command that puts a user in the task's directory themselves.
// It is what a surface says when it cannot open the code for them, so the answer
// still ends with something they can run.
func ShellLine(loc CodeLocation) string {
	if loc.Path == "" {
		return ""
	}
	if !loc.Remote() {
		return "cd " + loc.Path
	}
	return fmt.Sprintf("ssh %s -t %s", loc.Host,
		shellQuote("cd "+shellQuoteRemotePath(loc.Path)+" && exec ${SHELL:-/bin/sh} -l"))
}

// hostAddressCache memoizes HostAddress. `ssh -G` reads configuration and makes
// no connection, but the answer cannot change while ty runs and the callers are
// on a two-second refresh.
var (
	hostAddressMu    sync.Mutex
	hostAddressCache = map[string]string{}
)

// HostAddress is the network address to reach a placed host on from here: the
// hostname ssh itself would dial for that destination.
//
// It matters because an ssh destination is not necessarily a hostname. Fleets
// name hosts in ~/.ssh/config ("ol-agents"), and a URL built from that name is a
// URL no browser can open. `ssh -G` answers with the effective configuration —
// locally, without contacting anything. The fallback is the destination with any
// "user@" removed, which is right whenever the destination is already a name.
func HostAddress(ctx context.Context, target string) string {
	if target == "" || !IsRemotePlacement(target) {
		return ""
	}
	fallback := target
	if _, host, ok := strings.Cut(target, "@"); ok {
		fallback = host
	}
	hostAddressMu.Lock()
	cached, ok := hostAddressCache[target]
	hostAddressMu.Unlock()
	if ok {
		return cached
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	address := fallback
	if out, err := exec.CommandContext(ctx, sshBin(), "-G", target).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if name, value, found := strings.Cut(strings.TrimSpace(line), " "); found && name == "hostname" {
				if value = strings.TrimSpace(value); value != "" {
					address = value
				}
				break
			}
		}
	}
	hostAddressMu.Lock()
	hostAddressCache[target] = address
	hostAddressMu.Unlock()
	return address
}

// TaskServerURL is the URL of the dev server a task started on its own port,
// wherever the task is running. Empty when the task has no port.
func TaskServerURL(ctx context.Context, task *db.Task, localURL string) string {
	if task == nil || task.Port == 0 {
		return ""
	}
	if !IsRemotePlacement(task.PlacementTarget) {
		return fmt.Sprintf("%s:%d", localURL, task.Port)
	}
	address := HostAddress(ctx, task.PlacementTarget)
	if address == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", address, task.Port)
}

// remotePortProbeTimeout bounds the port check on a host. It is deliberately
// far below remoteProbeTimeout: this probe runs on the detail view's refresh
// tick, and a slow host must cost a missing "Server:" line, not a frozen view.
const remotePortProbeTimeout = 2 * time.Second

// PortListening reports whether anything is listening on the task's port, on
// the machine the task is running on.
func PortListening(ctx context.Context, task *db.Task) bool {
	if task == nil || task.Port == 0 {
		return false
	}
	port := fmt.Sprintf(":%d", task.Port)
	if !IsRemotePlacement(task.PlacementTarget) {
		ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		return exec.CommandContext(ctx, "lsof", "-i", port, "-sTCP:LISTEN").Run() == nil
	}
	// The same question, asked on the host: a server a placed task started
	// listens over there, and lsof here would answer about this machine's ports —
	// "no server" when there is one, or, when the coordinator happens to use the
	// same port, "yes" about something else entirely.
	//
	// ss is the fallback because a fleet host is often a minimal image with no
	// lsof on it, and "no lsof" must not read as "no server".
	script := fmt.Sprintf(
		"if command -v lsof >/dev/null 2>&1 && lsof -i %s -sTCP:LISTEN >/dev/null 2>&1; then exit 0; fi\n"+
			"ss -ltnH 2>/dev/null | grep -q %s",
		port, shellQuote(port+" "))
	ctx, cancel := context.WithTimeout(ctx, remotePortProbeTimeout)
	defer cancel()
	r := RemoteRunner{Host: task.PlacementTarget}
	return r.Command(ctx, "", "sh", "-c", script).Run() == nil
}

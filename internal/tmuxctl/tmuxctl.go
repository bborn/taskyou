// Package tmuxctl builds TaskYou's tmux commands and says which server each
// one goes to.
//
// There are two servers in play, and they are not always the same one:
//
//   - The agent server holds every task's executor and shell panes, the daemon
//     session, and the TUI sessions `ty` creates for itself. Fresh installs run
//     it as a private server (`tmux -L taskyou`), so TaskYou's sessions,
//     options and root key bindings never mix with the user's own tmux.
//   - The UI server is whichever server the running TUI sits in: $TMUX. It is
//     the agent server when `ty` started its own session, and the user's own
//     server when they ran `ty` inside their tmux.
//
// Nothing ever moves a pane between the two: the detail view shows a task's
// window through a nested client (see ViewAttachScript), which works across
// servers.
package tmuxctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// EnvSocket overrides the agent server: a socket name for `tmux -L`, or
// "default" for tmux's own default server.
const EnvSocket = "TASKYOU_TMUX_SOCKET"

// PrivateSocket is the server a fresh install runs its agents on.
const PrivateSocket = "taskyou"

// choiceFileName records, next to the database, which server this install
// uses. The choice has to be stable: an install whose agents already run on
// the default server must keep finding them there, or it would start a
// second agent for every task it can no longer see.
const choiceFileName = "tmux-socket"

var (
	choiceMu    sync.Mutex
	choiceCache = map[string]string{} // choice file path -> socket
)

// underTest keeps `go test` off this machine's recorded choice. Without an
// explicit TASKYOU_TMUX_SOCKET, a test gets tmux's default server and neither
// reads nor writes the real data directory. It has to: a test that stubbed tmux
// on PATH once found "no agents" there and recorded the private server for the
// live install, which would have hidden every running agent from ty.
var underTest = testing.Testing()

// Socket returns the `-L` name of the agent server, or "" for tmux's default
// server.
func Socket() string {
	if v, ok := os.LookupEnv(EnvSocket); ok {
		return normalize(v)
	}
	if underTest {
		return ""
	}
	path := filepath.Join(filepath.Dir(db.DefaultPath()), choiceFileName)
	choiceMu.Lock()
	defer choiceMu.Unlock()
	if s, ok := choiceCache[path]; ok {
		return s
	}
	s := normalize(readOrChoose(path))
	choiceCache[path] = s
	return s
}

// readOrChoose returns the recorded choice, or makes and records one: the
// default server if agents already run there, the private one otherwise.
func readOrChoose(path string) string {
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b))
	}
	choice := PrivateSocket
	if defaultServerHasAgents() {
		choice = "default"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(choice+"\n"), 0o644) // best effort; the cache still holds it
	return choice
}

// defaultServerHasAgents reports whether tmux's default server already holds a
// TaskYou daemon session: an install from before the private server existed.
func defaultServerHasAgents() bool {
	out, err := exec.Command("tmux", "-L", "default", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return false // no server, so nothing to keep finding
	}
	for _, name := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(name, "task-daemon-") {
			return true
		}
	}
	return false
}

func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "default" {
		return ""
	}
	return v
}

// Pane tags on the agent server. A pane carries what it is, so a task's panes
// are found by asking tmux, not by a pane ID remembered from earlier: tmux
// reuses IDs, and a stored one can end up naming another task's pane.
const (
	PaneTaskOption = "@ty_task"
	PaneRoleOption = "@ty_role"
	RoleAgent      = "agent"
	RoleShell      = "shell"
)

// TagPaneArgs are the commands that tag pane as taskID's agent or shell.
func TagPaneArgs(pane string, taskID int64, role string) [][]string {
	id := strconv.FormatInt(taskID, 10)
	return [][]string{
		{"set-option", "-p", "-t", pane, PaneTaskOption, id},
		{"set-option", "-p", "-t", pane, PaneRoleOption, role},
	}
}

// The size of an agent session nobody is looking at. A detached session
// otherwise starts at tmux's 80x24, and an agent that lays itself out for that
// has to reflow the moment someone attaches.
const (
	DefaultWidth  = 200
	DefaultHeight = 50
)

// DefaultSizeArgs are new-session's size flags for DefaultWidth x DefaultHeight.
func DefaultSizeArgs() []string {
	return []string{"-x", strconv.Itoa(DefaultWidth), "-y", strconv.Itoa(DefaultHeight)}
}

// DefaultSize is the same size as a tmux "default-size" value.
func DefaultSize() string {
	return fmt.Sprintf("%dx%d", DefaultWidth, DefaultHeight)
}

// AgentArgs prefixes args with the agent server's socket.
func AgentArgs(args ...string) []string {
	if s := Socket(); s != "" {
		return append([]string{"-L", s}, args...)
	}
	return args
}

// Agent builds a tmux command for the agent server.
func Agent(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "tmux", AgentArgs(args...)...)
}

// UI builds a tmux command for the server the running TUI sits in ($TMUX).
func UI(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "tmux", args...)
}

// SameServer reports whether the UI and agent servers are one server, which is
// true whenever the TUI runs in a session `ty` created for itself.
func SameServer() bool {
	ui := os.Getenv("TMUX")
	if ui == "" {
		return true
	}
	// $TMUX is "<socket path>,<pid>,<session>"; the socket file is named after
	// the -L name, "default" for the default server.
	socketPath := strings.SplitN(ui, ",", 2)[0]
	name := Socket()
	if name == "" {
		name = "default"
	}
	return filepath.Base(socketPath) == name
}

// AgentShell is the agent server's tmux invocation as a shell word list, for
// command strings that tmux or a shell will run: "tmux" or "tmux -L 'taskyou'".
// $TMUX is cleared so the command reaches the named server even from inside
// another tmux.
func AgentShell() string {
	if s := Socket(); s != "" {
		return "env -u TMUX -u TMUX_PANE tmux -L " + shellQuote(s)
	}
	return "env -u TMUX -u TMUX_PANE tmux"
}

// ViewAttachScript is the shell line a detail-view pane runs to show a task's
// window without moving any pane: a nested client attached to view, a session
// grouped with the daemon's that the caller has pointed at the task window.
//
// destroy-unattached is set only after the client connects, chained with
// tmux's ";", so it cannot fire first and take the session with it. When the
// pane is killed the client detaches and tmux disposes of the view session;
// the shared window, and the agent in it, stay where they are.
func ViewAttachScript(view string) string {
	return fmt.Sprintf("exec %s attach-session -t %s ';' set-option destroy-unattached on",
		AgentShell(), shellQuote(view))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

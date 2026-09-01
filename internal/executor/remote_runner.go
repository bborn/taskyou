package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RemoteRunner runs commands on another machine over ssh.
//
// It is the second implementation of the Runner seam, and the only thing in ty
// that knows a task can run somewhere other than here. It still knows nothing
// about hosts: the host name and the directory come from a task.placement
// handler's answer, verbatim. Choosing them is the extension's job; reaching
// them is this type's.
type RemoteRunner struct {
	// Host is the ssh destination, exactly as the placement handler named it —
	// an ssh_config alias, a user@host, whatever the operator's fleet uses.
	Host string
	// WorkDir is the task's directory on that host, used when a command does not
	// name one of its own. It is a REMOTE path: a leading "~" is deliberately
	// left unexpanded so the remote shell expands it, not this machine's.
	WorkDir string
	// SSHBin is the ssh binary; empty means "ssh".
	SSHBin string
	// ConnectTimeout bounds the TCP/handshake phase. Zero means
	// DefaultRemoteConnectTimeout.
	ConnectTimeout time.Duration
}

// DefaultRemoteConnectTimeout bounds how long ty waits to reach a placed host.
// A host that cannot be reached in this long is treated as unreachable, and an
// unreachable host FAILS the task rather than quietly running it here.
const DefaultRemoteConnectTimeout = 10 * time.Second

// Target names the host this runner reaches, which is what makes a placement
// visible in `ty show` and on the board.
func (r RemoteRunner) Target() string { return r.Host }

// Command builds an ssh invocation that runs name+args on the remote host.
//
// workDir is a REMOTE directory. It is applied with a `cd` inside the remote
// shell rather than with cmd.Dir, which would point at a path on this machine —
// the single most likely way to get this subtly, silently wrong.
func (r RemoteRunner) Command(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	if workDir == "" {
		workDir = r.WorkDir
	}
	return exec.CommandContext(ctx, r.ssh(), append(r.sshArgs(), loginShell(r.remoteScript(workDir, name, args...)))...)
}

// loginShell wraps a remote shell line so the host runs it in a LOGIN shell.
//
// ssh with a command runs a NON-login, NON-interactive shell, so ~/.profile
// never runs. Everything a fleet host installs per-user is invisible in that
// shell: on ol-agents `sh -c 'command -v claude'` finds nothing while
// `sh -lc 'command -v claude'` finds /home/olgm/.local/bin/claude. The first
// task ty ever placed remotely died on exactly this — the window ran
// "claude: not found", exited within a second, and the task parked as "needs
// review" with no trace of why.
//
// It is applied to EVERY remote command, not just the agent launch: git, tmux
// and the worktree provisioning script are all just as likely to be
// version-managed (mise, asdf, rbenv, nvm) or to live in ~/.local/bin.
func loginShell(script string) string {
	return "sh -lc " + shellQuote(script)
}

// remoteScript renders the shell line the remote host will run.
func (r RemoteRunner) remoteScript(workDir, name string, args ...string) string {
	var b strings.Builder
	if workDir != "" {
		// Quoted, but with a leading "~" left free: a remote workdir routinely
		// starts with one and the point of sending it is for the REMOTE shell to
		// expand it. Quoting the whole path would make ty look for a directory
		// literally named "~".
		b.WriteString("cd ")
		b.WriteString(shellQuoteRemotePath(workDir))
		b.WriteString(" && ")
	}
	b.WriteString(shellQuote(name))
	for _, a := range args {
		b.WriteString(" ")
		b.WriteString(shellQuote(a))
	}
	return b.String()
}

// sshBinary is the ssh client every remote command goes through. A package-level
// var only so tests can point it at a stub; nothing user-facing changes it.
var sshBinary = "ssh"

// ssh returns the ssh binary to use.
func (r RemoteRunner) ssh() string {
	if r.SSHBin != "" {
		return r.SSHBin
	}
	return sshBinary
}

// sshArgs are the options every remote command carries.
//
// BatchMode is the important one: without it a host whose key is missing or
// whose passphrase is not cached drops ssh into an interactive prompt inside the
// daemon, where nobody can answer it, and the task hangs instead of failing.
func (r RemoteRunner) sshArgs() []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(r.connectTimeout().Seconds())),
	}
	args = append(args, sshMultiplexArgs()...)
	return append(args, r.Host)
}

// sshMultiplexArgs reuse ONE ssh connection per host across every command ty
// sends there.
//
// Polling made this necessary. A placed task's window is checked on a timer for
// as long as it runs, and without multiplexing every check pays a fresh TCP
// connect, key exchange and authentication — a few hundred milliseconds and a
// new sshd process on the far side, per task, forever. With a shared master the
// checks after the first are a write down an existing socket.
//
// ControlPersist keeps the master alive a little longer than the poll interval,
// so consecutive polls of the same task reuse it rather than re-handshaking in
// the gap. If the socket is stale or unusable ssh silently opens a normal
// connection, so the worst case is today's behaviour.
func sshMultiplexArgs() []string {
	dir := sshControlDir()
	if dir == "" {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		// %C is a hash of (host, port, user, proxy) — one master per destination.
		"-o", "ControlPath=" + filepath.Join(dir, "%C"),
		"-o", "ControlPersist=60s",
	}
}

// sshControlDir returns the private directory the control sockets live in, or ""
// when one cannot be prepared (in which case ty simply does not multiplex).
//
// It is deliberately NOT under os.TempDir(): on macOS that is a long
// per-user path under /var/folders, and a unix socket path is capped at ~104
// bytes — %C alone is 64 hex characters, so the socket would silently fail to
// bind. The directory is per-uid and 0700 so another user cannot plant a socket
// ty would then connect through.
func sshControlDir() string {
	dir := filepath.Join("/tmp", fmt.Sprintf("ty-ssh-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	// A pre-existing directory owned by someone else, or left group/world
	// writable, is not somewhere to keep a control socket.
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return ""
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return ""
	}
	return dir
}

func (r RemoteRunner) connectTimeout() time.Duration {
	if r.ConnectTimeout > 0 {
		return r.ConnectTimeout
	}
	return DefaultRemoteConnectTimeout
}

// Preflight checks that the host answers, that it has tmux, and that the
// placement's workdir exists on it — and returns that workdir resolved to an
// absolute remote path.
//
// The resolution is not a nicety. Inventories are written in the remote user's
// own terms ("~/projects/engineering"), and tmux's -c flag does no tilde
// expansion, so a window created with the raw answer would fail to start in the
// right directory. Asking the remote shell once, up front, is the only place
// that expansion can honestly happen.
//
// A failure here is NOT a reason to fall back to local. Failing to DECIDE where
// to run falls back; failing to RUN where you were told does not — a silent
// fallback would put the load right back on the machine this feature exists to
// unload, on exactly the days nobody is watching.
func (r RemoteRunner) Preflight(ctx context.Context) (string, error) {
	if strings.TrimSpace(r.Host) == "" {
		return "", fmt.Errorf("placement named no host")
	}
	ctx, cancel := context.WithTimeout(ctx, r.connectTimeout()+5*time.Second)
	defer cancel()

	probe := "command -v tmux >/dev/null || { echo 'tmux is not installed there' >&2; exit 1; }; " +
		"command -v git >/dev/null || { echo 'git is not installed there' >&2; exit 1; }; "
	if r.WorkDir != "" {
		probe += fmt.Sprintf("cd %s || exit 1; ", shellQuoteRemotePath(r.WorkDir))
	}
	probe += "pwd"

	cmd := exec.CommandContext(ctx, r.ssh(), append(r.sshArgs(), loginShell(probe))...)
	cmd.WaitDelay = 2 * time.Second

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("cannot reach %s (workdir %q): %s", r.Host, r.WorkDir, detail)
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return "", fmt.Errorf("cannot reach %s: it answered with no working directory", r.Host)
	}
	return resolved, nil
}

// shellQuote single-quotes a value for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteRemotePath quotes a remote path while leaving a leading "~" (or
// leading "~/" free to be expanded by the remote shell.
//
// Placement handlers answer with the path as the fleet's inventory writes it,
// and that inventory is written in the remote user's own terms ("~/projects").
// Quoting the tilde would send ty looking for a directory literally named "~".
func shellQuoteRemotePath(path string) string {
	if path == "~" {
		return "~"
	}
	if strings.HasPrefix(path, "~/") {
		return "~/" + shellQuote(strings.TrimPrefix(path, "~/"))
	}
	return shellQuote(path)
}

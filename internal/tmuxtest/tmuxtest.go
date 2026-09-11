// Package tmuxtest keeps tests off the live tmux server.
//
// Pointing TMUX_TMPDIR at a scratch directory is not enough on its own: when
// $TMUX is set, tmux talks to that socket and ignores TMUX_TMPDIR. Every
// TaskYou agent runs inside tmux, so a test that only moved TMUX_TMPDIR
// created its sessions on the live server when an agent ran the suite, and a
// cleanup that ran "kill-server" took down every agent on the machine. Both
// variables have to go, which is what this package does.
package tmuxtest

import (
	"os"
	"os/exec"
	"testing"
)

// Isolate gives one test its own tmux server, torn down when the test ends.
// The test's own tmux calls and those of the code under test (plain
// exec.Command, inheriting the environment) all land on it.
func Isolate(t testing.TB) {
	t.Helper()
	dir, err := socketDir()
	if err != nil {
		t.Fatalf("tmux socket dir: %v", err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv(socketEnv, "default")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
}

// Main runs a package's tests against a throwaway tmux server, so no test in
// it can reach the live one even if it forgets to call Isolate. Use it as
//
//	func TestMain(m *testing.M) { os.Exit(tmuxtest.Main(m)) }
func Main(m *testing.M) int {
	dir, err := socketDir()
	if err == nil {
		_ = os.Setenv("TMUX_TMPDIR", dir)
	}
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	_ = os.Setenv(socketEnv, "default")
	code := m.Run()
	if err == nil {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	}
	return code
}

// socketEnv is tmuxctl.EnvSocket, pinned here to tmux's default socket: tests
// talk to tmux with plain `tmux` calls, and the code under test must reach the
// same (isolated) server rather than choosing, and recording on disk, a
// private one. Spelled out rather than imported to keep this package a leaf.
const socketEnv = "TASKYOU_TMUX_SOCKET"

// socketDir is short on purpose: a socket path is capped near 104 bytes, and
// os.TempDir() on darwin is already long.
func socketDir() (string, error) {
	return os.MkdirTemp("/tmp", "tytmux")
}

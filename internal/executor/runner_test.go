package executor

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// The Runner seam exists to make execution location changeable. These tests pin
// the one thing that must NOT change while it is being introduced: the local
// runner builds exactly the command the call sites used to build inline.

func TestLocalRunnerBuildsTheSameCommandAsExecCommand(t *testing.T) {
	got := LocalRunner{}.Command(context.Background(), "", "git", "worktree", "prune")
	want := exec.Command("git", "worktree", "prune")

	if got.Path != want.Path {
		t.Errorf("Path = %q, want %q", got.Path, want.Path)
	}
	if strings.Join(got.Args, " ") != strings.Join(want.Args, " ") {
		t.Errorf("Args = %v, want %v", got.Args, want.Args)
	}
	if got.Dir != "" {
		t.Errorf("Dir = %q, want empty so the command inherits the caller's cwd", got.Dir)
	}
}

func TestLocalRunnerRunsInWorkDir(t *testing.T) {
	cmd := LocalRunner{}.Command(context.Background(), "/tmp/some-worktree", "git", "status")
	if cmd.Dir != "/tmp/some-worktree" {
		t.Errorf("Dir = %q, want /tmp/some-worktree", cmd.Dir)
	}
}

// A cancelled context must kill the process, which is what exec.CommandContext
// gives us and what the call sites that used it relied on.
func TestLocalRunnerHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := LocalRunner{}.Command(ctx, "", "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep here: %v", err)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected the cancelled context to kill the process")
	}
}

func TestLocalRunnerTargetIsEmpty(t *testing.T) {
	if target := (LocalRunner{}).Target(); target != "" {
		t.Errorf("Target() = %q, want \"\" for local execution", target)
	}
}

// The default must be local, and must be the only thing core ever picks. A
// remote runner is added later, outside core; nothing here may vary it.
func TestDefaultRunnerIsLocal(t *testing.T) {
	if _, ok := DefaultRunner().(LocalRunner); !ok {
		t.Fatalf("DefaultRunner() = %T, want LocalRunner", DefaultRunner())
	}
	if target := DefaultRunner().Target(); target != "" {
		t.Errorf("DefaultRunner().Target() = %q, want \"\"", target)
	}
}

func TestGitCmdAndTmuxCmdGoThroughTheRunner(t *testing.T) {
	git := gitCmd(context.Background(), "/tmp/repo", "rev-parse", "HEAD")
	if strings.Join(git.Args, " ") != "git rev-parse HEAD" {
		t.Errorf("gitCmd args = %v", git.Args)
	}
	if git.Dir != "/tmp/repo" {
		t.Errorf("gitCmd Dir = %q, want /tmp/repo", git.Dir)
	}

	tm := tmuxCmd(context.Background(), "list-windows", "-a")
	if strings.Join(tm.Args, " ") != "tmux list-windows -a" {
		t.Errorf("tmuxCmd args = %v", tm.Args)
	}
	if tm.Dir != "" {
		t.Errorf("tmuxCmd Dir = %q, want empty: a tmux command addresses a server, not a directory", tm.Dir)
	}
}

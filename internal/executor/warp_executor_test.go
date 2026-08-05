package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func newWarpTestExecutor(t *testing.T) *Executor {
	t.Helper()

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	return New(database, &config.Config{})
}

func warpTask() *db.Task {
	return &db.Task{
		ID:           42,
		Port:         9000,
		WorktreePath: "/tmp/projects/myapp/.task-worktrees/42-fix-bug",
	}
}

// TestWarpLaunchScriptExecsWarp guards the property the prompt feeder depends on:
// the Warp binary — not the wrapping shell — must become the pane's foreground
// process, or `pane_current_command` never matches and the paste is delayed
// until the readiness loop times out.
func TestWarpLaunchScriptExecsWarp(t *testing.T) {
	script := warpLaunchScript(warpTask(), "/tmp/work", "", "")

	if !strings.Contains(script, "exec env ") {
		t.Errorf("launch script should exec the warp binary, got:\n%s", script)
	}
	if !strings.HasSuffix(strings.TrimSpace(script), "warp") {
		t.Errorf("launch script should end by running warp, got:\n%s", script)
	}
	for _, want := range []string{"WORKTREE_TASK_ID=42", "WORKTREE_PORT=9000", "WORKTREE_PATH="} {
		if !strings.Contains(script, want) {
			t.Errorf("launch script should contain %q, got:\n%s", want, script)
		}
	}
}

func TestWarpLaunchScriptFlags(t *testing.T) {
	tests := []struct {
		name      string
		dangerous bool
		want      []string
		notWant   []string
	}{
		{
			name: "default has no flags",
			// --resume is never passed: ty has no Warp conversation token, and
			// task.ClaudeSessionID may belong to a different agent entirely.
			notWant: []string{"--auto-approve", "--resume"},
		},
		{
			name:      "dangerous mode auto-approves",
			dangerous: true,
			want:      []string{"--auto-approve"},
			notWant:   []string{"--resume"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := warpTask()
			task.DangerousMode = tt.dangerous

			script := warpLaunchScript(task, "/tmp/work", "", "")

			for _, want := range tt.want {
				if !strings.Contains(script, want) {
					t.Errorf("script should contain %q, got:\n%s", want, script)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(script, notWant) {
					t.Errorf("script should not contain %q, got:\n%s", notWant, script)
				}
			}
		})
	}
}

// TestWarpLaunchScriptPromptFeeder checks the paste path: Warp takes no prompt
// argument, so the prompt only reaches it via a bracketed tmux paste.
func TestWarpLaunchScriptPromptFeeder(t *testing.T) {
	promptFile := "/tmp/task-prompt-xyz.txt"
	script := warpLaunchScript(warpTask(), "/tmp/work", promptFile, "")

	for _, want := range []string{
		"load-buffer",
		"paste-buffer -d -p", // -p keeps a multi-line prompt as a single paste
		"send-keys",          // submit
		`"$TMUX_PANE"`,       // target this pane, not whatever is active
		"rm -f " + `"` + promptFile + `"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("feeder should contain %q, got:\n%s", want, script)
		}
	}

	// No prompt file means no feeder at all.
	bare := warpLaunchScript(warpTask(), "/tmp/work", "", "")
	if strings.Contains(bare, "paste-buffer") {
		t.Errorf("bare session should not paste anything, got:\n%s", bare)
	}
}

// TestWarpLaunchScriptIsValidShell catches quoting mistakes in the generated
// script before they turn into a window that dies on startup.
func TestWarpLaunchScriptIsValidShell(t *testing.T) {
	task := warpTask()
	task.DangerousMode = true

	script := warpLaunchScript(task, "/tmp/work dir", "/tmp/task prompt.txt", "CLAUDE_CONFIG_DIR=/tmp/cfg ")

	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("generated script is not valid sh: %v\n%s\nscript:\n%s", err, out, script)
	}
}

func TestBuildWarpPrompt(t *testing.T) {
	prompt := buildWarpPrompt("/tmp/wt/42-fix", "Do the thing", "", false)

	if !strings.Contains(prompt, "/tmp/wt/42-fix") {
		t.Errorf("prompt should name the worktree, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Do the thing") {
		t.Errorf("prompt should contain the task prompt, got:\n%s", prompt)
	}
	// Warp has no taskyou MCP server, so it must be pointed at the ty CLI.
	if !strings.Contains(prompt, "ty complete") {
		t.Errorf("prompt should point Warp at the ty CLI for completion, got:\n%s", prompt)
	}

	withFeedback := buildWarpPrompt("/tmp/wt/42-fix", "Do the thing", "Also fix the tests", true)
	if !strings.Contains(withFeedback, "Also fix the tests") {
		t.Errorf("resume prompt should include feedback, got:\n%s", withFeedback)
	}
	if strings.Contains(prompt, "Also fix the tests") {
		t.Error("fresh prompt should not include feedback")
	}
}

func TestWarpBuildCommandWritesPromptFile(t *testing.T) {
	e := newWarpTestExecutor(t)
	warp := e.executorFactory.Get(db.ExecutorWarp)
	if warp == nil {
		t.Fatal("warp executor not registered")
	}

	script := warp.BuildCommand(warpTask(), "", "Fix the flaky test")

	path := warpPromptPathFromScript(t, script)
	defer os.Remove(path)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("prompt file not written: %v", err)
	}
	if !strings.Contains(string(content), "Fix the flaky test") {
		t.Errorf("prompt file should hold the prompt, got %q", content)
	}
}

// warpPromptPathFromScript pulls the temp prompt path back out of a launch script.
func warpPromptPathFromScript(t *testing.T, script string) string {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "rm -f ") {
			continue
		}
		return strings.Trim(strings.TrimPrefix(line, "rm -f "), `"`)
	}
	t.Fatalf("no prompt file in script:\n%s", script)
	return ""
}

// TestWarpPromptDeliveryInTmux runs the real launch script against a stub `warp`
// that echoes whatever it is given, and asserts the prompt actually lands in the
// pane. This is the only check that exercises the readiness wait, the bracketed
// paste and the submit together.
func TestWarpPromptDeliveryInTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tmux integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	dir := t.TempDir()

	// A stub named `warp` that echoes whatever it is fed. It has to be a compiled
	// binary rather than a shell script: the readiness wait matches on
	// pane_current_command, which reports the interpreter for a script.
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available to build the warp stub")
	}
	stubSrc := filepath.Join(dir, "warp_stub.go")
	if err := os.WriteFile(stubSrc, []byte("package main\n\nimport (\n\t\"io\"\n\t\"os\"\n)\n\nfunc main() { io.Copy(os.Stdout, os.Stdin) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(binDir, "warp"), stubSrc)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("could not build warp stub: %v (%s)", err, out)
	}

	promptFile := filepath.Join(dir, "prompt.txt")
	const promptText = "## Working Directory\n\nline two of the prompt\n"
	if err := os.WriteFile(promptFile, []byte(promptText), 0o644); err != nil {
		t.Fatal(err)
	}

	script := "PATH=" + binDir + ":$PATH\n" + warpLaunchScript(warpTask(), dir, promptFile, "")

	session := fmt.Sprintf("ty-warp-test-%d", os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "120", "-y", "40", "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session failed: %v (%s)", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", session).Run()

	// The stub echoes the paste back into the pane. Allow well under the
	// readiness loop's own timeout, so a broken pane_current_command match
	// (which would fall through to pasting blind) fails here instead of passing
	// slowly.
	deadline := time.Now().Add(12 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		pane, _ := exec.Command("tmux", "capture-pane", "-t", session, "-p").Output()
		if strings.Contains(string(pane), "line two of the prompt") {
			got = string(pane)
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if got == "" {
		pane, _ := exec.Command("tmux", "capture-pane", "-t", session, "-p").Output()
		t.Fatalf("prompt never reached the pane\npane:\n%s", pane)
	}
	if !strings.Contains(got, "## Working Directory") {
		t.Errorf("first line of the prompt was lost, pane:\n%s", got)
	}

	// The feeder is responsible for cleaning up after itself.
	cleanupDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(cleanupDeadline) {
		if _, err := os.Stat(promptFile); os.IsNotExist(err) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("prompt file %s was not removed after the paste", promptFile)
}

func TestWarpAuthPromptDetection(t *testing.T) {
	// The device-login screen the Warp CLI shows when it has no credentials.
	pane := "Welcome to Warp\n  ● Waiting for login...\n  Log in with Warp\n  Copy URL (c)"

	reason, stuck := DetectAuthPrompt(pane)
	if !stuck {
		t.Fatal("logged-out Warp screen should be detected as needing auth")
	}
	if !strings.Contains(strings.ToLower(reason), "warp") {
		t.Errorf("reason should name Warp, got %q", reason)
	}
}

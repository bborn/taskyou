package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Live checks of the two halves of "a placed host has my tools", against a
// real fleet host. Opt-in:
//
//	TY_REMOTE_TEST_HOST=<host> go test ./internal/executor -run OverSSH -v
//
// They prove what the local tests cannot: that the scripts survive ssh and the
// host's login shell, that this Mac's rsync talks to the host's, and that a
// relayed call makes the whole trip over a real connection and fails fast when
// that connection is cut.

func remoteTestHost(t *testing.T) string {
	t.Helper()
	host := os.Getenv("TY_REMOTE_TEST_HOST")
	if host == "" {
		t.Skip("set TY_REMOTE_TEST_HOST to run against a real host")
	}
	return host
}

// remoteRun runs a script on host and returns its stdout.
func remoteRun(t *testing.T, host, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := RemoteRunner{Host: host}.Command(ctx, "", "sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("on %s: %s: %v", host, script, err)
	}
	return strings.TrimSpace(string(out))
}

// sandboxTransport is the ssh transport with HOME (and so the Claude config dir
// and ty's record) moved to a scratch directory, so a test sync never touches
// the host's real skills.
type sandboxTransport struct {
	sshSyncTransport
	home string
}

func (s sandboxTransport) Run(ctx context.Context, script string) (string, error) {
	return s.sshSyncTransport.Run(ctx, "export HOME="+shellQuote(s.home)+"; unset CLAUDE_CONFIG_DIR; "+script)
}

func TestHostSyncOverSSH(t *testing.T) {
	host := remoteTestHost(t)
	home := remoteRun(t, host, "mktemp -d /tmp/ty-sync-test-XXXXXX")
	t.Cleanup(func() { remoteRun(t, host, "rm -rf "+shellQuote(home)) })

	src := syncSource(t)
	// No plugins: installing real ones is not this test's business.
	writeTree(t, src, "settings.json", `{}`, 0o644)
	s := testSyncer(src, sandboxTransport{sshSyncTransport{RemoteRunner{Host: host}}, home})

	res, err := s.Sync(context.Background(), host, false)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	t.Logf("first sync: %s", res.Summary(host))

	skills := home + "/.claude/skills"
	got := remoteRun(t, host, fmt.Sprintf(`cd %s && find . -print | sort; echo; cat linked/shared.md; echo; [ -L linked ] && echo LINK || echo DIR`, shellQuote(skills)))
	for _, want := range []string{"./plain/SKILL.md", "./linked/shared.md", "./builder/bin/helper.sh", "shared", "DIR"} {
		if !strings.Contains(got, want) {
			t.Errorf("host is missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"./builder/bin/tool", "node_modules", ".git", "dangling", "./synced"} {
		if strings.Contains(got, absent) {
			t.Errorf("host got %q:\n%s", absent, got)
		}
	}
	// The changed skill's setup ran there, detached.
	deadline := time.Now().Add(20 * time.Second)
	for remoteRun(t, host, "[ -f "+shellQuote(home+"/setup-ran")+" ] && echo yes || echo no") != "yes" {
		if time.Now().After(deadline) {
			t.Fatal("builder's setup never ran on the host")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The normal case: nothing changed, one command, well under a second.
	s.cachedAt = time.Time{}
	start := time.Now()
	res, err = s.Sync(context.Background(), host, false)
	took := time.Since(start)
	if err != nil || !res.UpToDate {
		t.Fatalf("second sync = %+v, %v", res, err)
	}
	t.Logf("an up-to-date check took %s", took)
	if took > 2*time.Second {
		t.Errorf("an up-to-date check took %s", took)
	}
}

// remoteStub runs the staged stub on host over ssh, as Claude there would.
type remoteStub struct {
	in    io.WriteCloser
	lines chan string
	cmd   *exec.Cmd
}

func startRemoteStub(t *testing.T, host, workDir, coordinator, run string, taskID int64) *remoteStub {
	t.Helper()
	script := fmt.Sprintf("WORKTREE_TASK_ID=%d WORKTREE_RUN_ID=%s WORKTREE_COORDINATOR_ID=%s exec %s taskyou",
		taskID, shellQuote(run), shellQuote(coordinator), shellQuote(workDir+"/"+mcpProxyScriptPath))
	cmd := RemoteRunner{Host: host}.Command(context.Background(), "", "sh", "-c", script)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &remoteStub{in: in, lines: make(chan string, 16), cmd: cmd}
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			s.lines <- scanner.Text()
		}
		close(s.lines)
	}()
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return s
}

func (s *remoteStub) ask(t *testing.T, req string, within time.Duration) (map[string]json.RawMessage, time.Duration) {
	t.Helper()
	start := time.Now()
	if _, err := io.WriteString(s.in, req+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line, ok := <-s.lines:
		if !ok {
			t.Fatalf("the stub exited without answering %s", req)
		}
		var reply map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("non-JSON reply %q", line)
		}
		return reply, time.Since(start)
	case <-time.After(within):
		t.Fatalf("no reply to %s within %s", req, within)
	}
	return nil, 0
}

func TestMCPRelayOverSSH(t *testing.T) {
	host := remoteTestHost(t)
	e, database := reconcileTestExecutor(t)
	t.Cleanup(e.closeMCPRelay)
	task, run := relayTestTaskOn(t, database, host)
	coordinator, _ := database.CoordinatorID()
	t.Cleanup(func() { remoteRun(t, host, "rm -rf \"$HOME/.ty-events/\""+shellQuote(coordinator)) })

	workDir := remoteRun(t, host, "mktemp -d /tmp/ty-relay-test-XXXXXX")
	t.Cleanup(func() { remoteRun(t, host, "rm -rf "+shellQuote(workDir)) })
	ctx := WithRunner(context.Background(), RemoteRunner{Host: host})
	if _, err := e.installMCPProxy(ctx, task, workDir); err != nil {
		t.Fatalf("installMCPProxy over ssh: %v", err)
	}

	// Offline first: nothing has dialled the host for this coordinator yet.
	stub := startRemoteStub(t, host, workDir, coordinator, run, task.ID)
	list, _ := stub.ask(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 20*time.Second)
	if !strings.Contains(string(list["result"]), "taskyou_complete") {
		t.Fatalf("tools/list with no channel = %v", list)
	}
	reply, took := stub.ask(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{}}}`, 20*time.Second)
	if !strings.Contains(string(reply["error"]), "offline") || took > 3*time.Second {
		t.Errorf("a call with no channel = %v after %s, want a fast offline error", reply, took)
	}

	// Channel up: the call makes the trip and is answered from this machine.
	c := e.hostChannelFor(host)
	deadline := time.Now().Add(30 * time.Second)
	for {
		stamp := remoteRun(t, host, "cat \"$HOME/.ty-events/\""+shellQuote(coordinator)+"/mcp/alive 2>/dev/null; true")
		if stamp != "" && stamp != "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host agent never heard a ping")
		}
		time.Sleep(300 * time.Millisecond)
	}
	reply, took = stub.ask(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{"task_id":%d}}}`, task.ID), 30*time.Second)
	if !strings.Contains(string(reply["result"]), "relayed task") {
		t.Fatalf("relayed show_task over ssh = %v", reply)
	}
	t.Logf("relayed call over ssh answered in %s", took)

	// Cut the channel: the next call fails at once, and the server stays up.
	c.Close()
	reply, took = stub.ask(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{}}}`, 30*time.Second)
	t.Logf("call after the cut: %s in %s", reply["error"], took)
	if !strings.Contains(string(reply["error"]), "unavailable") || took > 12*time.Second {
		t.Errorf("a call after the cut = %v after %s, want a fast unavailable error", reply, took)
	}
	list, _ = stub.ask(t, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, 20*time.Second)
	if !strings.Contains(string(list["result"]), "taskyou_complete") {
		t.Errorf("tools/list after the cut = %v", list)
	}
}

// A real Claude on the host, given the staged --mcp-config, must accept the stub
// as a server, call a taskyou tool through it, and — with the channel cut —
// get a readable error and carry on rather than hang. Costs two short Claude
// turns on the host's own login, so it is opt-in twice over:
//
//	TY_REMOTE_TEST_HOST=<host> TY_REMOTE_TEST_CLAUDE=1 go test ./internal/executor -run TestPlacedClaudeUsesRelayedToolsOverSSH -v
func TestPlacedClaudeUsesRelayedToolsOverSSH(t *testing.T) {
	host := remoteTestHost(t)
	if os.Getenv("TY_REMOTE_TEST_CLAUDE") == "" {
		t.Skip("set TY_REMOTE_TEST_CLAUDE=1 to spend two Claude turns on the host")
	}
	e, database := reconcileTestExecutor(t)
	t.Cleanup(e.closeMCPRelay)
	task, run := relayTestTaskOn(t, database, host)
	coordinator, _ := database.CoordinatorID()
	t.Cleanup(func() { remoteRun(t, host, "rm -rf \"$HOME/.ty-events/\""+shellQuote(coordinator)) })
	workDir := remoteRun(t, host, "mktemp -d /tmp/ty-relay-claude-XXXXXX")
	t.Cleanup(func() { remoteRun(t, host, "rm -rf "+shellQuote(workDir)) })
	install, err := e.installMCPProxy(WithRunner(context.Background(), RemoteRunner{Host: host}), task, workDir)
	if err != nil {
		t.Fatal(err)
	}

	askClaude := func(prompt string) (string, time.Duration) {
		t.Helper()
		script := fmt.Sprintf("cd %s && WORKTREE_TASK_ID=%d WORKTREE_RUN_ID=%s WORKTREE_COORDINATOR_ID=%s claude -p --model haiku --mcp-config %s --allowedTools mcp__taskyou__taskyou_show_task -- %s",
			shellQuote(workDir), task.ID, shellQuote(run), shellQuote(coordinator), shellQuote(install.ConfigPath), shellQuote(prompt))
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		start := time.Now()
		out, err := RemoteRunner{Host: host}.Command(ctx, "", "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("claude on %s: %v\n%s", host, err, out)
		}
		return string(out), time.Since(start)
	}

	c := e.hostChannelFor(host)
	deadline := time.Now().Add(30 * time.Second)
	for {
		stamp := remoteRun(t, host, "cat \"$HOME/.ty-events/\""+shellQuote(coordinator)+"/mcp/alive 2>/dev/null; true")
		if stamp != "" && stamp != "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host agent never heard a ping")
		}
		time.Sleep(300 * time.Millisecond)
	}

	out, took := askClaude(fmt.Sprintf("Call the taskyou_show_task tool with task_id %d. Reply with only the task's title, nothing else.", task.ID))
	t.Logf("with the channel up (%s): %s", took, strings.TrimSpace(out))
	if !strings.Contains(out, "relayed task") {
		t.Errorf("Claude on %s did not get the task through the relay:\n%s", host, out)
	}

	c.Close()
	out, took = askClaude(fmt.Sprintf("Call the taskyou_show_task tool with task_id %d. If the call fails, do not retry: reply with the word CONTINUED followed by the error text.", task.ID))
	t.Logf("with the channel cut (%s): %s", took, strings.TrimSpace(out))
	if !strings.Contains(out, "CONTINUED") {
		t.Errorf("Claude on %s did not carry on past the failed call:\n%s", host, out)
	}
}

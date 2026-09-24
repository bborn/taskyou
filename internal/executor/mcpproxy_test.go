package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestParseRelayName(t *testing.T) {
	ref, ok := parseRelayName("42.0a1b2c.taskyou.1234.7")
	if !ok || ref.TaskID != 42 || ref.RunID != "0a1b2c" || ref.Server != "taskyou" {
		t.Fatalf("parseRelayName = %+v, %v", ref, ok)
	}
	if ref, ok := parseRelayName("42.0a1b2c.claude_in-chrome.1.2"); !ok || ref.Server != "claude_in-chrome" {
		t.Errorf("a server name with _ and - was refused: %+v %v", ref, ok)
	}
	for _, bad := range []string{
		"", "42.0a1b2c.taskyou.1234", "0.0a1b2c.taskyou.1.1", "x.0a1b2c.taskyou.1.1",
		"42.NOTHEX.taskyou.1.1", "42.0a1b2c.task/you.1.1", "42.0a1b2c.taskyou.a.1",
		"../42.0a1b2c.taskyou.1.1", "42.0a1b2c.taskyou.1.1 ; rm -rf ~",
	} {
		if _, ok := parseRelayName(bad); ok {
			t.Errorf("parseRelayName(%q) accepted a malformed name", bad)
		}
	}
}

// relayTestTask is a task placed on far-host, with the run a stub launched for
// it would carry.
func relayTestTask(t *testing.T) (*Executor, *db.DB, *db.Task, string) {
	t.Helper()
	e, database := reconcileTestExecutor(t)
	t.Cleanup(e.closeMCPRelay)
	task, run := relayTestTaskOn(t, database, "far-host")
	return e, database, task, run
}

func relayTestTaskOn(t *testing.T, database *db.DB, host string) (*db.Task, string) {
	t.Helper()
	task := &db.Task{Title: "relayed task", Type: "task", Project: "test", Status: db.StatusProcessing}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	run, err := database.BeginRemoteRun(task.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	return task, run
}

// A relayed call acts as the task it names only if that is the task's current
// run on the host it came from — the same scope `ty mcp-server --task-id N`
// has locally, checked where the task store is.
func TestRelayAnswersOnlyTheCurrentRunOnItsHost(t *testing.T) {
	e, _, task, run := relayTestTask(t)
	relay := e.mcpRelayFor()
	call := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{"task_id":%d}}}`, task.ID)

	out := relay.handle("far-host", fmt.Sprintf("%d.%s.taskyou.1.1", task.ID, run), []byte(call))
	if !strings.Contains(string(out), "relayed task") || !strings.Contains(string(out), `"id":3`) {
		t.Fatalf("current run got %s, want the task answered to id 3", out)
	}

	for label, name := range map[string]string{
		"another host": fmt.Sprintf("%d.%s.taskyou.1.1", task.ID, run),
		"an old run":   fmt.Sprintf("%d.%s.taskyou.1.1", task.ID, "0123abcd"),
	} {
		host := "far-host"
		if label == "another host" {
			host = "other-host"
		}
		out := relay.handle(host, name, []byte(call))
		if !strings.Contains(string(out), `"error"`) || !strings.Contains(string(out), "not the current run") {
			t.Errorf("%s got %s, want a refusal", label, out)
		}
	}
	if out := relay.handle("far-host", "garbage", []byte(call)); !strings.Contains(string(out), "malformed") {
		t.Errorf("a malformed name got %s", out)
	}
}

// stagedStub installs the proxy into a worktree on THIS machine (no runner in
// the context means local) and returns how to start the stub as the host would.
func stagedStub(t *testing.T, e *Executor, task *db.Task) (workDir string, install mcpProxyInstall) {
	t.Helper()
	workDir = t.TempDir()
	install, err := e.installMCPProxy(context.Background(), task, workDir)
	if err != nil {
		t.Fatalf("installMCPProxy: %v", err)
	}
	return workDir, install
}

// mcpClient drives a stub the way Claude does: newline-delimited JSON-RPC.
type mcpClient struct {
	t     *testing.T
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan string
}

func startStub(t *testing.T, stub, server, home, coordinator string, taskID int64, run string) *mcpClient {
	t.Helper()
	cmd := exec.Command(stub, server)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"WORKTREE_TASK_ID="+strconv.FormatInt(taskID, 10),
		"WORKTREE_RUN_ID="+run,
		"WORKTREE_COORDINATOR_ID="+coordinator)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &mcpClient{t: t, cmd: cmd, in: in, lines: make(chan string, 16)}
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			c.lines <- scanner.Text()
		}
		close(c.lines)
	}()
	t.Cleanup(func() {
		_ = in.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return c
}

// ask sends one request and returns the reply and how long it took.
func (c *mcpClient) ask(req string, within time.Duration) (map[string]json.RawMessage, time.Duration) {
	c.t.Helper()
	start := time.Now()
	if _, err := io.WriteString(c.in, req+"\n"); err != nil {
		c.t.Fatalf("write to stub: %v", err)
	}
	select {
	case line, ok := <-c.lines:
		if !ok {
			c.t.Fatalf("the stub exited without answering %s", req)
		}
		var reply map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			c.t.Fatalf("stub replied with non-JSON %q: %v", line, err)
		}
		return reply, time.Since(start)
	case <-time.After(within):
		c.t.Fatalf("no reply to %s within %s", req, within)
	}
	return nil, 0
}

// The server must survive this machine being asleep: Claude asks for the tool
// list at startup and drops a server that does not answer. And a tool call made
// while it is away must fail at once, as a plain MCP error the agent can read,
// rather than hang the session.
func TestStubAnswersToolListOfflineAndFailsCallsFast(t *testing.T) {
	e, database, task, run := relayTestTask(t)
	workDir, install := stagedStub(t, e, task)
	coordinator, _ := database.CoordinatorID()
	home := t.TempDir()

	// The config Claude is given names the stub by absolute path, on the host.
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	data, err := os.ReadFile(install.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	ty := cfg.MCPServers["taskyou"]
	if ty.Command != filepath.Join(workDir, mcpProxyScriptPath) || len(ty.Args) != 1 || ty.Args[0] != "taskyou" {
		t.Fatalf("config = %s", data)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".ty", ".gitignore")); err != nil {
		t.Errorf("the staged files are not kept out of git: %v", err)
	}

	for _, label := range []string{"never connected", "went quiet"} {
		t.Run(label, func(t *testing.T) {
			if label == "went quiet" {
				// A stamp a minute old: the host agent has not heard from ty since.
				spool := filepath.Join(home, ".ty-events", coordinator, "mcp")
				if err := os.MkdirAll(spool, 0o700); err != nil {
					t.Fatal(err)
				}
				stamp := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
				if err := os.WriteFile(filepath.Join(spool, "alive"), []byte(stamp+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c := startStub(t, ty.Command, "taskyou", home, coordinator, task.ID, run)

			init, _ := c.ask(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, 5*time.Second)
			if string(init["id"]) != "0" || !strings.Contains(string(init["result"]), "taskyou") {
				t.Fatalf("initialize = %v", init)
			}
			// A notification gets no reply: the next answer is for the next request.
			_, _ = io.WriteString(c.in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")

			list, _ := c.ask(`{"method":"tools/list","params":{},"jsonrpc":"2.0","id":1}`, 5*time.Second)
			if string(list["id"]) != "1" || !strings.Contains(string(list["result"]), "taskyou_complete") {
				t.Fatalf("tools/list = %v", list)
			}

			call, took := c.ask(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"taskyou_complete","arguments":{"summary":"the \"id\": 99 and \"method\": \"x\" in here are not the request's"}}}`, 5*time.Second)
			if string(call["id"]) != "2" {
				t.Errorf("the error answered id %s, want 2", call["id"])
			}
			msg := string(call["error"])
			if !strings.Contains(msg, "taskyou unavailable") || !strings.Contains(msg, "offline") || !strings.Contains(msg, ".ty/signal done") {
				t.Errorf("tools/call while offline = %v, want the offline error naming .ty/signal", call)
			}
			if took > 2*time.Second {
				t.Errorf("an offline call took %s; it must fail at once", took)
			}
			if left, _ := filepath.Glob(filepath.Join(home, ".ty-events", coordinator, "mcp", "*.req")); len(left) > 0 {
				t.Errorf("a call that was never made left its request queued: %v", left)
			}

			ping, _ := c.ask(`{"jsonrpc":"2.0","id":"p","method":"ping"}`, 5*time.Second)
			if string(ping["result"]) != "{}" {
				t.Errorf("ping = %v", ping)
			}
		})
	}
}

// localHostChannel runs the real host agent script on this machine, as a
// channel's dial would over ssh, with HOME pointed at home.
func localHostChannel(t *testing.T, e *Executor, home, coordinator string) *hostChannel {
	t.Helper()
	bin := t.TempDir()
	// A tmux with no windows: the agent's only work is the relay.
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &hostChannel{host: "far-host", stop: cancel, done: make(chan struct{}), coordinator: coordinator, relay: e.mcpRelayFor().handle}
	go c.run(ctx, func(ctx context.Context) (*exec.Cmd, io.Reader, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", hostAgentProgram(coordinator))
		cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+home, "PATH="+bin+":"+os.Getenv("PATH"))
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		c.attach(in)
		return cmd, out, nil
	})
	t.Cleanup(c.Close)

	// Up once the agent has heard ty's first ping.
	alive := filepath.Join(home, ".ty-events", coordinator, "mcp", "alive")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(alive); err == nil {
			if at, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && time.Since(time.Unix(at, 0)) < 5*time.Second {
				return c
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the host agent never recorded a ping from ty")
	return nil
}

// End to end through real shells: Claude's request goes into the stub, is
// spooled, carried up by the host agent, run against this machine's task store,
// and answered back down the same connection. Then the connection is cut, and
// the next call fails at once instead of waiting.
func TestRelayRoundTripThroughTheHostAgent(t *testing.T) {
	e, database, task, run := relayTestTask(t)
	_, install := stagedStub(t, e, task)
	stub := strings.TrimSuffix(install.ConfigPath, "/mcp/config.json") + "/mcp-proxy"
	coordinator, _ := database.CoordinatorID()
	home := t.TempDir()

	c := localHostChannel(t, e, home, coordinator)
	client := startStub(t, stub, "taskyou", home, coordinator, task.ID, run)

	reply, took := client.ask(fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{"task_id":%d}}}`, task.ID), 15*time.Second)
	if string(reply["id"]) != "5" || !strings.Contains(string(reply["result"]), "relayed task") {
		t.Fatalf("relayed show_task = %v", reply)
	}
	t.Logf("relayed call answered in %s", took)

	// A write goes through too, and lands in this machine's store.
	reply, _ = client.ask(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"taskyou_set_project_context","arguments":{"context":"written from the host"}}}`, 15*time.Second)
	if _, failed := reply["error"]; failed {
		t.Fatalf("relayed set_project_context = %v", reply)
	}
	if got, _ := database.GetProjectContext("test"); got != "written from the host" {
		t.Errorf("project context after a relayed set = %q, want the host's", got)
	}

	// Cut the channel: the agent's reader sees ty hang up and says so.
	c.Close()
	reply, took = client.ask(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"taskyou_show_task","arguments":{}}}`, 5*time.Second)
	if !strings.Contains(string(reply["error"]), "offline") {
		t.Errorf("a call after the channel was cut = %v, want the offline error", reply)
	}
	if took > 2*time.Second {
		t.Errorf("a call after the channel was cut took %s; it must fail at once", took)
	}
	list, _ := client.ask(`{"jsonrpc":"2.0","id":8,"method":"tools/list"}`, 5*time.Second)
	if !strings.Contains(string(list["result"]), "taskyou_complete") {
		t.Errorf("tools/list after the cut = %v", list)
	}
}

// TestHelperFakeMCPServer is not a test: it is a stdio MCP server the relay
// tests start as a child process, standing in for claude-in-chrome.
func TestHelperFakeMCPServer(t *testing.T) {
	if os.Getenv("TY_FAKE_MCP_SERVER") != "1" {
		t.Skip("helper process for the relay tests")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":true},"logging":{}},"serverInfo":{"name":"fake","version":"1"},"instructions":"drive the fake"}`
		case "tools/list":
			result = `{"tools":[{"name":"poke","description":"pokes","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			result = fmt.Sprintf(`{"content":[{"type":"text","text":"poked by %d"}]}`, os.Getpid())
		default:
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"no"}}`+"\n", req.ID)
			continue
		}
		// A notification first, which the relay must not mistake for a reply.
		fmt.Println(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"hi"}}`)
		fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", req.ID, result)
	}
	os.Exit(0)
}

// A Mac-only server runs HERE, one per placed run, started on first use and
// reused after; its tool list is read here for staging, with only the tools
// capability passed on.
func TestRelayRunsAMacOnlyServerHere(t *testing.T) {
	e, _, task, run := relayTestTask(t)
	relay := e.mcpRelayFor()
	def := mcpServerDef{Name: "fake", Command: os.Args[0], Args: []string{"-test.run=^TestHelperFakeMCPServer$"}, Env: map[string]string{"TY_FAKE_MCP_SERVER": "1"}}
	relay.defs = func() []mcpServerDef { return []mcpServerDef{def} }

	l, err := relay.listing(context.Background(), def)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if !strings.Contains(string(l.Tools), `"poke"`) {
		t.Errorf("tools = %s", l.Tools)
	}
	if strings.Contains(string(l.Init), "listChanged") || !strings.Contains(string(l.Init), "drive the fake") {
		t.Errorf("staged handshake = %s, want tools only and the server's instructions kept", l.Init)
	}

	name := fmt.Sprintf("%d.%s.fake.1.1", task.ID, run)
	first := relay.handle("far-host", name, []byte(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"poke"}}`))
	second := relay.handle("far-host", name, []byte(`{"jsonrpc":"2.0","id":"twelve","method":"tools/call","params":{"name":"poke"}}`))
	if !strings.Contains(string(first), `"id":11`) || !strings.Contains(string(second), `"id":"twelve"`) {
		t.Fatalf("replies = %s / %s", first, second)
	}
	pid := func(b []byte) string {
		_, after, _ := strings.Cut(string(b), "poked by ")
		return strings.TrimRight(after, `"}]`)
	}
	if pid(first) == "" || pid(first) != pid(second) {
		t.Errorf("calls went to different processes (%q, %q); a run keeps its server", pid(first), pid(second))
	}

	relay.mu.Lock()
	sessions := len(relay.sessions)
	relay.mu.Unlock()
	if sessions != 1 {
		t.Fatalf("%d sessions running, want 1", sessions)
	}
	relay.Close()
	relay.mu.Lock()
	sessions = len(relay.sessions)
	relay.mu.Unlock()
	if sessions != 0 {
		t.Errorf("Close left %d sessions running", sessions)
	}
}

func TestValidateRemoteMCPProxy(t *testing.T) {
	for _, ok := range []string{"", " ", "claude-in-chrome", "claude-in-chrome, claude-in-chrome"} {
		if err := ValidateRemoteMCPProxy(ok); err != nil {
			t.Errorf("ValidateRemoteMCPProxy(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"taskyou", "a.b", "claude-in-chrome,no-such-server-anywhere"} {
		if err := ValidateRemoteMCPProxy(bad); err == nil {
			t.Errorf("ValidateRemoteMCPProxy(%q) accepted it", bad)
		}
	}
}

// The launch line names the config staged ON THE HOST, and only when one was
// staged; nothing about this machine's own MCP config travels.
func TestRemoteLaunchPassesTheStagedMCPConfig(t *testing.T) {
	task := &db.Task{ID: 42, Port: 3042}
	script, err := remoteLaunchScriptWith(task, "claude", "/srv/x/.task-worktrees/42-x", "go", "/srv/x/.task-worktrees/42-x/.ty/mcp/config.json", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "claude --mcp-config '/srv/x/.task-worktrees/42-x/.ty/mcp/config.json' ") {
		t.Errorf("launch line does not pass the staged config: %s", script)
	}
	if strings.Contains(script, worktreeMCPConfigPath(42)) {
		t.Errorf("launch line carries this machine's config path: %s", script)
	}
	plain, _ := remoteLaunchScript(task, "claude", "/srv/x", "go")
	if strings.Contains(plain, "--mcp-config") {
		t.Errorf("a launch with nothing staged passes --mcp-config: %s", plain)
	}
}

// With its tools relayed the agent is told to use them — and still told what
// to do when they fail because this machine is away, which is exactly when
// they will.
func TestProxyInstructionsKeepTheSignalAsTheOfflinePath(t *testing.T) {
	got := proxyInstructions("workstation")
	for _, want := range []string{"taskyou_complete", "workstation", "unavailable", signalScriptPath + " done", "needs-input", "exactly once", "ToolSearch"} {
		if !strings.Contains(got, want) {
			t.Errorf("proxy instructions never mention %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NOT available") {
		t.Error("proxy instructions say the tools are not available")
	}
}

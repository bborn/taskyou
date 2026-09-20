package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/bborn/workflow/internal/db"
)

func TestRemoteShellAPIAndTerminalRouteEveryOperationToHost(t *testing.T) {
	srv, database, local := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "remote terminal", Status: db.StatusProcessing})
	for _, err := range []error{
		database.SetTaskPlacement(task.ID, "test-remote-host", "test"),
		database.SetTaskRemoteWorktree(task.ID, "/remote/worktree", "task/test"),
		database.UpdateTaskDaemonSession(task.ID, "task-daemon-7"),
		database.UpdateTaskPaneIDs(task.ID, "%local-agent", "%local-shell"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	t.Setenv("TY_TEST_REMOTE_DIR", dir)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TY_TEST_REMOTE_DIR/commands"
for arg do last="$arg"; done
case "$last" in
 *new-window*) touch "$TY_TEST_REMOTE_DIR/shell"; echo %92 ;;
 *list-panes*'-shell'*) test -f "$TY_TEST_REMOTE_DIR/shell" || exit 1; echo %92 ;;
 *list-panes*) echo %91 ;;
 *capture-pane*) if test -f "$TY_TEST_REMOTE_DIR/typed"; then echo typed; else echo initial; fi ;;
 *display-message*) echo '80 24' ;;
 *send-keys*) touch "$TY_TEST_REMOTE_DIR/typed" ;;
 *resize-pane*) : ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for i := 0; i < 2; i++ {
		response := ensureShellPaneRequest(t, srv, task.ID)
		if response.Code != http.StatusOK {
			t.Fatalf("shell: %d %s", response.Code, response.Body.String())
		}
		var info terminalInfoJSON
		if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.RemoteHost != "test-remote-host" || info.ShellPaneID != "%92" || info.ClaudePaneID != "%91" || info.Workdir != "/remote/worktree" {
			t.Fatalf("wrong terminal info: %+v", info)
		}
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", fmt.Sprint(task.ID))
		srv.handleTerminal(w, r)
	}))
	defer httpServer.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"?pane=shell", nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("\x1b[D")); err != nil {
		t.Fatal(err)
	}
	for {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(frame), "typed") {
			break
		}
	}
	commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
	if strings.Count(string(commands), "new-window") != 1 {
		t.Fatalf("shell not reused: %s", commands)
	}
	for _, op := range []string{"capture-pane", "display-message", "send-keys", "resize-pane"} {
		found := false
		for _, line := range strings.Split(string(commands), "\n") {
			if strings.Contains(line, op) {
				found = true
				if !strings.Contains(line, "test-remote-host") || !strings.Contains(line, "%92") {
					t.Errorf("misrouted command: %s", line)
				}
			}
		}
		if !found {
			t.Errorf("missing remote %s", op)
		}
	}
	if len(local.calls) != 0 {
		t.Fatalf("remote API touched local tmux: %v", local.calls)
	}
	fresh, _ := database.GetTask(task.ID)
	if fresh.ShellPaneID != "%local-shell" || fresh.ClaudePaneID != "%local-agent" {
		t.Fatal("remote IDs overwrote local IDs")
	}
}

// A remote task's pane lives on another machine's tmux server, so it is found
// by remoteTerminalInfo rather than by a tag lookup here — but the delivery is
// the shared one, so what reaches the host is a single paste and its Enter.
func TestRemoteTaskInputRoutesToRemoteAgentPane(t *testing.T) {
	srv, database, local := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "remote input", Status: db.StatusBlocked})
	commandsPath := remoteInputFixture(t, database, task)

	body := `{"message":"continue remotely"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/input", task.ID), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskInput(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("input: %d %s", w.Code, w.Body.String())
	}
	commands, _ := os.ReadFile(commandsPath)
	got := string(commands)
	for _, want := range []string{"test-remote-host", "set-buffer", "continue remotely", "paste-buffer", "%91", "send-keys", "Enter"} {
		if !strings.Contains(got, want) {
			t.Fatalf("remote delivery missing %q: %s", want, got)
		}
	}
	if len(local.snapshot()) != 0 {
		t.Fatalf("remote input touched local tmux: %v", local.snapshot())
	}
}

// The busy check is not a local-only courtesy: an agent working on another host
// is no more able to read a line typed into the middle of its own output.
func TestRemoteTaskInputRefusesABusyAgent(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "remote input", Status: db.StatusProcessing})
	commandsPath := remoteInputFixture(t, database, task)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/input", task.ID), strings.NewReader(body))
		req.SetPathValue("id", fmt.Sprint(task.ID))
		w := httptest.NewRecorder()
		srv.handleTaskInput(w, req)
		return w
	}

	w := post(`{"message":"while it works"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "agent_busy" {
		t.Errorf("code = %q, want agent_busy", code)
	}
	if commands, _ := os.ReadFile(commandsPath); strings.Contains(string(commands), "while it works") {
		t.Errorf("refused input reached the host anyway: %s", commands)
	}

	if w := post(`{"message":"while it works","force":true}`); w.Code != http.StatusOK {
		t.Fatalf("forced send: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	commands, _ := os.ReadFile(commandsPath)
	if !strings.Contains(string(commands), "while it works") {
		t.Errorf("forced send never reached the host: %s", commands)
	}
}

// remoteInputFixture places a task on a stubbed host and returns the file its
// fake ssh records every command in.
func remoteInputFixture(t *testing.T, database *db.DB, task *db.Task) string {
	t.Helper()
	for _, err := range []error{
		database.SetTaskPlacement(task.ID, "test-remote-host", "test"),
		database.SetTaskRemoteWorktree(task.ID, "/remote/worktree", "task/test"),
		database.UpdateTaskDaemonSession(task.ID, "task-daemon-7"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	commandsPath := filepath.Join(dir, "commands")
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "$TY_TEST_REMOTE_COMMANDS"
case "$*" in
  *list-panes*) echo %91 ;;
  *capture-pane*) echo "remote pane says hello" ;;
  *set-buffer*|*paste-buffer*|*send-keys*|*wait-for*) : ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TY_TEST_REMOTE_COMMANDS", commandsPath)
	return commandsPath
}

// `ty output`'s web twin has the same blind spot: a remotely placed task's pane
// is on that host's tmux server, and capturing it here only ever came back
// empty — which the GUI showed as a task producing nothing.
func TestRemoteTaskOutputReadsTheRemotePane(t *testing.T) {
	srv, database, local := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "remote output", Status: db.StatusProcessing})
	remoteInputFixture(t, database, task)

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/output", task.ID), nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskOutput(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("output: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Output, "remote pane says hello") {
		t.Fatalf("output did not come from the remote pane: %q", body.Output)
	}
	if len(local.snapshot()) != 0 {
		t.Fatalf("remote output touched local tmux: %v", local.snapshot())
	}
}

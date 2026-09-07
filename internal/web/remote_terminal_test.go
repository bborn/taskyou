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

	"github.com/bborn/workflow/internal/db"
	"github.com/gorilla/websocket"
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

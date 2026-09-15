package web

import (
	"fmt"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tmuxtest"
)

func TestPanelMirrorDoesNotResizeOrSelectTaskPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	tmuxtest.Isolate(t)
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pane := tmux("new-session", "-d", "-s", "mirror-test", "-x", "100", "-y", "30", "-P", "-F", "#{pane_id}", "sh")
	before := tmux("display-message", "-p", "-t", pane, "#{pane_width} #{pane_height} #{window_zoomed_flag} #{pane_pid}")
	srv, d, _ := setupServer(t)
	task := createTestTask(t, d, &db.Task{Title: "Theme preview", Status: "backlog"})
	if err := d.UpdateTaskPaneIDs(task.ID, "", pane); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(srv.srv.Handler)
	defer server.Close()
	ws, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+fmt.Sprintf("/api/tasks/%d/terminal?pane=shell&resize=0", task.ID), nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, size, err := ws.ReadMessage()
	if err != nil || !strings.Contains(string(size), `"type":"size"`) {
		t.Fatalf("size: %s %v", size, err)
	}
	if _, _, err = ws.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatal(err)
	}
	if err = ws.WriteMessage(websocket.TextMessage, []byte("mirror-input")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	input := false
	for time.Now().Before(deadline) {
		if strings.Contains(tmux("capture-pane", "-p", "-t", pane), "mirror-input") {
			input = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !input {
		t.Fatal("terminal input was not forwarded")
	}
	after := tmux("display-message", "-p", "-t", pane, "#{pane_width} #{pane_height} #{window_zoomed_flag} #{pane_pid}")
	if after != before {
		t.Fatalf("mirror changed task state: %q -> %q", before, after)
	}
	// The owning tmux client can still resize. Mirrors must learn the new size.
	tmux("resize-window", "-t", "mirror-test", "-x", "110", "-y", "35")
	changed := false
	for !changed {
		_, frame, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("missing owner resize: %v", err)
		}
		changed = strings.Contains(string(frame), `"cols":110`)
	}
}

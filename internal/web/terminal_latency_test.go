package web

import (
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

func TestTerminalEchoDoesNotWaitForIdlePoll(t *testing.T) {
	root := t.TempDir()
	frame := filepath.Join(root, "frame")
	if err := os.WriteFile(frame, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$1\" in\n capture-pane) /bin/cat '" + frame + "';;\n display-message) printf '80 24';;\n send-keys) printf 'changed' > '" + frame + "';;\nesac\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	database, err := db.Open(filepath.Join(root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := &db.Task{Title: "Audit terminal", Status: db.StatusBlocked, ClaudePaneID: "%audit"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE tasks SET claude_pane_id='%audit' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	srv := &Server{db: database}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", fmt.Sprint(task.ID))
		srv.handleTerminal(w, r)
	}))
	defer httpServer.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	conn.SetReadDeadline(start.Add(350 * time.Millisecond))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("x")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(msg), "changed") {
		t.Fatalf("unexpected frame: %q", msg)
	}
	t.Logf("WebSocket typed input to next frame: %.2f ms", float64(time.Since(start).Microseconds())/1000)
}

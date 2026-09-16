package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/bborn/workflow/internal/db"
)

// The web terminal both mirrors a pane and types into it, so a stale stored
// pane id would show one task's session while typing into another's. The tagged
// pane wins.
func TestTerminalMirrorsTheTaggedPane(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := &db.Task{Title: "Audit terminal routing", Status: db.StatusBlocked}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	database.UpdateTaskPaneIDs(task.ID, "%stale", "")

	runner := &mockRunner{outputVal: []byte("pane contents")}
	tagPaneInFakeTmux(runner, task.ID, "%live")
	srv := &Server{db: database, runner: runner}

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
	if _, _, err := conn.ReadMessage(); err != nil { // size
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil { // first frame
		t.Fatal(err)
	}

	for _, call := range runner.snapshot() {
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) && call[i+1] == "%stale" {
				t.Fatalf("terminal used the stale stored pane: %v", call)
			}
		}
	}
}

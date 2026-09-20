package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// placeRemoteTask stands a task up on a named remote host with the worktree and
// daemon session the remote terminal probe needs. Callers install their own
// ssh stub on PATH to drive the probe's failure classification (window-gone vs
// unreachable) — see executor.classifyRemoteProbeFailure.
func placeRemoteTask(t *testing.T, database *db.DB, host string) *db.Task {
	t.Helper()
	task := createTestTask(t, database, &db.Task{Title: "remote terminal", Status: db.StatusBlocked})
	for _, err := range []error{
		database.SetTaskPlacement(task.ID, host, "test"),
		database.SetTaskRemoteWorktree(task.ID, "/remote/worktree", "task/test"),
		database.UpdateTaskDaemonSession(task.ID, "task-daemon-7"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return task
}

// writeSSHStub replaces ssh on PATH with a script that reproduces a given
// remote-probe outcome. The script inspects its first argument to spot tmux
// subcommands the probe issues, so the stub is authoritative for whatever
// exit status the test wants ssh to relay.
func writeSSHStub(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A remote task whose executor window has ended must surface the canonical 409
// with executor.ErrRemoteTerminalEnded's message from the WebSocket attach
// endpoint, not the generic 400 "task has no requested terminal pane" the
// handler used to fall through to. Both the agent-pane (default) and
// ?pane=shell attach paths route through the same agent-window probe that
// returns ErrRemoteTerminalEnded, so both are covered here.
func TestTerminalReportsEndedRemoteSession(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := placeRemoteTask(t, database, "test-remote-host")
	// ssh relays the remote command's exit status: the remote tmux says the
	// window isn't there (list-panes exits 1), so classifyRemoteProbeFailure ->
	// windowGone -> ErrRemoteTerminalEnded. Exit 255 would be ssh's OWN failure
	// (windowUnreachable), so the stub keeps every path at exit 1, never 255.
	writeSSHStub(t, "#!/bin/sh\ncase \"$1\" in\n  list-panes*) exit 1 ;;\n  *) exit 1 ;;\nesac\n")

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"agent_pane", ""},
		{"shell_pane", "?pane=shell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/terminal%s", task.ID, tc.query), nil)
			req.SetPathValue("id", fmt.Sprint(task.ID))
			w := httptest.NewRecorder()
			srv.handleTerminal(w, req)

			if w.Code != http.StatusConflict {
				t.Fatalf("expected 409 Conflict for an ended remote session, got %d %q", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), executor.ErrRemoteTerminalEnded.Error()) {
				t.Fatalf("expected body to mention %q, got %q", executor.ErrRemoteTerminalEnded.Error(), w.Body.String())
			}
		})
	}
}

// A genuinely unreachable host (ssh's own exit 255) is a different state from
// an ended session: remoteTerminalInfo surfaces it as info.Error (not the
// silent WindowExists==false sentinel), so the existing info.Error != "" branch
// already returns 409. This guards against the new sentinel->409 translation
// clobbering that path with the ended-session message, which would make a down
// host look like a finished session to an operator.
func TestTerminalReportsUnreachableHostAsConflict(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := placeRemoteTask(t, database, "ghost-host")
	// ssh's own failure code, written to stderr: classifyRemoteProbeFailure ->
	// windowUnreachable, so InspectRemoteTerminal wraps the error and
	// remoteTerminalInfo returns info.Error != "" (WindowExists stays false) —
	// the path the new guard must NOT clobber with the ended-session message.
	writeSSHStub(t, "#!/bin/sh\necho 'ssh: connect to host ghost-host port 22: No route to host' >&2\nexit 255\n")

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/terminal", task.ID), nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTerminal(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for an unreachable host, got %d %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), executor.ErrRemoteTerminalEnded.Error()) {
		t.Fatalf("unreachable host must not be misreported as an ended session: %q", w.Body.String())
	}
}

// A local task with no requested pane is unaffected by the remote-session guard:
// the guard only runs inside the PlacementTarget != "" remote branch, so a task
// that never had a remote session still gets the generic 400. This guards
// against the fix accidentally widening the 409 to local tasks.
func TestTerminalLocalMissingPaneStill400(t *testing.T) {
	srv, database, _ := setupServer(t)
	// No placement, no cached pane IDs: the local branch never resolves a pane.
	task := createTestTask(t, database, &db.Task{Title: "local no pane", Status: db.StatusBlocked})

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"agent_pane", ""},
		{"shell_pane", "?pane=shell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/terminal%s", task.ID, tc.query), nil)
			req.SetPathValue("id", fmt.Sprint(task.ID))
			w := httptest.NewRecorder()
			srv.handleTerminal(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for a local task with no pane, got %d %q", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "task has no requested terminal pane") {
				t.Fatalf("expected generic missing-pane message, got %q", w.Body.String())
			}
		})
	}
}

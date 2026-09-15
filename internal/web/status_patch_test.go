package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// TestPatchTaskRefusesStatus: PATCH /api/tasks/{id} edits fields. Status is not
// a field, it is a transition, and it has one entry point that records who and
// why and runs the completion gates.
//
// Before this, `status` simply wasn't in the request struct, so a client that
// sent it got HTTP 200 and an unchanged task — the worst of both worlds. The
// status was (correctly) never written, but the caller had every reason to
// believe it had closed the task. A silent no-op is how a board and the people
// reading it come to disagree about what happened.
func TestPatchTaskRefusesStatus(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "Support Apple Pay at checkout", Status: db.StatusBacklog, Type: db.TypeCode})

	for _, body := range []string{
		`{"status":"done"}`,
		`{"status":"archived"}`,
		`{"title":"renamed","status":"done"}`,
	} {
		w := patchTask(t, srv, task.ID, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s: expected 400, got %d (%s)", body, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "/status") {
			t.Errorf("PATCH %s: the refusal must point at the route that has the gates, got %s", body, w.Body.String())
		}
		got, err := database.GetTask(task.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if got.Status != db.StatusBacklog {
			t.Fatalf("PATCH %s moved the task to %q", body, got.Status)
		}
		// The whole request is refused, not just the status field: a partial
		// apply would leave the caller guessing which half landed.
		if got.Title != "Support Apple Pay at checkout" {
			t.Errorf("PATCH %s applied the rest of the request anyway: title is %q", body, got.Title)
		}
	}

	// And the transition never reached the log, because it never reached
	// SetTaskStatus — not even as a refusal.
	events, err := database.GetStatusEvents(task.ID)
	if err != nil {
		t.Fatalf("get status events: %v", err)
	}
	for _, e := range events {
		if e.To == db.StatusDone || e.To == db.StatusArchived {
			t.Fatalf("a terminal transition reached the log from the PATCH route: %+v", e)
		}
	}
}

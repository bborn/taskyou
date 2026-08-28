package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// patchTask issues a PATCH /api/tasks/{id} with the given JSON body.
func patchTask(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/tasks/%d", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprint(id))
	w := httptest.NewRecorder()
	srv.handleUpdateTask(w, req)
	return w
}

// TestHandleUpdateTask_RejectsUnknownModel closes the API half of model
// validation: the GUI and any HTTP client go through here, and a model the
// agent's CLI won't accept stalls the task exactly as it would from the CLI.
func TestHandleUpdateTask_RejectsUnknownModel(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "t", Status: db.StatusBacklog, Type: db.TypeCode})

	t.Setenv("ANTHROPIC_BASE_URL", "")

	tests := []struct {
		name string
		body string
	}{
		{"typo", `{"model":"opuss"}`},
		{"executor slug as model", `{"model":"claude"}`},
		{"other vendor", `{"model":"gpt-5"}`},
		{"model on a modelless executor", `{"executor":"codex","model":"opus"}`},
		{"claude alias on grok", `{"executor":"grok","model":"opus"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := patchTask(t, srv, task.ID, tt.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
			}
			// The rejection must not have been persisted.
			got, err := database.GetTask(task.ID)
			if err != nil {
				t.Fatalf("get task: %v", err)
			}
			if got.Model != "" {
				t.Errorf("rejected model was stored anyway: %q", got.Model)
			}
		})
	}
}

// TestHandleUpdateTask_AcceptsRealModels guards against over-strictness on the
// API path, including model IDs newer than this code.
func TestHandleUpdateTask_AcceptsRealModels(t *testing.T) {
	srv, database, _ := setupServer(t)
	t.Setenv("ANTHROPIC_BASE_URL", "")

	tests := []struct {
		name string
		body string
		want string
	}{
		{"alias", `{"model":"opus"}`, "opus"},
		{"full id", `{"model":"claude-opus-5"}`, "claude-opus-5"},
		{"unreleased id", `{"model":"claude-opus-9"}`, "claude-opus-9"},
		{"grok id with executor", `{"executor":"grok","model":"grok-4"}`, "grok-4"},
		{"clearing the override", `{"model":""}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := createTestTask(t, database, &db.Task{Title: tt.name, Status: db.StatusBacklog, Type: db.TypeCode})
			w := patchTask(t, srv, task.ID, tt.body)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
			}
			// The response echoes the task; the row is what actually matters.
			var echoed map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &echoed); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if echoed["model"] != nil && echoed["model"] != tt.want {
				t.Errorf("response model = %v, want %q", echoed["model"], tt.want)
			}
			stored, err := database.GetTask(task.ID)
			if err != nil {
				t.Fatalf("get task: %v", err)
			}
			if stored.Model != tt.want {
				t.Errorf("stored model = %q, want %q", stored.Model, tt.want)
			}
		})
	}
}

// TestHandleUpdateTask_LeavesStoredModelAlone is the compatibility guard: a
// task carrying a model this check would now reject (stored before the check
// existed) must still accept unrelated edits. Validation runs only when the
// request is actually setting a model.
func TestHandleUpdateTask_LeavesStoredModelAlone(t *testing.T) {
	srv, database, _ := setupServer(t)
	t.Setenv("ANTHROPIC_BASE_URL", "")

	task := createTestTask(t, database, &db.Task{Title: "legacy", Status: db.StatusBacklog, Type: db.TypeCode})
	// Write a now-invalid model straight to the row, the way an older build could.
	if _, err := database.Exec(`UPDATE tasks SET model = 'claude' WHERE id = ?`, task.ID); err != nil {
		t.Fatalf("seed legacy model: %v", err)
	}

	w := patchTask(t, srv, task.ID, `{"title":"renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("a title-only edit must not be blocked by a legacy model: %d (%s)", w.Code, w.Body.String())
	}
	stored, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Title != "renamed" {
		t.Errorf("title = %q, want %q", stored.Title, "renamed")
	}
	if stored.Model != "claude" {
		t.Errorf("stored model should be untouched, got %q", stored.Model)
	}
}

// TestHandleUpdateTask_ProxyModelNeedsRouting mirrors the CLI escape hatch on
// the API: a proxy-only model is rejected on a stock backend and accepted once
// the task is actually routed at the proxy.
func TestHandleUpdateTask_ProxyModelNeedsRouting(t *testing.T) {
	srv, database, _ := setupServer(t)
	t.Setenv("ANTHROPIC_BASE_URL", "")

	task := createTestTask(t, database, &db.Task{Title: "ollama", Status: db.StatusBacklog, Type: db.TypeCode})
	if w := patchTask(t, srv, task.ID, `{"model":"glm-5.2:cloud"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a proxy model on a stock backend, got %d", w.Code)
	}

	// Route the task at the proxy, then the same model is legitimate.
	if _, err := database.Exec(`UPDATE tasks SET claude_config_dir = '~/.claude-ollama' WHERE id = ?`, task.ID); err != nil {
		t.Fatalf("route task at proxy: %v", err)
	}
	if w := patchTask(t, srv, task.ID, `{"model":"glm-5.2:cloud"}`); w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a proxy model on a routed task, got %d (%s)", w.Code, w.Body.String())
	}
	stored, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Model != "glm-5.2:cloud" {
		t.Errorf("stored model = %q, want %q", stored.Model, "glm-5.2:cloud")
	}
}

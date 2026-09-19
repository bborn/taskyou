package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// blockedOnQuestion makes a blocked task whose agent asked q, with its agent
// pane tagged in the fake tmux.
func blockedOnQuestion(t *testing.T, database *db.DB, runner *mockRunner, q *db.PendingQuestion) *db.Task {
	t.Helper()
	task := &db.Task{Title: "Pick a cache", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	q.TaskID = task.ID
	if err := database.SetPendingQuestion(q); err != nil {
		t.Fatalf("set question: %v", err)
	}
	tagPaneInFakeTmux(runner, task.ID, "%7")
	return task
}

func cacheQuestion() *db.PendingQuestion {
	return &db.PendingQuestion{
		Question: "Which cache backend?",
		Kind:     db.QuestionChoice,
		Options: []db.QuestionOption{
			{Label: "Redis", Description: "already in prod"},
			{Label: "Memcached"},
			{Label: "In-process LRU"},
		},
		AllowOther: true,
	}
}

func postAnswer(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/answer", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleAnswerQuestion(w, req)
	return w
}

// The answer reaches the agent as the plain text it can read, through the same
// one-paste delivery a typed reply uses, and the question is then gone.
func TestHandleAnswerQuestion_DeliversTheFormattedAnswer(t *testing.T) {
	srv, database, runner := setupServer(t)
	q := cacheQuestion()
	task := blockedOnQuestion(t, database, runner, q)

	w := postAnswer(t, srv, task.ID, fmt.Sprintf(`{"question_id":%d,"choices":[1]}`, q.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Answer string `json:"answer"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Answer != "Selected: Redis" {
		t.Errorf("answer = %q, want %q", resp.Answer, "Selected: Redis")
	}

	calls := runner.waitForPrompts(t, 3)
	if calls[0][1] != "set-buffer" || calls[0][len(calls[0])-1] != "Selected: Redis" {
		t.Errorf("answer not staged as one buffer: %v", calls[0])
	}
	if !hasFlag(calls[1], "-p") {
		t.Errorf("answer not pasted in bracketed-paste mode: %v", calls[1])
	}
	for _, call := range calls {
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) && call[i+1] != "%7" {
				t.Fatalf("answer aimed at %q, want the tagged pane %%7: %v", call[i+1], call)
			}
		}
	}

	if got, _ := database.GetPendingQuestion(task.ID); got != nil {
		t.Errorf("question still pending after it was answered: %+v", got)
	}
}

func TestHandleAnswerQuestion_OtherIsSentAsWritten(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := blockedOnQuestion(t, database, runner, cacheQuestion())

	w := postAnswer(t, srv, task.ID, `{"other":"Whatever the payments team uses"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	calls := runner.waitForPrompts(t, 3)
	if got := calls[0][len(calls[0])-1]; got != "Whatever the payments team uses" {
		t.Errorf("staged %q, want the words as written", got)
	}
}

// An answer that does not fit the question is the person's to fix: nothing is
// typed, and the question stays up.
func TestHandleAnswerQuestion_RejectsAnInvalidAnswer(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := blockedOnQuestion(t, database, runner, cacheQuestion())

	for name, body := range map[string]string{
		"out of range":   `{"choices":[4]}`,
		"two for choice": `{"choices":[1,2]}`,
		"nothing":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := postAnswer(t, srv, task.ID, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if code := errCode(t, w); code != "invalid_answer" {
				t.Errorf("code = %q, want invalid_answer", code)
			}
		})
	}
	if got := prompts(runner.snapshot()); len(got) != 0 {
		t.Errorf("an invalid answer was typed into tmux: %v", got)
	}
	if got, _ := database.GetPendingQuestion(task.ID); got == nil {
		t.Error("an invalid answer took the question down")
	}
}

// A person looking at an old question must not have their pick applied to the
// new one: option 2 of the old question is not option 2 of this one.
func TestHandleAnswerQuestion_RefusesAnAnswerToAReplacedQuestion(t *testing.T) {
	srv, database, runner := setupServer(t)
	old := cacheQuestion()
	task := blockedOnQuestion(t, database, runner, old)
	newer := &db.PendingQuestion{TaskID: task.ID, Question: "Ship it?", Kind: db.QuestionConfirm}
	if err := database.SetPendingQuestion(newer); err != nil {
		t.Fatalf("replace question: %v", err)
	}

	w := postAnswer(t, srv, task.ID, fmt.Sprintf(`{"question_id":%d,"choices":[2]}`, old.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "question_changed" {
		t.Errorf("code = %q, want question_changed", code)
	}
	if got := prompts(runner.snapshot()); len(got) != 0 {
		t.Errorf("a stale answer was typed into tmux: %v", got)
	}
}

func TestHandleAnswerQuestion_NoQuestion(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := &db.Task{Title: "Just blocked", Status: db.StatusBlocked, Project: "personal"}
	database.CreateTask(task)
	tagPaneInFakeTmux(runner, task.ID, "%7")

	w := postAnswer(t, srv, task.ID, `{"choices":[1]}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "no_question" {
		t.Errorf("code = %q, want no_question", code)
	}
}

// With no live agent the answer cannot be delivered, so the question is put
// back for another try instead of being lost.
func TestHandleAnswerQuestion_KeepsTheQuestionWhenThereIsNoAgent(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := &db.Task{Title: "Agent gone", Status: db.StatusBlocked, Project: "personal"}
	database.CreateTask(task)
	q := cacheQuestion()
	q.TaskID = task.ID
	database.SetPendingQuestion(q)

	w := postAnswer(t, srv, task.ID, `{"choices":[2]}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := errCode(t, w); code != "no_agent_pane" {
		t.Errorf("code = %q, want no_agent_pane", code)
	}
	got, _ := database.GetPendingQuestion(task.ID)
	if got == nil || got.ID != q.ID {
		t.Fatalf("question after a failed delivery = %+v, want %d restored", got, q.ID)
	}
}

func TestHandleGetQuestion(t *testing.T) {
	srv, database, runner := setupServer(t)
	task := blockedOnQuestion(t, database, runner, &db.PendingQuestion{Question: "Deploy now?", Kind: db.QuestionConfirm})

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/question", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleGetQuestion(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Question *questionJSON `json:"question"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Question == nil || resp.Question.Kind != db.QuestionConfirm {
		t.Fatalf("question = %+v, want the confirm", resp.Question)
	}
	// A confirm is answered like a choice, so the client is handed its two options.
	if len(resp.Question.Options) != 2 || resp.Question.Options[0].Label != "Yes" || resp.Question.Options[1].Label != "No" {
		t.Errorf("confirm options = %+v, want Yes, No", resp.Question.Options)
	}
}

// The GUI refreshes its board with GET /api/tasks, so the question rides on the
// task there — and only on a task that is still blocked on it.
func TestHandleListTasks_CarriesPendingQuestions(t *testing.T) {
	srv, database, runner := setupServer(t)
	blocked := blockedOnQuestion(t, database, runner, cacheQuestion())
	other := &db.Task{Title: "Unrelated", Status: db.StatusBacklog, Project: "personal"}
	database.CreateTask(other)

	req := httptest.NewRequest("GET", "/api/tasks?all=true", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	var tasks []*taskJSON
	json.NewDecoder(w.Body).Decode(&tasks)
	found := false
	for _, tj := range tasks {
		switch tj.ID {
		case blocked.ID:
			found = true
			if tj.Question == nil || tj.Question.Question != "Which cache backend?" || len(tj.Question.Options) != 3 {
				t.Errorf("blocked task question = %+v", tj.Question)
			}
		case other.ID:
			if tj.Question != nil {
				t.Errorf("unrelated task carries a question: %+v", tj.Question)
			}
		}
	}
	if !found {
		t.Fatal("blocked task missing from the list")
	}
}

// A task placed on another host is answered through the same route /input
// takes there (resolveInputRoute): its pane is found on the host, and the answer
// arrives as a single paste and its Enter — nothing touches this machine's tmux.
func TestRemoteTaskAnswerRoutesToRemoteAgentPane(t *testing.T) {
	srv, database, local := setupServer(t)
	task := createTestTask(t, database, &db.Task{Title: "remote question", Status: db.StatusBlocked})
	commandsPath := remoteInputFixture(t, database, task)
	q := cacheQuestion()
	q.TaskID = task.ID
	if err := database.SetPendingQuestion(q); err != nil {
		t.Fatal(err)
	}

	w := postAnswer(t, srv, task.ID, fmt.Sprintf(`{"question_id":%d,"choices":[3]}`, q.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body.String())
	}
	commands, _ := os.ReadFile(commandsPath)
	got := string(commands)
	for _, want := range []string{"test-remote-host", "set-buffer", "Selected: In-process LRU", "paste-buffer", "%91", "send-keys", "Enter"} {
		if !strings.Contains(got, want) {
			t.Fatalf("remote delivery missing %q: %s", want, got)
		}
	}
	if len(local.snapshot()) != 0 {
		t.Fatalf("remote answer touched local tmux: %v", local.snapshot())
	}
	if got, _ := database.GetPendingQuestion(task.ID); got != nil {
		t.Errorf("question still pending after a delivered answer: %+v", got)
	}
}

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// callNeedsInput runs one taskyou_needs_input call and returns the tool result.
func callNeedsInput(t *testing.T, database *db.DB, taskID int64, args map[string]interface{}) toolCallResult {
	t.Helper()
	reqBytes, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": "taskyou_needs_input", "arguments": args},
	})
	server, output := testServer(database, taskID, string(reqBytes)+"\n")
	server.Run()

	var resp struct {
		Result toolCallResult `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("parse response %q: %v", output.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("protocol error %q; a bad call should be a tool error the agent can read", resp.Error.Message)
	}
	return resp.Result
}

func TestNeedsInput_ChoiceIsStoredForEverySurface(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	res := callNeedsInput(t, database, task.ID, map[string]interface{}{
		"question": "Which cache backend?",
		"kind":     "choice",
		"options": []interface{}{
			map[string]interface{}{"label": "Redis", "description": "already in prod"},
			map[string]interface{}{"label": "Memcached"},
		},
		"allow_other": true,
	})
	if res.IsError {
		t.Fatalf("valid call returned a tool error: %+v", res.Content)
	}
	if text := res.Content[0].Text; !strings.Contains(text, `"Selected: <label>"`) {
		t.Errorf("result does not say what the answer will look like: %q", text)
	}

	updated, _ := database.GetTask(task.ID)
	if updated.Status != db.StatusBlocked {
		t.Fatalf("status = %q, want blocked", updated.Status)
	}
	q, err := database.GetPendingQuestion(task.ID)
	if err != nil || q == nil {
		t.Fatalf("pending question = %v, %v; want the question stored", q, err)
	}
	if q.Kind != db.QuestionChoice || !q.AllowOther || len(q.Options) != 2 || q.Options[0].Description != "already in prod" {
		t.Errorf("stored question = %+v", q)
	}
}

// The plain call keeps working exactly as it did: blocked, logged, same reply.
func TestNeedsInput_PlainQuestionIsUnchanged(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	res := callNeedsInput(t, database, task.ID, map[string]interface{}{"question": "What should I do next?"})
	if res.IsError || res.Content[0].Text != "Input requested. The user will be notified." {
		t.Fatalf("result = %+v, want the unchanged reply", res)
	}
	if updated, _ := database.GetTask(task.ID); updated.Status != db.StatusBlocked {
		t.Fatalf("status = %q, want blocked", updated.Status)
	}
	logs, _ := database.GetTaskLogs(task.ID, 5)
	logged := false
	for _, l := range logs {
		if l.LineType == "question" && l.Content == "What should I do next?" {
			logged = true
		}
	}
	if !logged {
		t.Error("question not logged")
	}
	if q, _ := database.GetPendingQuestion(task.ID); q == nil || q.Kind != db.QuestionText {
		t.Errorf("pending question = %+v, want a text question", q)
	}
}

// Agents send options in shapes other than the documented one; the common
// ones are accepted rather than bounced.
func TestNeedsInput_AcceptsStringOptions(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	res := callNeedsInput(t, database, task.ID, map[string]interface{}{
		"question": "Which region?",
		"options":  `["us-east-1", "eu-west-1"]`,
	})
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	q, _ := database.GetPendingQuestion(task.ID)
	if q == nil || q.Kind != db.QuestionChoice || len(q.Options) != 2 || q.Options[1].Label != "eu-west-1" {
		t.Fatalf("stored question = %+v", q)
	}
}

// A malformed question is a tool error the agent can read and fix — and it asks
// nothing: the task is not blocked on a question nobody can answer.
func TestNeedsInput_RejectsBadShapes(t *testing.T) {
	tests := map[string]struct {
		args map[string]interface{}
		want string
	}{
		"choice with one option": {
			args: map[string]interface{}{"question": "Which?", "kind": "choice", "options": []interface{}{"only"}},
			want: "needs 2 to 6 options (got 1)",
		},
		"unknown kind": {
			args: map[string]interface{}{"question": "Which?", "kind": "ranking"},
			want: `unknown kind "ranking"`,
		},
		"options not an array": {
			args: map[string]interface{}{"question": "Which?", "kind": "choice", "options": 3},
			want: "options must be an array",
		},
		"option without a label": {
			args: map[string]interface{}{"question": "Which?", "options": []interface{}{map[string]interface{}{"description": "x"}, "B"}},
			want: "option 1 needs a string label",
		},
		"confirm with options": {
			args: map[string]interface{}{"question": "Ok?", "kind": "confirm", "options": []interface{}{"Sure", "Nope"}},
			want: "answered Yes or No",
		},
		"missing question": {
			args: map[string]interface{}{"kind": "confirm"},
			want: "question is required",
		},
		"allow_other not a bool": {
			args: map[string]interface{}{"question": "Which?", "allow_other": "yes"},
			want: "allow_other must be true or false",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			database := testDB(t)
			task := createTestTask(t, database)

			res := callNeedsInput(t, database, task.ID, tt.args)
			if !res.IsError {
				t.Fatalf("bad call accepted: %+v", res.Content)
			}
			if text := res.Content[0].Text; !strings.Contains(text, tt.want) {
				t.Errorf("error %q does not contain %q", text, tt.want)
			}
			if updated, _ := database.GetTask(task.ID); updated.Status != db.StatusProcessing {
				t.Errorf("status = %q; a rejected question must not block the task", updated.Status)
			}
		})
	}
}

// The schema is how an agent learns the options exist at all.
func TestNeedsInput_SchemaAdvertisesOptions(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)
	server, output := testServer(database, task.ID, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")
	server.Run()

	var resp struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, tl := range resp.Result.Tools {
		if tl.Name != "taskyou_needs_input" {
			continue
		}
		props, _ := tl.InputSchema["properties"].(map[string]interface{})
		for _, p := range []string{"question", "kind", "options", "allow_other"} {
			if _, ok := props[p]; !ok {
				t.Errorf("schema lacks %q", p)
			}
		}
		if req, _ := tl.InputSchema["required"].([]interface{}); len(req) != 1 || req[0] != "question" {
			t.Errorf("required = %v, want only question", tl.InputSchema["required"])
		}
		if !strings.Contains(tl.Description, "multi_choice") || !strings.Contains(tl.Description, "discrete decision") {
			t.Errorf("description does not explain when to offer options: %q", tl.Description)
		}
		return
	}
	t.Fatal("taskyou_needs_input not listed")
}

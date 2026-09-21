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
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "taskyou_needs_input", "arguments": args},
	})
	server, output := testServer(database, taskID, string(reqBytes)+"\n")
	server.Run()
	var resp struct {
		Result toolCallResult `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("parse %q: %v", output.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("protocol error %q; a bad call should be a tool error the agent can read", resp.Error.Message)
	}
	return resp.Result
}

// Offered answers are logged just ahead of the question, so the question stays
// the latest line for everything that already looks for it.
func TestNeedsInputLogsOptionsAheadOfTheQuestion(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	res := callNeedsInput(t, database, task.ID, map[string]interface{}{
		"question": "Which cache backend?",
		"options": []interface{}{
			map[string]interface{}{"label": "Redis", "description": "already in prod"},
			map[string]interface{}{"label": "Memcached"},
		},
	})
	if res.IsError || !strings.Contains(res.Content[0].Text, "label they pick arrives as your next message") {
		t.Fatalf("result = %+v", res)
	}

	logs, _ := database.GetTaskLogs(task.ID, 10) // newest first
	if len(logs) < 2 || logs[0].LineType != "question" || logs[1].LineType != db.LogQuestionOptions {
		t.Fatalf("want the question with its options just before it, got %+v", logs)
	}
	opts := db.QuestionOptionsFor(logs, 0)
	if len(opts) != 2 || opts[0].Label != "Redis" || opts[0].Description != "already in prod" || opts[1].Label != "Memcached" {
		t.Errorf("logged options = %+v", opts)
	}
	if updated, _ := database.GetTask(task.ID); updated.Status != db.StatusBlocked {
		t.Errorf("status = %q, want blocked", updated.Status)
	}
}

// A plain question behaves exactly as before: same reply, no options line.
func TestNeedsInputWithoutOptionsIsUnchanged(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	res := callNeedsInput(t, database, task.ID, map[string]interface{}{"question": "What should I do next?"})
	if res.IsError || res.Content[0].Text != "Input requested. The user will be notified." {
		t.Fatalf("result = %+v, want the unchanged reply", res)
	}
	logs, _ := database.GetTaskLogs(task.ID, 10)
	for _, l := range logs {
		if l.LineType == db.LogQuestionOptions {
			t.Errorf("options line written for a plain question: %q", l.Content)
		}
	}
}

// A malformed options list is a tool error the agent can fix, and nothing is
// asked: no log lines, and the task is not blocked.
func TestNeedsInputRejectsBadOptions(t *testing.T) {
	for name, tt := range map[string]struct {
		options interface{}
		want    string
	}{
		"one option":    {[]interface{}{map[string]interface{}{"label": "Only"}}, "2 to 6 entries, got 1"},
		"seven options": {[]interface{}{"a", "b", "c", "d", "e", "f", "g"}, "2 to 6 entries, got 7"},
		"not an array":  {"Redis, Memcached", "options must be an array"},
		"blank label":   {[]interface{}{map[string]interface{}{"label": "Redis"}, map[string]interface{}{"label": " "}}, "option 2 needs a non-empty label"},
		"bare strings":  {[]interface{}{"Redis", "Memcached"}, `option 1 must be an object like {"label": "Redis"}`},
		"duplicates":    {[]interface{}{map[string]interface{}{"label": "Redis"}, map[string]interface{}{"label": "redis"}}, "appears twice"},
	} {
		t.Run(name, func(t *testing.T) {
			database := testDB(t)
			task := createTestTask(t, database)

			res := callNeedsInput(t, database, task.ID, map[string]interface{}{"question": "Which?", "options": tt.options})
			if !res.IsError || !strings.Contains(res.Content[0].Text, tt.want) {
				t.Fatalf("result = %+v, want a tool error containing %q", res, tt.want)
			}
			if logs, _ := database.GetTaskLogs(task.ID, 10); len(logs) != 0 {
				t.Errorf("a rejected question was logged: %+v", logs)
			}
			if updated, _ := database.GetTask(task.ID); updated.Status != db.StatusProcessing {
				t.Errorf("status = %q; a rejected question must not block the task", updated.Status)
			}
		})
	}
}

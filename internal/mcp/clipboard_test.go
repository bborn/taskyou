package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func callClipboard(t *testing.T, server *Server, text string) toolCallResult {
	t.Helper()
	req, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "taskyou_copy_to_clipboard", "arguments": map[string]interface{}{"text": text}},
	})
	out := server.Handle(req)
	var resp struct {
		Result toolCallResult `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("parse %s: %v", out, err)
	}
	if resp.Error != nil {
		t.Fatalf("rpc error: %s", resp.Error.Message)
	}
	return resp.Result
}

// The tool hands the text over untouched, and the task log — which is not a
// place for secrets — records only its size.
func TestCopyToClipboardDeliversTextAndKeepsItOutOfTheLog(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)
	server, _ := testServer(database, task.ID, "")
	var got string
	server.copyToClipboard = func(_ context.Context, text string) ([]string, error) {
		got = text
		return []string{"system clipboard (pbcopy)"}, nil
	}

	secret := "export TOKEN_VALUE=sk-live-very-long-value \\\n  && deploy --all"
	res := callClipboard(t, server, secret)
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if got != secret {
		t.Fatalf("copied %q, want %q", got, secret)
	}
	if !strings.Contains(res.Content[0].Text, "pbcopy") {
		t.Errorf("result does not say where the text went: %q", res.Content[0].Text)
	}
	logs, err := database.GetTaskLogs(task.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	logged := false
	for _, l := range logs {
		logged = logged || strings.Contains(l.Content, "Copied")
		if strings.Contains(l.Content, "sk-live") {
			t.Errorf("task log holds the copied text: %q", l.Content)
		}
	}
	if !logged {
		t.Error("the copy left no trace in the task log")
	}
}

func TestCopyToClipboardFailureIsAToolErrorTheAgentCanRead(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)
	server, _ := testServer(database, task.ID, "")
	server.copyToClipboard = func(context.Context, string) ([]string, error) {
		return nil, errors.New("could not reach the clipboard: nothing here")
	}
	res := callClipboard(t, server, "x")
	if !res.IsError || !strings.Contains(res.Content[0].Text, "nothing here") {
		t.Fatalf("result = %+v", res)
	}
}

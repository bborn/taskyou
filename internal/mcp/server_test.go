package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// testDB creates a temporary database for testing.
func testDB(t *testing.T) *db.DB {
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	return database
}

// createTestTask creates a task for testing.
func createTestTask(t *testing.T, database *db.DB) *db.Task {
	// First create the test-project
	if err := database.CreateProject(&db.Project{Name: "test-project", Path: "/tmp/test-project"}); err != nil {
		t.Fatalf("failed to create test-project: %v", err)
	}

	task := &db.Task{
		Title:   "Test Task",
		Status:  db.StatusProcessing,
		Project: "test-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	return task
}

// testServer creates a server with mocked IO for testing.
func testServer(database *db.DB, taskID int64, input string) (*Server, *bytes.Buffer) {
	output := &bytes.Buffer{}
	server := &Server{
		db:     database,
		taskID: taskID,
		reader: bufio.NewReader(strings.NewReader(input)),
		writer: output,
	}
	return server, output
}

func TestToolsList(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Check that the tools list includes workflow_screenshot
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be a map")
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools to be an array")
	}

	var foundScreenshot bool
	for _, toolI := range tools {
		tool, ok := toolI.(map[string]interface{})
		if !ok {
			continue
		}
		if tool["name"] == "workflow_screenshot" {
			foundScreenshot = true
			// Verify the tool has proper schema
			schema, ok := tool["inputSchema"].(map[string]interface{})
			if !ok {
				t.Error("expected inputSchema to be a map")
			}
			props, ok := schema["properties"].(map[string]interface{})
			if !ok {
				t.Error("expected properties to be a map")
			}
			if _, ok := props["filename"]; !ok {
				t.Error("expected filename property")
			}
			if _, ok := props["description"]; !ok {
				t.Error("expected description property")
			}
		}
	}

	if !foundScreenshot {
		t.Error("workflow_screenshot not found in tools list")
	}
}

func TestWorkflowComplete(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_complete",
			"arguments": map[string]interface{}{
				"summary": "Task completed successfully",
			},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Verify task status was updated
	updatedTask, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if updatedTask.Status != db.StatusDone {
		t.Errorf("expected status 'done', got '%s'", updatedTask.Status)
	}
}

func TestWorkflowNeedsInput(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_needs_input",
			"arguments": map[string]interface{}{
				"question": "What should I do next?",
			},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Verify task status was updated to blocked
	updatedTask, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if updatedTask.Status != db.StatusBlocked {
		t.Errorf("expected status 'blocked', got '%s'", updatedTask.Status)
	}
}

func TestUnknownTool(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "unknown_tool",
			"arguments": map[string]interface{}{},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(resp.Error.Message, "Unknown tool") {
		t.Errorf("expected error about unknown tool, got: %s", resp.Error.Message)
	}
}

func TestInitialize(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be a map")
	}

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion '2024-11-05', got '%v'", result["protocolVersion"])
	}

	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatal("expected serverInfo to be a map")
	}
	if serverInfo["name"] != "workflow-mcp" {
		t.Errorf("expected server name 'workflow-mcp', got '%v'", serverInfo["name"])
	}
}

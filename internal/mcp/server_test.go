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

func TestWorkflowCreateTask(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_create_task",
			"arguments": map[string]interface{}{
				"title":  "New Task",
				"body":   "Description of new task",
				"status": "queued",
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

	// Verify task was created
	tasks, err := database.ListTasks(db.ListTasksOptions{Status: "queued"})
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}

	var found bool
	for _, tsk := range tasks {
		if tsk.Title == "New Task" {
			found = true
			if tsk.Body != "Description of new task" {
				t.Errorf("expected body 'Description of new task', got '%s'", tsk.Body)
			}
			if tsk.Project != task.Project {
				t.Errorf("expected project '%s', got '%s'", task.Project, tsk.Project)
			}
			break
		}
	}

	if !found {
		t.Error("new task not found in database")
	}
}

func TestWorkflowListTasks(t *testing.T) {
	database := testDB(t)
	// Create current task
	currentTask := createTestTask(t, database)

	// Create another task in the same project
	otherTask := &db.Task{
		Title:   "Other Task",
		Status:  db.StatusQueued,
		Project: "test-project",
	}
	if err := database.CreateTask(otherTask); err != nil {
		t.Fatalf("failed to create other task: %v", err)
	}

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "workflow_list_tasks",
			"arguments": map[string]interface{}{},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, currentTask.ID, string(reqBytes))
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
	content, ok := result["content"].([]interface{})
	if !ok {
		t.Fatal("expected content to be an array")
	}
	textBlock, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected text block to be a map")
	}
	text := textBlock["text"].(string)

	if !strings.Contains(text, "Other Task") {
		t.Errorf("expected output to contain 'Other Task', got:\n%s", text)
	}
	if !strings.Contains(text, "Test Task") {
		t.Errorf("expected output to contain 'Test Task', got:\n%s", text)
	}
}

func TestWorkflowGetProjectContext(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	// First call should return empty context message
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "workflow_get_project_context",
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

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be a map")
	}
	content, ok := result["content"].([]interface{})
	if !ok {
		t.Fatal("expected content to be an array")
	}
	textBlock, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected text block to be a map")
	}
	text := textBlock["text"].(string)

	if !strings.Contains(text, "No cached project context found") {
		t.Errorf("expected 'No cached project context found', got:\n%s", text)
	}

	// Now set some context
	testContext := "This is a test codebase with Go files."
	if err := database.SetProjectContext("test-project", testContext); err != nil {
		t.Fatalf("failed to set project context: %v", err)
	}

	// Call again to get the cached context
	server, output = testServer(database, task.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result, ok = resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be a map")
	}
	content, ok = result["content"].([]interface{})
	if !ok {
		t.Fatal("expected content to be an array")
	}
	textBlock, ok = content[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected text block to be a map")
	}
	text = textBlock["text"].(string)

	if !strings.Contains(text, testContext) {
		t.Errorf("expected context '%s' in output, got:\n%s", testContext, text)
	}
}

func TestWorkflowSetProjectContext(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	testContext := "This is a Go project with cmd/, internal/, and pkg/ directories."

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_set_project_context",
			"arguments": map[string]interface{}{
				"context": testContext,
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

	// Verify the context was saved
	savedContext, err := database.GetProjectContext("test-project")
	if err != nil {
		t.Fatalf("failed to get project context: %v", err)
	}
	if savedContext != testContext {
		t.Errorf("expected saved context '%s', got '%s'", testContext, savedContext)
	}

	// Test with empty context (should fail)
	request = map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_set_project_context",
			"arguments": map[string]interface{}{
				"context": "",
			},
		},
	}
	reqBytes, _ = json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Error("expected error for empty context")
	}
}

// TestProjectContextCachingFlow tests the full end-to-end flow of context caching
func TestProjectContextCachingFlow(t *testing.T) {
	database := testDB(t)

	// Create a project
	if err := database.CreateProject(&db.Project{Name: "caching-test-project", Path: "/tmp/caching-test"}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create first task
	task1 := &db.Task{
		Title:   "First Task",
		Status:  db.StatusProcessing,
		Project: "caching-test-project",
	}
	if err := database.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task1: %v", err)
	}

	// Step 1: First task tries to get context - should be empty
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "workflow_get_project_context",
			"arguments": map[string]interface{}{},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task1.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "No cached project context found") {
		t.Errorf("expected empty context message, got: %s", text)
	}

	// Step 2: First task sets context after exploration
	explorationContext := `## Project Structure
- cmd/task - CLI entry point
- internal/db - Database layer
- internal/mcp - MCP server
- internal/executor - Task execution

## Key Patterns
- Uses SQLite with migrations
- MCP for Claude communication
- Tmux for session management`

	request = map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "workflow_set_project_context",
			"arguments": map[string]interface{}{
				"context": explorationContext,
			},
		},
	}
	reqBytes, _ = json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task1.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("failed to set context: %s", resp.Error.Message)
	}

	// Step 3: Create second task in same project
	task2 := &db.Task{
		Title:   "Second Task",
		Status:  db.StatusProcessing,
		Project: "caching-test-project",
	}
	if err := database.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task2: %v", err)
	}

	// Step 4: Second task gets cached context - should have the context
	request = map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "workflow_get_project_context",
			"arguments": map[string]interface{}{},
		},
	}
	reqBytes, _ = json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task2.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result = resp.Result.(map[string]interface{})
	content = result["content"].([]interface{})
	text = content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "internal/db - Database layer") {
		t.Errorf("expected cached context in response, got: %s", text)
	}
	if !strings.Contains(text, "Key Patterns") {
		t.Errorf("expected full context, got: %s", text)
	}

	// Step 5: Verify context persists in database
	savedContext, err := database.GetProjectContext("caching-test-project")
	if err != nil {
		t.Fatalf("failed to get context from DB: %v", err)
	}
	if savedContext != explorationContext {
		t.Errorf("context not properly saved in DB")
	}

	t.Log("Full context caching flow works correctly!")
}

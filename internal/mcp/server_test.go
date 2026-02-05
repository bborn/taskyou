package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
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

	// Check that the tools list includes taskyou_screenshot
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
		if tool["name"] == "taskyou_screenshot" {
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
		t.Error("taskyou_screenshot not found in tools list")
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
			"name": "taskyou_complete",
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
			"name": "taskyou_needs_input",
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
	if serverInfo["name"] != "taskyou-mcp" {
		t.Errorf("expected server name 'taskyou-mcp', got '%v'", serverInfo["name"])
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
			"name": "taskyou_create_task",
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
			"name":      "taskyou_list_tasks",
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
			"name":      "taskyou_get_project_context",
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
			"name": "taskyou_set_project_context",
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
			"name": "taskyou_set_project_context",
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
			"name":      "taskyou_get_project_context",
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
			"name": "taskyou_set_project_context",
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
			"name":      "taskyou_get_project_context",
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

// TestContextReminderOnComplete tests that completing a task after requesting empty context shows a reminder
func TestContextReminderOnComplete(t *testing.T) {
	database := testDB(t)

	// Create a project without context
	if err := database.CreateProject(&db.Project{Name: "reminder-test-project", Path: "/tmp/reminder-test"}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task
	task := &db.Task{
		Title:   "Reminder Test Task",
		Status:  db.StatusProcessing,
		Project: "reminder-test-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Step 1: Request context (will be empty, setting contextWasEmpty flag)
	getContextReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "taskyou_get_project_context",
			"arguments": map[string]interface{}{},
		},
	}
	reqBytes, _ := json.Marshal(getContextReq)
	reqBytes = append(reqBytes, '\n')

	// Also prepare the complete request
	completeReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_complete",
			"arguments": map[string]interface{}{
				"summary": "Test completed without saving context",
			},
		},
	}
	completeBytes, _ := json.Marshal(completeReq)
	completeBytes = append(completeBytes, '\n')

	// Combine both requests
	combinedInput := string(reqBytes) + string(completeBytes)

	server, output := testServer(database, task.ID, combinedInput)
	server.Run()

	// Parse responses (there will be two)
	responses := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(responses) < 2 {
		t.Fatalf("expected 2 responses, got %d: %s", len(responses), output.String())
	}

	// Parse the complete response (second one)
	var completeResp jsonRPCResponse
	if err := json.Unmarshal([]byte(responses[1]), &completeResp); err != nil {
		t.Fatalf("failed to parse complete response: %v", err)
	}

	if completeResp.Error != nil {
		t.Fatalf("unexpected error: %s", completeResp.Error.Message)
	}

	result := completeResp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	// Should contain the reminder since context was requested but never saved
	if !strings.Contains(text, "REMINDER") {
		t.Errorf("expected reminder in completion response, got: %s", text)
	}
	if !strings.Contains(text, "taskyou_set_project_context") {
		t.Errorf("expected reminder to mention taskyou_set_project_context, got: %s", text)
	}

	t.Log("Context reminder on completion works correctly!")
}

// TestNoReminderWhenContextSaved tests that no reminder appears when context was saved
func TestNoReminderWhenContextSaved(t *testing.T) {
	database := testDB(t)

	// Create a project
	if err := database.CreateProject(&db.Project{Name: "no-reminder-project", Path: "/tmp/no-reminder"}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task
	task := &db.Task{
		Title:   "No Reminder Task",
		Status:  db.StatusProcessing,
		Project: "no-reminder-project",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Request context (empty), then set context, then complete
	getContextReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "taskyou_get_project_context",
			"arguments": map[string]interface{}{},
		},
	}

	setContextReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_set_project_context",
			"arguments": map[string]interface{}{
				"context": "This is a test project context.",
			},
		},
	}

	completeReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_complete",
			"arguments": map[string]interface{}{
				"summary": "Test completed after saving context",
			},
		},
	}

	reqBytes1, _ := json.Marshal(getContextReq)
	reqBytes2, _ := json.Marshal(setContextReq)
	reqBytes3, _ := json.Marshal(completeReq)

	combinedInput := string(reqBytes1) + "\n" + string(reqBytes2) + "\n" + string(reqBytes3) + "\n"

	server, output := testServer(database, task.ID, combinedInput)
	server.Run()

	// Parse responses
	responses := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(responses) < 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	// Parse the complete response (third one)
	var completeResp jsonRPCResponse
	if err := json.Unmarshal([]byte(responses[2]), &completeResp); err != nil {
		t.Fatalf("failed to parse complete response: %v", err)
	}

	if completeResp.Error != nil {
		t.Fatalf("unexpected error: %s", completeResp.Error.Message)
	}

	result := completeResp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	// Should NOT contain reminder since context was saved
	if strings.Contains(text, "REMINDER") {
		t.Errorf("should not have reminder when context was saved, got: %s", text)
	}

	t.Log("No reminder when context saved works correctly!")
}

// TestSpotlightStatus tests the spotlight status action when inactive
func TestSpotlightStatus(t *testing.T) {
	database := testDB(t)

	// Create temp directories for worktree and main repo
	worktreeDir := t.TempDir()
	mainRepoDir := t.TempDir()

	// Initialize git repos
	runGit(t, mainRepoDir, "init")
	runGit(t, mainRepoDir, "config", "user.email", "test@test.com")
	runGit(t, mainRepoDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(mainRepoDir, "README.md"), []byte("# Test"), 0644)
	runGit(t, mainRepoDir, "add", ".")
	runGit(t, mainRepoDir, "commit", "-m", "initial")

	runGit(t, worktreeDir, "init")
	runGit(t, worktreeDir, "config", "user.email", "test@test.com")
	runGit(t, worktreeDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(worktreeDir, "README.md"), []byte("# Test"), 0644)
	runGit(t, worktreeDir, "add", ".")
	runGit(t, worktreeDir, "commit", "-m", "initial")

	// Create a project
	if err := database.CreateProject(&db.Project{Name: "spotlight-test", Path: mainRepoDir}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a task
	task := &db.Task{
		Title:   "Spotlight Test Task",
		Status:  db.StatusProcessing,
		Project: "spotlight-test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Set the worktree path (normally done by executor)
	task.WorktreePath = worktreeDir
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("failed to update task with worktree: %v", err)
	}

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "status",
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

	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "INACTIVE") {
		t.Errorf("expected spotlight status to be INACTIVE, got: %s", text)
	}
}

// TestSpotlightStartStopFlow tests the full start/stop flow
func TestSpotlightStartStopFlow(t *testing.T) {
	database := testDB(t)

	// Create temp directories for worktree and main repo
	worktreeDir := t.TempDir()
	mainRepoDir := t.TempDir()

	// Initialize main repo
	runGit(t, mainRepoDir, "init")
	runGit(t, mainRepoDir, "config", "user.email", "test@test.com")
	runGit(t, mainRepoDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(mainRepoDir, "README.md"), []byte("# Main Repo"), 0644)
	runGit(t, mainRepoDir, "add", ".")
	runGit(t, mainRepoDir, "commit", "-m", "initial")

	// Initialize worktree with same structure
	runGit(t, worktreeDir, "init")
	runGit(t, worktreeDir, "config", "user.email", "test@test.com")
	runGit(t, worktreeDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(worktreeDir, "README.md"), []byte("# Worktree Changes"), 0644)
	os.WriteFile(filepath.Join(worktreeDir, "newfile.txt"), []byte("new content"), 0644)
	runGit(t, worktreeDir, "add", "README.md")
	runGit(t, worktreeDir, "commit", "-m", "initial")

	// Create project
	if err := database.CreateProject(&db.Project{Name: "spotlight-flow-test", Path: mainRepoDir}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create task
	task := &db.Task{
		Title:   "Spotlight Flow Test",
		Status:  db.StatusProcessing,
		Project: "spotlight-flow-test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Set the worktree path (normally done by executor)
	task.WorktreePath = worktreeDir
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("failed to update task with worktree: %v", err)
	}

	// Step 1: Start spotlight
	startReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "start",
			},
		},
	}
	reqBytes, _ := json.Marshal(startReq)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse start response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("start failed: %s", resp.Error.Message)
	}

	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "enabled") {
		t.Errorf("expected spotlight to be enabled, got: %s", text)
	}

	// Verify state file was created
	stateFile := filepath.Join(worktreeDir, ".spotlight-active")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Error("spotlight state file not created")
	}

	// Verify files were synced
	mainReadme, err := os.ReadFile(filepath.Join(mainRepoDir, "README.md"))
	if err != nil {
		t.Fatalf("failed to read main repo README: %v", err)
	}
	if string(mainReadme) != "# Worktree Changes" {
		t.Errorf("README not synced, got: %s", string(mainReadme))
	}

	// Step 2: Check status (should be active)
	statusReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "status",
			},
		},
	}
	reqBytes, _ = json.Marshal(statusReq)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse status response: %v", err)
	}

	result = resp.Result.(map[string]interface{})
	content = result["content"].([]interface{})
	text = content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "ACTIVE") {
		t.Errorf("expected spotlight to be ACTIVE, got: %s", text)
	}

	// Step 3: Stop spotlight
	stopReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "stop",
			},
		},
	}
	reqBytes, _ = json.Marshal(stopReq)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task.ID, string(reqBytes))
	server.Run()

	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse stop response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("stop failed: %s", resp.Error.Message)
	}

	result = resp.Result.(map[string]interface{})
	content = result["content"].([]interface{})
	text = content[0].(map[string]interface{})["text"].(string)

	if !strings.Contains(text, "disabled") {
		t.Errorf("expected spotlight to be disabled, got: %s", text)
	}

	// Verify state file was removed
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Error("spotlight state file should be removed")
	}

	// Verify main repo was restored
	mainReadme, err = os.ReadFile(filepath.Join(mainRepoDir, "README.md"))
	if err != nil {
		t.Fatalf("failed to read main repo README: %v", err)
	}
	if string(mainReadme) != "# Main Repo" {
		t.Errorf("README not restored, got: %s", string(mainReadme))
	}
}

// TestSpotlightRequiresWorktree tests that spotlight fails without worktree
func TestSpotlightRequiresWorktree(t *testing.T) {
	database := testDB(t)
	task := createTestTask(t, database)

	// Task has no worktree path set
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "status",
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

	if resp.Error == nil {
		t.Fatal("expected error for task without worktree")
	}

	if !strings.Contains(resp.Error.Message, "worktree") {
		t.Errorf("expected error about worktree, got: %s", resp.Error.Message)
	}
}

// TestSpotlightSync tests the sync action
func TestSpotlightSync(t *testing.T) {
	database := testDB(t)

	// Create temp directories
	worktreeDir := t.TempDir()
	mainRepoDir := t.TempDir()

	// Initialize repos
	runGit(t, mainRepoDir, "init")
	runGit(t, mainRepoDir, "config", "user.email", "test@test.com")
	runGit(t, mainRepoDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(mainRepoDir, "file.txt"), []byte("original"), 0644)
	runGit(t, mainRepoDir, "add", ".")
	runGit(t, mainRepoDir, "commit", "-m", "initial")

	runGit(t, worktreeDir, "init")
	runGit(t, worktreeDir, "config", "user.email", "test@test.com")
	runGit(t, worktreeDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(worktreeDir, "file.txt"), []byte("modified"), 0644)
	runGit(t, worktreeDir, "add", ".")
	runGit(t, worktreeDir, "commit", "-m", "initial")

	// Create project
	if err := database.CreateProject(&db.Project{Name: "spotlight-sync-test", Path: mainRepoDir}); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create task
	task := &db.Task{
		Title:   "Spotlight Sync Test",
		Status:  db.StatusProcessing,
		Project: "spotlight-sync-test",
	}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Set the worktree path (normally done by executor)
	task.WorktreePath = worktreeDir
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("failed to update task with worktree: %v", err)
	}

	// First start spotlight
	startReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "start",
			},
		},
	}
	reqBytes, _ := json.Marshal(startReq)
	reqBytes = append(reqBytes, '\n')

	server, output := testServer(database, task.ID, string(reqBytes))
	server.Run()
	_ = output // first response not needed

	// Now modify the worktree file
	os.WriteFile(filepath.Join(worktreeDir, "file.txt"), []byte("modified again"), 0644)

	// Sync changes
	syncReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "taskyou_spotlight",
			"arguments": map[string]interface{}{
				"action": "sync",
			},
		},
	}
	reqBytes, _ = json.Marshal(syncReq)
	reqBytes = append(reqBytes, '\n')

	server, output = testServer(database, task.ID, string(reqBytes))
	server.Run()

	var resp jsonRPCResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("sync failed: %s", resp.Error.Message)
	}

	// Verify file was synced
	mainContent, err := os.ReadFile(filepath.Join(mainRepoDir, "file.txt"))
	if err != nil {
		t.Fatalf("failed to read main repo file: %v", err)
	}
	if string(mainContent) != "modified again" {
		t.Errorf("file not synced, got: %s", string(mainContent))
	}
}

// runGit is a helper to run git commands in tests
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}

package webapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	tmpDir, err := os.MkdirTemp("", "webapi-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	server := New(Config{
		Addr: ":0",
		DB:   database,
	})

	// Start the WebSocket hub in a goroutine
	go server.wsHub.Run()

	cleanup := func() {
		database.Close()
		os.RemoveAll(tmpDir)
	}

	return server, cleanup
}

func TestListTasks(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a task first
	task := &db.Task{
		Title:   "Test Task",
		Body:    "Test body",
		Type:    "code",
		Project: "personal",
		Status:  db.StatusBacklog,
	}
	if err := server.db.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Test list tasks
	req := httptest.NewRequest("GET", "/tasks", nil)
	w := httptest.NewRecorder()

	server.handleListTasks(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var tasks []*TaskResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Test Task" {
		t.Errorf("expected title 'Test Task', got '%s'", tasks[0].Title)
	}
}

func TestCreateTask(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Create task via API
	createReq := CreateTaskRequest{
		Title:   "New Task",
		Body:    "Task body",
		Type:    "writing",
		Project: "personal",
	}
	reqBody, _ := json.Marshal(createReq)

	req := httptest.NewRequest("POST", "/tasks", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateTask(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 201, got %d: %s", resp.StatusCode, string(body))
	}

	var task TaskResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if task.Title != "New Task" {
		t.Errorf("expected title 'New Task', got '%s'", task.Title)
	}
	if task.Status != db.StatusBacklog {
		t.Errorf("expected status 'backlog', got '%s'", task.Status)
	}
}

func TestCreateTaskRequiresTitle(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Create task without title
	createReq := CreateTaskRequest{
		Body: "Task body",
	}
	reqBody, _ := json.Marshal(createReq)

	req := httptest.NewRequest("POST", "/tasks", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateTask(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestListProjects(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/projects", nil)
	w := httptest.NewRecorder()

	server.handleListProjects(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var projects []*ProjectResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &projects); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have at least the default 'personal' project
	found := false
	for _, p := range projects {
		if p.Name == "personal" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'personal' project to exist")
	}
}

func TestCreateProject(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	createReq := CreateProjectRequest{
		Name:  "test-project",
		Path:  "/tmp/test-project",
		Color: "#FF0000",
	}
	reqBody, _ := json.Marshal(createReq)

	req := httptest.NewRequest("POST", "/projects", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateProject(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 201, got %d: %s", resp.StatusCode, string(body))
	}

	var project ProjectResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &project); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if project.Name != "test-project" {
		t.Errorf("expected name 'test-project', got '%s'", project.Name)
	}
	if project.Color != "#FF0000" {
		t.Errorf("expected color '#FF0000', got '%s'", project.Color)
	}
}

func TestGetSettings(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Set a setting first
	server.db.SetSetting("theme", "dark")

	req := httptest.NewRequest("GET", "/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetSettings(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var settings map[string]string
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if settings["theme"] != "dark" {
		t.Errorf("expected theme 'dark', got '%s'", settings["theme"])
	}
}

func TestUpdateSettings(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	settings := map[string]string{
		"theme":      "light",
		"pane_height": "50",
	}
	reqBody, _ := json.Marshal(settings)

	req := httptest.NewRequest("PUT", "/settings", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSettings(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify settings were saved
	theme, _ := server.db.GetSetting("theme")
	if theme != "light" {
		t.Errorf("expected theme 'light', got '%s'", theme)
	}

	paneHeight, _ := server.db.GetSetting("pane_height")
	if paneHeight != "50" {
		t.Errorf("expected pane_height '50', got '%s'", paneHeight)
	}
}

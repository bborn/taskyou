package webapi

import (
	"net/http"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// TaskResponse represents a task in JSON responses.
type TaskResponse struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	Body            string     `json:"body"`
	Status          string     `json:"status"`
	Type            string     `json:"type"`
	Project         string     `json:"project"`
	WorktreePath    string     `json:"worktree_path,omitempty"`
	BranchName      string     `json:"branch_name,omitempty"`
	Port            int        `json:"port,omitempty"`
	PRUrl           string     `json:"pr_url,omitempty"`
	PRNumber        int        `json:"pr_number,omitempty"`
	DangerousMode   bool       `json:"dangerous_mode"`
	ScheduledAt     *time.Time `json:"scheduled_at,omitempty"`
	Recurrence      string     `json:"recurrence,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

func taskToResponse(t *db.Task) *TaskResponse {
	resp := &TaskResponse{
		ID:            t.ID,
		Title:         t.Title,
		Body:          t.Body,
		Status:        t.Status,
		Type:          t.Type,
		Project:       t.Project,
		WorktreePath:  t.WorktreePath,
		BranchName:    t.BranchName,
		Port:          t.Port,
		PRUrl:         t.PRURL,
		PRNumber:      t.PRNumber,
		DangerousMode: t.DangerousMode,
		Recurrence:    t.Recurrence,
		CreatedAt:     t.CreatedAt.Time,
		UpdatedAt:     t.UpdatedAt.Time,
	}

	if t.ScheduledAt != nil && !t.ScheduledAt.Time.IsZero() {
		resp.ScheduledAt = &t.ScheduledAt.Time
	}
	if t.LastRunAt != nil && !t.LastRunAt.Time.IsZero() {
		resp.LastRunAt = &t.LastRunAt.Time
	}
	if t.StartedAt != nil && !t.StartedAt.Time.IsZero() {
		resp.StartedAt = &t.StartedAt.Time
	}
	if t.CompletedAt != nil && !t.CompletedAt.Time.IsZero() {
		resp.CompletedAt = &t.CompletedAt.Time
	}

	return resp
}

// handleListTasks handles GET /tasks
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	opts := db.ListTasksOptions{}

	if status := r.URL.Query().Get("status"); status != "" {
		opts.Status = status
	}
	if project := r.URL.Query().Get("project"); project != "" {
		opts.Project = project
	}
	if taskType := r.URL.Query().Get("type"); taskType != "" {
		opts.Type = taskType
	}
	if r.URL.Query().Get("all") == "true" {
		opts.IncludeClosed = true
	}

	tasks, err := s.db.ListTasks(opts)
	if err != nil {
		s.logger.Error("list tasks failed", "error", err)
		jsonError(w, "Failed to list tasks", http.StatusInternalServerError)
		return
	}

	responses := make([]*TaskResponse, len(tasks))
	for i, t := range tasks {
		responses[i] = taskToResponse(t)
	}

	jsonResponse(w, responses, http.StatusOK)
}

// CreateTaskRequest represents a request to create a task.
type CreateTaskRequest struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Type        string `json:"type"`
	Project     string `json:"project"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
	Recurrence  string `json:"recurrence,omitempty"`
}

// handleCreateTask handles POST /tasks
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		jsonError(w, "Title is required", http.StatusBadRequest)
		return
	}

	// Set defaults
	if req.Project == "" {
		req.Project = "personal"
	}
	if req.Type == "" {
		req.Type = "code"
	}

	// Build task struct
	task := &db.Task{
		Title:      req.Title,
		Body:       req.Body,
		Type:       req.Type,
		Project:    req.Project,
		Status:     db.StatusBacklog,
		Recurrence: req.Recurrence,
	}

	// Handle scheduling
	if req.ScheduledAt != "" {
		scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err == nil {
			task.ScheduledAt = &db.LocalTime{Time: scheduledAt}
		}
	}

	if err := s.db.CreateTask(task); err != nil {
		s.logger.Error("create task failed", "error", err)
		jsonError(w, "Failed to create task", http.StatusInternalServerError)
		return
	}

	// Reload task to get all fields
	task, _ = s.db.GetTask(task.ID)

	// Broadcast update
	s.BroadcastTaskUpdate(task)

	jsonResponse(w, taskToResponse(task), http.StatusCreated)
}

// handleGetTask handles GET /tasks/{id}
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil {
		s.logger.Error("get task failed", "error", err)
		jsonError(w, "Failed to get task", http.StatusInternalServerError)
		return
	}

	if task == nil {
		jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, taskToResponse(task), http.StatusOK)
}

// UpdateTaskRequest represents a request to update a task.
type UpdateTaskRequest struct {
	Title       *string `json:"title,omitempty"`
	Body        *string `json:"body,omitempty"`
	Status      *string `json:"status,omitempty"`
	Type        *string `json:"type,omitempty"`
	Project     *string `json:"project,omitempty"`
	ScheduledAt *string `json:"scheduled_at,omitempty"`
	Recurrence  *string `json:"recurrence,omitempty"`
}

// handleUpdateTask handles PUT /tasks/{id}
func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	var req UpdateTaskRequest
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	// Apply updates
	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Body != nil {
		task.Body = *req.Body
	}
	if req.Status != nil {
		task.Status = *req.Status
	}
	if req.Type != nil {
		task.Type = *req.Type
	}
	if req.Project != nil {
		task.Project = *req.Project
	}

	if err := s.db.UpdateTask(task); err != nil {
		s.logger.Error("update task failed", "error", err)
		jsonError(w, "Failed to update task", http.StatusInternalServerError)
		return
	}

	// Handle scheduling
	if req.ScheduledAt != nil || req.Recurrence != nil {
		var scheduledAt *db.LocalTime
		var recurrence string

		if req.ScheduledAt != nil {
			if *req.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, *req.ScheduledAt)
				if err == nil {
					scheduledAt = &db.LocalTime{Time: t}
				}
			}
		}
		if req.Recurrence != nil {
			recurrence = *req.Recurrence
		}

		s.db.UpdateTaskSchedule(id, scheduledAt, recurrence, nil)
	}

	// Reload task
	task, _ = s.db.GetTask(id)

	// Broadcast update
	s.BroadcastTaskUpdate(task)

	jsonResponse(w, taskToResponse(task), http.StatusOK)
}

// handleDeleteTask handles DELETE /tasks/{id}
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteTask(id); err != nil {
		s.logger.Error("delete task failed", "error", err)
		jsonError(w, "Failed to delete task", http.StatusInternalServerError)
		return
	}

	// Broadcast deletion
	s.wsHub.Broadcast(Message{
		Type: "task_deleted",
		Data: map[string]int64{"id": id},
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleQueueTask handles POST /tasks/{id}/queue
func (s *Server) handleQueueTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	// Update status to queued
	if err := s.db.UpdateTaskStatus(id, "queued"); err != nil {
		s.logger.Error("queue task failed", "error", err)
		jsonError(w, "Failed to queue task", http.StatusInternalServerError)
		return
	}

	// Reload task
	task, _ = s.db.GetTask(id)

	// Broadcast update
	s.BroadcastTaskUpdate(task)

	jsonResponse(w, taskToResponse(task), http.StatusOK)
}

// RetryTaskRequest represents a request to retry a task.
type RetryTaskRequest struct {
	Feedback string `json:"feedback,omitempty"`
}

// handleRetryTask handles POST /tasks/{id}/retry
func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	var req RetryTaskRequest
	parseJSON(r, &req) // Feedback is optional

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	// If feedback provided, add to body
	if req.Feedback != "" {
		newBody := task.Body
		if newBody != "" {
			newBody += "\n\n---\nFeedback:\n"
		}
		newBody += req.Feedback
		task.Body = newBody
		s.db.UpdateTask(task)
	}

	// Update status to queued
	if err := s.db.UpdateTaskStatus(id, "queued"); err != nil {
		s.logger.Error("retry task failed", "error", err)
		jsonError(w, "Failed to retry task", http.StatusInternalServerError)
		return
	}

	// Reload task
	task, _ = s.db.GetTask(id)

	// Broadcast update
	s.BroadcastTaskUpdate(task)

	jsonResponse(w, taskToResponse(task), http.StatusOK)
}

// handleCloseTask handles POST /tasks/{id}/close
func (s *Server) handleCloseTask(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	// Update status to done
	if err := s.db.UpdateTaskStatus(id, "done"); err != nil {
		s.logger.Error("close task failed", "error", err)
		jsonError(w, "Failed to close task", http.StatusInternalServerError)
		return
	}

	// Reload task
	task, _ = s.db.GetTask(id)

	// Broadcast update
	s.BroadcastTaskUpdate(task)

	jsonResponse(w, taskToResponse(task), http.StatusOK)
}

// handleGetTaskLogs handles GET /tasks/{id}/logs
func (s *Server) handleGetTaskLogs(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	// Parse limit parameter
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := getIDParam(r); err == nil && parsed > 0 {
			limit = int(parsed)
		}
	}

	logs, err := s.db.GetTaskLogs(id, limit)
	if err != nil {
		s.logger.Error("get task logs failed", "error", err)
		jsonError(w, "Failed to get task logs", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, logs, http.StatusOK)
}

package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/bborn/workflow/internal/completion"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// --- JSON helpers ---

// apiTime renders a timestamp for API responses. Times are stored/handled in
// local time internally (db.LocalTime); convert to UTC so the trailing Z is
// actually true — clients parse these as UTC.
func apiTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *Server) requireTask(w http.ResponseWriter, r *http.Request) (*db.Task, bool) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return nil, false
	}
	task, err := s.db.GetTask(id)
	if err != nil {
		jsonErr(w, "database error", http.StatusInternalServerError)
		return nil, false
	}
	if task == nil {
		jsonErr(w, "task not found", http.StatusNotFound)
		return nil, false
	}
	return task, true
}

// --- Board ---

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	opts := db.ListTasksOptions{IncludeClosed: true, Limit: 500}
	if v := r.URL.Query().Get("project"); v != "" {
		opts.Project = v
	}

	tasks, err := s.db.ListTasks(opts)
	if err != nil {
		jsonErr(w, "failed to load tasks", http.StatusInternalServerError)
		return
	}

	jsonOK(w, BuildBoardSnapshot(tasks, limit))
}

// --- Tasks CRUD ---

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	opts := db.ListTasksOptions{
		Status:        q.Get("status"),
		Type:          q.Get("type"),
		Project:       q.Get("project"),
		Limit:         limit,
		Offset:        offset,
		IncludeClosed: q.Get("all") == "true",
	}

	tasks, err := s.db.ListTasks(opts)
	if err != nil {
		jsonErr(w, "failed to list tasks", http.StatusInternalServerError)
		return
	}

	result := make([]*taskJSON, len(tasks))
	for i, t := range tasks {
		result[i] = toTaskJSON(t)
	}
	jsonOK(w, result)
}

type createTaskRequest struct {
	Title          string `json:"title"`
	Body           string `json:"body"`
	Type           string `json:"type"`
	Project        string `json:"project"`
	Executor       string `json:"executor"`
	Execute        bool   `json:"execute"`
	Tags           string `json:"tags"`
	Pinned         bool   `json:"pinned"`
	PermissionMode string `json:"permission_mode"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == "" && req.Body == "" {
		jsonErr(w, "title or body required", http.StatusBadRequest)
		return
	}

	// Validate type if provided
	if req.Type != "" {
		tt, err := s.db.GetTaskTypeByName(req.Type)
		if err != nil || tt == nil {
			jsonErr(w, "invalid task type", http.StatusBadRequest)
			return
		}
	}

	status := db.StatusBacklog
	if req.Execute {
		status = db.StatusQueued
	}

	// Default title from body if empty
	title := req.Title
	if title == "" && req.Body != "" {
		title = req.Body
		if len(title) > 50 {
			title = title[:50] + "..."
		}
	}

	task := &db.Task{
		Title:          title,
		Body:           req.Body,
		Type:           req.Type,
		Project:        req.Project,
		Executor:       req.Executor,
		Status:         status,
		Tags:           req.Tags,
		Pinned:         req.Pinned,
		PermissionMode: db.NormalizePermissionMode(req.PermissionMode),
	}

	if err := s.db.CreateTask(task); err != nil {
		jsonErr(w, "failed to create task: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, toTaskJSON(task))
}

func (s *Server) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	logs, err := s.db.GetTaskLogs(task.ID, 100)
	if err != nil {
		jsonErr(w, "failed to load logs", http.StatusInternalServerError)
		return
	}

	// Reverse logs to chronological order (DB returns DESC)
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	jsonOK(w, map[string]interface{}{
		"task": toTaskJSON(task),
		"logs": toLogJSONSlice(logs),
	})
}

type updateTaskRequest struct {
	Title          *string `json:"title"`
	Body           *string `json:"body"`
	Type           *string `json:"type"`
	Project        *string `json:"project"`
	Executor       *string `json:"executor"`
	Tags           *string `json:"tags"`
	Pinned         *bool   `json:"pinned"`
	PermissionMode *string `json:"permission_mode"`
	EffortLevel    *string `json:"effort_level"`
	Model          *string `json:"model"`
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Body != nil {
		task.Body = *req.Body
	}
	if req.Type != nil {
		if tt, err := s.db.GetTaskTypeByName(*req.Type); err != nil || tt == nil {
			jsonErr(w, "invalid task type", http.StatusBadRequest)
			return
		}
		task.Type = *req.Type
	}
	if req.Project != nil {
		task.Project = *req.Project
	}
	if req.Executor != nil {
		task.Executor = *req.Executor
	}
	if req.Tags != nil {
		task.Tags = *req.Tags
	}
	if req.Pinned != nil {
		task.Pinned = *req.Pinned
	}
	if req.PermissionMode != nil {
		task.PermissionMode = db.NormalizePermissionMode(*req.PermissionMode)
	}
	if req.EffortLevel != nil {
		task.EffortLevel = *req.EffortLevel
	}
	if req.Model != nil {
		task.Model = *req.Model
	}

	if err := s.db.UpdateTask(task); err != nil {
		jsonErr(w, "failed to update task", http.StatusInternalServerError)
		return
	}

	jsonOK(w, toTaskJSON(task))
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		jsonErr(w, "task not found", http.StatusNotFound)
		return
	}

	// Soft-delete: trash the task (recoverable) rather than destroying it. The
	// daemon sweep hard-deletes it after the retention window.
	if err := s.db.SoftDeleteTask(id); err != nil {
		jsonErr(w, "failed to delete task", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// --- Task actions ---

type moveRequest struct {
	Project string `json:"project"`
}

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" {
		jsonErr(w, "project required", http.StatusBadRequest)
		return
	}

	project, err := s.db.GetProjectByName(req.Project)
	if err != nil || project == nil {
		jsonErr(w, "project not found", http.StatusNotFound)
		return
	}

	task.Project = req.Project
	if err := s.db.UpdateTask(task); err != nil {
		jsonErr(w, "failed to move task", http.StatusInternalServerError)
		return
	}

	jsonOK(w, toTaskJSON(task))
}

type statusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	var req statusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		jsonErr(w, "status required", http.StatusBadRequest)
		return
	}

	valid := map[string]bool{
		db.StatusBacklog: true, db.StatusQueued: true, db.StatusProcessing: true,
		db.StatusBlocked: true, db.StatusDone: true, db.StatusArchived: true,
	}
	if !valid[req.Status] {
		jsonErr(w, "invalid status", http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateTaskStatus(id, req.Status); err != nil {
		jsonErr(w, "failed to update status", http.StatusInternalServerError)
		return
	}
	// The board is a human surface, so this write is allowed even with an open PR
	// — dragging a card to Done is a deliberate human decision. It is still
	// recorded, so "who marked this done?" stays answerable.
	if req.Status == db.StatusDone {
		completion.RecordStatusWrite(s.db, id, req.Status, "the web board (PATCH status)")
	}

	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleExecuteTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	if task.Status == db.StatusQueued || task.Status == db.StatusProcessing {
		jsonErr(w, "task is already queued or processing", http.StatusConflict)
		return
	}

	if err := s.db.UpdateTaskStatus(task.ID, db.StatusQueued); err != nil {
		jsonErr(w, "failed to queue task", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleCloseTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	if task.Status == db.StatusDone {
		jsonOK(w, map[string]string{"message": "task already done"})
		return
	}

	if err := s.db.UpdateTaskStatus(task.ID, db.StatusDone); err != nil {
		jsonErr(w, "failed to close task", http.StatusInternalServerError)
		return
	}
	completion.RecordStatusWrite(s.db, task.ID, db.StatusDone, "the web board (close)")

	jsonOK(w, map[string]bool{"ok": true})
}

type retryRequest struct {
	Feedback string `json:"feedback"`
}

func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req retryRequest
	json.NewDecoder(r.Body).Decode(&req) // optional body

	if err := s.db.RetryTask(task.ID, req.Feedback); err != nil {
		jsonErr(w, "failed to retry task", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

type pinRequest struct {
	Pinned *bool `json:"pinned"`
	Toggle bool  `json:"toggle"`
}

func (s *Server) handlePinTask(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req pinRequest
	json.NewDecoder(r.Body).Decode(&req) // optional body

	newVal := true
	if req.Toggle {
		newVal = !task.Pinned
	} else if req.Pinned != nil {
		newVal = *req.Pinned
	}

	if err := s.db.UpdateTaskPinned(task.ID, newVal); err != nil {
		jsonErr(w, "failed to update pin", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"pinned": newVal})
}

type inputRequest struct {
	Message string `json:"message"`
	Enter   bool   `json:"enter"`
	Key     string `json:"key"`
}

func (s *Server) handleTaskInput(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	paneID := task.ClaudePaneID
	if paneID == "" {
		jsonErr(w, "task has no executor pane", http.StatusBadRequest)
		return
	}

	var req inputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if s.runner == nil {
		jsonErr(w, "command runner not configured", http.StatusInternalServerError)
		return
	}

	if req.Key != "" {
		if err := s.runner.Run("tmux", "send-keys", "-t", paneID, req.Key); err != nil {
			jsonErr(w, "failed to send key", http.StatusInternalServerError)
			return
		}
	}

	if req.Message != "" {
		if err := s.runner.Run("tmux", "send-keys", "-t", paneID, req.Message, "Enter"); err != nil {
			jsonErr(w, "failed to send input", http.StatusInternalServerError)
			return
		}
	} else if req.Enter {
		if err := s.runner.Run("tmux", "send-keys", "-t", paneID, "Enter"); err != nil {
			jsonErr(w, "failed to send enter", http.StatusInternalServerError)
			return
		}
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// --- Task logs ---

func (s *Server) handleTaskOutput(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	paneID := task.ClaudePaneID
	if paneID == "" {
		jsonErr(w, "task has no executor pane", http.StatusBadRequest)
		return
	}

	if s.runner == nil {
		jsonErr(w, "command runner not configured", http.StatusInternalServerError)
		return
	}

	lines := "200"
	if v := r.URL.Query().Get("lines"); v != "" {
		lines = v
	}

	// -J joins wrapped lines so clients can reflow to their own width.
	output, err := s.runner.Output("tmux", "capture-pane", "-t", paneID, "-p", "-J", "-S", "-"+lines)
	if err != nil {
		jsonErr(w, "executor pane not available", http.StatusGone)
		return
	}

	jsonOK(w, map[string]string{"output": string(output)})
}

func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	logs, err := s.db.GetTaskLogs(id, limit)
	if err != nil {
		jsonErr(w, "failed to load logs", http.StatusInternalServerError)
		return
	}

	// Reverse to chronological
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	jsonOK(w, toLogJSONSlice(logs))
}

// --- Dependencies ---

func (s *Server) handleGetDeps(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	blockers, blockedBy, err := s.db.GetAllDependencies(id)
	if err != nil {
		jsonErr(w, "failed to get dependencies", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]interface{}{
		"blockers":   toTaskJSONSlice(blockers),
		"blocked_by": toTaskJSONSlice(blockedBy),
	})
}

type blockRequest struct {
	BlockerID int64 `json:"blocker_id"`
	AutoQueue bool  `json:"auto_queue"`
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	var req blockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BlockerID == 0 {
		jsonErr(w, "blocker_id required", http.StatusBadRequest)
		return
	}

	if err := s.db.AddDependency(req.BlockerID, id, req.AutoQueue); err != nil {
		jsonErr(w, "failed to add dependency: "+err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

type unblockRequest struct {
	BlockerID int64 `json:"blocker_id"`
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid task id", http.StatusBadRequest)
		return
	}

	var req unblockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BlockerID == 0 {
		jsonErr(w, "blocker_id required", http.StatusBadRequest)
		return
	}

	if err := s.db.RemoveDependency(req.BlockerID, id); err != nil {
		jsonErr(w, "dependency not found", http.StatusNotFound)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// --- Projects ---

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjects()
	if err != nil {
		jsonErr(w, "failed to list projects", http.StatusInternalServerError)
		return
	}

	result := make([]map[string]interface{}, len(projects))
	for i, p := range projects {
		count, _ := s.db.CountTasksByProject(p.Name)
		result[i] = projectToMap(p, count)
	}
	jsonOK(w, result)
}

type createProjectRequest struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	Instructions    string `json:"instructions"`
	Color           string `json:"color"`
	Aliases         string `json:"aliases"`
	ClaudeConfigDir string `json:"claude_config_dir"`
	UseWorktrees    *bool  `json:"use_worktrees"`
	PermissionMode  string `json:"default_permission_mode"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Path == "" {
		jsonErr(w, "name and path required", http.StatusBadRequest)
		return
	}

	existing, _ := s.db.GetProjectByName(req.Name)
	if existing != nil {
		jsonErr(w, "project already exists", http.StatusConflict)
		return
	}

	useWorktrees := true
	if req.UseWorktrees != nil {
		useWorktrees = *req.UseWorktrees
	}

	p := &db.Project{
		Name:                  req.Name,
		Path:                  req.Path,
		Instructions:          req.Instructions,
		Color:                 req.Color,
		Aliases:               req.Aliases,
		ClaudeConfigDir:       req.ClaudeConfigDir,
		UseWorktrees:          useWorktrees,
		DefaultPermissionMode: db.NormalizePermissionMode(req.PermissionMode),
	}

	if err := s.db.CreateProject(p); err != nil {
		jsonErr(w, "failed to create project: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, projectToMap(p, 0))
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := s.db.GetProjectByName(name)
	if err != nil || p == nil {
		jsonErr(w, "project not found", http.StatusNotFound)
		return
	}

	count, _ := s.db.CountTasksByProject(p.Name)
	ctx, _ := s.db.GetProjectContext(p.Name)

	result := projectToMap(p, count)
	result["context"] = ctx
	jsonOK(w, result)
}

type updateProjectRequest struct {
	Name            *string `json:"name"`
	Path            *string `json:"path"`
	Instructions    *string `json:"instructions"`
	Color           *string `json:"color"`
	Aliases         *string `json:"aliases"`
	ClaudeConfigDir *string `json:"claude_config_dir"`
	UseWorktrees    *bool   `json:"use_worktrees"`
	Context         *string `json:"context"`
	PermissionMode  *string `json:"default_permission_mode"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := s.db.GetProjectByName(name)
	if err != nil || p == nil {
		jsonErr(w, "project not found", http.StatusNotFound)
		return
	}

	var req updateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name != nil {
		if existing, _ := s.db.GetProjectByName(*req.Name); existing != nil && existing.ID != p.ID {
			jsonErr(w, "project name already taken", http.StatusConflict)
			return
		}
		p.Name = *req.Name
	}
	if req.Path != nil {
		p.Path = *req.Path
	}
	if req.Instructions != nil {
		p.Instructions = *req.Instructions
	}
	if req.Color != nil {
		p.Color = *req.Color
	}
	if req.Aliases != nil {
		p.Aliases = *req.Aliases
	}
	if req.ClaudeConfigDir != nil {
		p.ClaudeConfigDir = *req.ClaudeConfigDir
	}
	if req.UseWorktrees != nil {
		p.UseWorktrees = *req.UseWorktrees
	}
	if req.PermissionMode != nil {
		p.DefaultPermissionMode = db.NormalizePermissionMode(*req.PermissionMode)
	}

	if err := s.db.UpdateProject(p); err != nil {
		jsonErr(w, "failed to update project", http.StatusInternalServerError)
		return
	}

	if req.Context != nil {
		s.db.SetProjectContext(p.Name, *req.Context)
	}

	count, _ := s.db.CountTasksByProject(p.Name)
	jsonOK(w, projectToMap(p, count))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "personal" {
		jsonErr(w, "cannot delete the personal project", http.StatusForbidden)
		return
	}

	p, err := s.db.GetProjectByName(name)
	if err != nil || p == nil {
		jsonErr(w, "project not found", http.StatusNotFound)
		return
	}

	if err := s.db.DeleteProject(p.ID); err != nil {
		jsonErr(w, "failed to delete project", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// --- Task types ---

func (s *Server) handleListTypes(w http.ResponseWriter, r *http.Request) {
	types, err := s.db.ListTaskTypes()
	if err != nil {
		jsonErr(w, "failed to list types", http.StatusInternalServerError)
		return
	}

	result := make([]map[string]interface{}, len(types))
	for i, t := range types {
		result[i] = typeToMap(t)
	}
	jsonOK(w, result)
}

type createTypeRequest struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Instructions string `json:"instructions"`
	SortOrder    int    `json:"sort_order"`
}

func (s *Server) handleCreateType(w http.ResponseWriter, r *http.Request) {
	var req createTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonErr(w, "name required", http.StatusBadRequest)
		return
	}

	if existing, _ := s.db.GetTaskTypeByName(req.Name); existing != nil {
		jsonErr(w, "type already exists", http.StatusConflict)
		return
	}

	label := req.Label
	if label == "" {
		label = req.Name
	}
	sortOrder := req.SortOrder
	if sortOrder == 0 {
		sortOrder = 100
	}

	t := &db.TaskType{
		Name:         req.Name,
		Label:        label,
		Instructions: req.Instructions,
		SortOrder:    sortOrder,
	}

	if err := s.db.CreateTaskType(t); err != nil {
		jsonErr(w, "failed to create type: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, typeToMap(t))
}

func (s *Server) handleGetType(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := s.db.GetTaskTypeByName(name)
	if err != nil || t == nil {
		jsonErr(w, "type not found", http.StatusNotFound)
		return
	}
	jsonOK(w, typeToMap(t))
}

type updateTypeRequest struct {
	Name         *string `json:"name"`
	Label        *string `json:"label"`
	Instructions *string `json:"instructions"`
	SortOrder    *int    `json:"sort_order"`
}

func (s *Server) handleUpdateType(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := s.db.GetTaskTypeByName(name)
	if err != nil || t == nil {
		jsonErr(w, "type not found", http.StatusNotFound)
		return
	}

	var req updateTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name != nil {
		if existing, _ := s.db.GetTaskTypeByName(*req.Name); existing != nil && existing.ID != t.ID {
			jsonErr(w, "type name already taken", http.StatusConflict)
			return
		}
		t.Name = *req.Name
	}
	if req.Label != nil {
		t.Label = *req.Label
	}
	if req.Instructions != nil {
		t.Instructions = *req.Instructions
	}
	if req.SortOrder != nil {
		t.SortOrder = *req.SortOrder
	}

	if err := s.db.UpdateTaskType(t); err != nil {
		jsonErr(w, "failed to update type", http.StatusInternalServerError)
		return
	}

	jsonOK(w, typeToMap(t))
}

func (s *Server) handleDeleteType(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := s.db.GetTaskTypeByName(name)
	if err != nil || t == nil {
		jsonErr(w, "type not found", http.StatusNotFound)
		return
	}

	if t.IsBuiltin {
		jsonErr(w, "cannot delete built-in type", http.StatusForbidden)
		return
	}

	if err := s.db.DeleteTaskType(t.ID); err != nil {
		jsonErr(w, "failed to delete type", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// --- Events ---

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	query := `SELECT id, event_type, COALESCE(task_id, 0), COALESCE(message, ''), COALESCE(metadata, '{}'), created_at FROM event_log WHERE 1=1`
	args := []interface{}{}

	if v := q.Get("type"); v != "" {
		query += " AND event_type = ?"
		args = append(args, v)
	}
	if v := q.Get("task_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			query += " AND task_id = ?"
			args = append(args, n)
		}
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		jsonErr(w, "failed to query events", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id, taskID int64
		var eventType, message, metadata, createdAt string
		if err := rows.Scan(&id, &eventType, &taskID, &message, &metadata, &createdAt); err != nil {
			continue
		}
		events = append(events, map[string]interface{}{
			"id":         id,
			"event_type": eventType,
			"task_id":    taskID,
			"message":    message,
			"metadata":   metadata,
			"created_at": createdAt,
		})
	}

	if events == nil {
		events = []map[string]interface{}{}
	}
	jsonOK(w, events)
}

// --- Status ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Count tasks by status
	tasks, err := s.db.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 10000})
	if err != nil {
		jsonErr(w, "database error", http.StatusInternalServerError)
		return
	}

	counts := make(map[string]int)
	for _, t := range tasks {
		counts[t.Status]++
	}

	jsonOK(w, map[string]interface{}{
		"status": "ok",
		"tasks":  counts,
	})
}

// --- JSON conversion helpers ---

type taskJSON struct {
	ID             int64         `json:"id"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	Status         string        `json:"status"`
	Type           string        `json:"type"`
	Project        string        `json:"project"`
	Executor       string        `json:"executor"`
	Pinned         bool          `json:"pinned"`
	Tags           string        `json:"tags"`
	PermissionMode string        `json:"permission_mode"`
	BranchName     string        `json:"branch_name"`
	Port           int           `json:"port,omitempty"`
	WorktreePath   string        `json:"worktree_path,omitempty"`
	HasExecutor    bool          `json:"has_executor"`
	EffortLevel    string        `json:"effort_level,omitempty"`
	Model          string        `json:"model,omitempty"`
	SourceBranch   string        `json:"source_branch,omitempty"`
	DaemonSession  string        `json:"daemon_session,omitempty"`
	TmuxWindowID   string        `json:"tmux_window_id,omitempty"`
	ClaudePaneID   string        `json:"claude_pane_id,omitempty"`
	ShellPaneID    string        `json:"shell_pane_id,omitempty"`
	PRURL          string        `json:"pr_url"`
	PRNumber       int           `json:"pr_number,omitempty"`
	PR             *prStatusJSON `json:"pr,omitempty"`
	Summary        string        `json:"summary,omitempty"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
	StartedAt      string        `json:"started_at,omitempty"`
	CompletedAt    string        `json:"completed_at,omitempty"`
}

// prStatusJSON is the live PR badge payload surfaced on board cards: the PR's
// state plus its CI rollup and diff size. It mirrors the cached github.PRInfo
// persisted in tasks.pr_info_json, lower-cased for the web client. CheckState is
// "" when no checks are known (the batch refresh path doesn't fetch them).
type prStatusJSON struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`       // open | draft | merged | closed
	CheckState string `json:"check_state"` // passing | failing | pending | ""
	Mergeable  string `json:"mergeable"`   // MERGEABLE | CONFLICTING | UNKNOWN
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
}

type logJSON struct {
	ID        int64  `json:"id"`
	LineType  string `json:"line_type"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func toTaskJSON(t *db.Task) *taskJSON {
	tj := &taskJSON{
		ID:             t.ID,
		Title:          t.Title,
		Body:           t.Body,
		Status:         t.Status,
		Type:           t.Type,
		Project:        t.Project,
		Executor:       t.Executor,
		Pinned:         t.Pinned,
		Tags:           t.Tags,
		PermissionMode: t.EffectivePermissionMode(),
		BranchName:     t.BranchName,
		Port:           t.Port,
		WorktreePath:   t.WorktreePath,
		HasExecutor:    t.ClaudePaneID != "",
		EffortLevel:    t.EffortLevel,
		Model:          t.Model,
		SourceBranch:   t.SourceBranch,
		DaemonSession:  t.DaemonSession,
		TmuxWindowID:   t.TmuxWindowID,
		ClaudePaneID:   t.ClaudePaneID,
		ShellPaneID:    t.ShellPaneID,
		PRURL:          t.PRURL,
		PRNumber:       t.PRNumber,
		PR:             toPRStatusJSON(t.PRInfoJSON),
		Summary:        t.Summary,
		CreatedAt:      apiTime(t.CreatedAt.Time),
		UpdatedAt:      apiTime(t.UpdatedAt.Time),
	}
	if t.StartedAt != nil {
		tj.StartedAt = apiTime(t.StartedAt.Time)
	}
	if t.CompletedAt != nil {
		tj.CompletedAt = apiTime(t.CompletedAt.Time)
	}
	return tj
}

// toPRStatusJSON decodes the cached github.PRInfo JSON persisted on a task into
// the web badge payload. Returns nil when there's no associated PR so the field
// is omitted entirely.
func toPRStatusJSON(prInfoJSON string) *prStatusJSON {
	info := github.UnmarshalPRInfo(prInfoJSON)
	if info == nil {
		return nil
	}
	return &prStatusJSON{
		Number:     info.Number,
		URL:        info.URL,
		State:      prStateString(info.State),
		CheckState: checkStateString(info.CheckState),
		Mergeable:  info.Mergeable,
		Additions:  info.Additions,
		Deletions:  info.Deletions,
	}
}

// prStateString maps a github.PRState to the lower-case token the web client uses.
func prStateString(s github.PRState) string {
	switch s {
	case github.PRStateMerged:
		return "merged"
	case github.PRStateClosed:
		return "closed"
	case github.PRStateDraft:
		return "draft"
	case github.PRStateOpen:
		return "open"
	default:
		return ""
	}
}

// checkStateString maps a github.CheckState to a web token; "" means no checks known.
func checkStateString(s github.CheckState) string {
	switch s {
	case github.CheckStatePassing:
		return "passing"
	case github.CheckStateFailing:
		return "failing"
	case github.CheckStatePending:
		return "pending"
	default:
		return ""
	}
}

func toTaskJSONSlice(tasks []*db.Task) []*taskJSON {
	result := make([]*taskJSON, len(tasks))
	for i, t := range tasks {
		result[i] = toTaskJSON(t)
	}
	return result
}

func toLogJSONSlice(logs []*db.TaskLog) []*logJSON {
	result := make([]*logJSON, len(logs))
	for i, l := range logs {
		result[i] = &logJSON{
			ID:        l.ID,
			LineType:  l.LineType,
			Content:   l.Content,
			CreatedAt: apiTime(l.CreatedAt.Time),
		}
	}
	return result
}

func projectToMap(p *db.Project, taskCount int) map[string]interface{} {
	return map[string]interface{}{
		"id":                      p.ID,
		"name":                    p.Name,
		"path":                    p.Path,
		"aliases":                 p.Aliases,
		"instructions":            p.Instructions,
		"color":                   p.Color,
		"claude_config_dir":       p.ClaudeConfigDir,
		"use_worktrees":           p.UseWorktrees,
		"default_permission_mode": p.EffectiveDefaultPermissionMode(),
		"task_count":              taskCount,
	}
}

func typeToMap(t *db.TaskType) map[string]interface{} {
	return map[string]interface{}{
		"id":           t.ID,
		"name":         t.Name,
		"label":        t.Label,
		"instructions": t.Instructions,
		"sort_order":   t.SortOrder,
		"is_builtin":   t.IsBuiltin,
	}
}

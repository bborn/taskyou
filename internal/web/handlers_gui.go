package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/autocomplete"
	"github.com/bborn/workflow/internal/db"
)

// SessionManager is the subset of executor functionality the API needs to
// bootstrap interactive sessions and report executor availability. Implemented
// by *executor.Executor; abstracted for testability.
type SessionManager interface {
	EnsureTaskWindow(ctx context.Context, task *db.Task, sessionID, handoffContext string) (string, bool, error)
	AvailableExecutors() []string
	AllExecutors() []string
}

// maxAttachmentSize bounds decoded attachment uploads (matches generous GUI use
// without letting a single request balloon the SQLite file unboundedly).
const maxAttachmentSize = 32 << 20 // 32 MiB

// --- Settings ---

// isSecretSetting hides credential-like settings from the HTTP API.
func isSecretSetting(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "key") || strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password")
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.db.GetAllSettings()
	if err != nil {
		jsonErr(w, "failed to load settings", http.StatusInternalServerError)
		return
	}
	filtered := make(map[string]string)
	for k, v := range settings {
		if !isSecretSetting(k) {
			filtered[k] = v
		}
	}
	jsonOK(w, filtered)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	for k := range req {
		if isSecretSetting(k) {
			jsonErr(w, "setting cannot be managed over the API: "+k, http.StatusForbidden)
			return
		}
	}
	for k, v := range req {
		if err := s.db.SetSetting(k, v); err != nil {
			jsonErr(w, "failed to save setting "+k, http.StatusInternalServerError)
			return
		}
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// --- Attachments ---

func attachmentToMap(a *db.Attachment) map[string]interface{} {
	return map[string]interface{}{
		"id":         a.ID,
		"task_id":    a.TaskID,
		"filename":   a.Filename,
		"mime_type":  a.MimeType,
		"size":       a.Size,
		"created_at": apiTime(a.CreatedAt.Time),
	}
}

func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	attachments, err := s.db.ListAttachments(task.ID)
	if err != nil {
		jsonErr(w, "failed to list attachments", http.StatusInternalServerError)
		return
	}
	result := make([]map[string]interface{}, len(attachments))
	for i, a := range attachments {
		result[i] = attachmentToMap(a)
	}
	jsonOK(w, result)
}

type addAttachmentRequest struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64-encoded
}

func (s *Server) handleAddAttachment(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req addAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Filename == "" || req.Data == "" {
		jsonErr(w, "filename and data required", http.StatusBadRequest)
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		jsonErr(w, "data must be base64-encoded", http.StatusBadRequest)
		return
	}
	if len(data) == 0 {
		jsonErr(w, "attachment is empty", http.StatusBadRequest)
		return
	}
	if len(data) > maxAttachmentSize {
		jsonErr(w, "attachment too large (max 32MB)", http.StatusRequestEntityTooLarge)
		return
	}

	mimeType := req.MimeType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(req.Filename))
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	attachment, err := s.db.AddAttachment(task.ID, filepath.Base(req.Filename), mimeType, data)
	if err != nil {
		jsonErr(w, "failed to save attachment", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, attachmentToMap(attachment))
}

func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid attachment id", http.StatusBadRequest)
		return
	}
	attachment, err := s.db.GetAttachment(id)
	if err != nil || attachment == nil {
		jsonErr(w, "attachment not found", http.StatusNotFound)
		return
	}

	contentType := attachment.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", attachment.Filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(attachment.Data)))
	w.Write(attachment.Data)
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		jsonErr(w, "invalid attachment id", http.StatusBadRequest)
		return
	}
	attachment, err := s.db.GetAttachment(id)
	if err != nil || attachment == nil {
		jsonErr(w, "attachment not found", http.StatusNotFound)
		return
	}
	if err := s.db.DeleteAttachment(id); err != nil {
		jsonErr(w, "failed to delete attachment", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// --- Executors ---

func (s *Server) handleListExecutors(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		jsonErr(w, "executor manager not configured", http.StatusServiceUnavailable)
		return
	}

	available := make(map[string]bool)
	for _, name := range s.sessions.AvailableExecutors() {
		available[name] = true
	}

	defaultExecutor := db.DefaultExecutor()
	executors := []map[string]interface{}{}
	for _, name := range s.sessions.AllExecutors() {
		executors = append(executors, map[string]interface{}{
			"name":      name,
			"available": available[name],
			"default":   name == defaultExecutor,
		})
	}
	jsonOK(w, executors)
}

// --- Autocomplete (ghost text) ---

type autocompleteRequest struct {
	Input       string   `json:"input"`
	FieldType   string   `json:"field_type"` // "title" or "body"
	Project     string   `json:"project"`
	Context     string   `json:"context"`
	RecentTasks []string `json:"recent_tasks"`
}

// autocompleteService lazily initializes a shared suggestion service so its
// cache survives across requests.
func (s *Server) autocompleteService() *autocomplete.Service {
	s.autocompleteMu.Lock()
	defer s.autocompleteMu.Unlock()
	if s.autocomplete == nil {
		apiKey, _ := s.db.GetSetting("anthropic_api_key")
		s.autocomplete = autocomplete.NewService(apiKey)
	}
	return s.autocomplete
}

func (s *Server) handleAutocomplete(w http.ResponseWriter, r *http.Request) {
	var req autocompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Input == "" {
		jsonErr(w, "input required", http.StatusBadRequest)
		return
	}
	if req.FieldType == "" {
		req.FieldType = "title"
	}

	svc := s.autocompleteService()
	if !svc.IsAvailable() {
		jsonErr(w, "autocomplete unavailable (set ANTHROPIC_API_KEY or the anthropic_api_key setting)", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	suggestion := svc.GetSuggestion(ctx, req.Input, req.FieldType, req.Project, req.Context, req.RecentTasks)
	if suggestion == nil {
		jsonOK(w, map[string]string{"suggestion": "", "full_text": ""})
		return
	}
	jsonOK(w, map[string]string{
		"suggestion": suggestion.Text,
		"full_text":  suggestion.FullText,
	})
}

// --- Latest activity per task (board sub-lines) ---

func (s *Server) handleLatestLogs(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	if idsParam == "" {
		jsonOK(w, map[string]interface{}{})
		return
	}
	var ids []int64
	for _, part := range strings.Split(idsParam, ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	logs, err := s.db.GetLatestLogPerTask(ids)
	if err != nil {
		jsonErr(w, "failed to load logs", http.StatusInternalServerError)
		return
	}
	result := make(map[string]*logJSON, len(logs))
	for taskID, l := range logs {
		result[strconv.FormatInt(taskID, 10)] = &logJSON{
			ID:        l.ID,
			LineType:  l.LineType,
			Content:   l.Content,
			CreatedAt: apiTime(l.CreatedAt.Time),
		}
	}
	jsonOK(w, result)
}

// --- Terminal info & session bootstrap ---

type terminalInfoJSON struct {
	DaemonSession string `json:"daemon_session"`
	TmuxWindowID  string `json:"tmux_window_id"`
	ClaudePaneID  string `json:"claude_pane_id"`
	ShellPaneID   string `json:"shell_pane_id"`
	WindowTarget  string `json:"window_target"`
	WindowExists  bool   `json:"window_exists"`
	// PaneBorrowedBy is set when the executor pane is alive but currently
	// joined into another session (the TUI moves panes into its own session
	// while a detail view is open, destroying the daemon window). Clients
	// should report this and re-poll: the pane returns to a daemon window
	// when the TUI releases it.
	PaneBorrowedBy string `json:"pane_borrowed_by,omitempty"`
	Workdir        string `json:"workdir"`
}

// findTaskWindowTarget scans tmux for the task's window in any task-daemon
// session and returns its "session:index" target, or "".
func (s *Server) findTaskWindowTarget(taskID int64) string {
	if s.runner == nil {
		return ""
	}
	out, err := s.runner.Output("tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}")
	if err != nil {
		return ""
	}
	windowName := fmt.Sprintf("task-%d", taskID)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] == windowName && strings.HasPrefix(parts[0], "task-daemon-") {
			return parts[0] + ":" + parts[1]
		}
	}
	return ""
}

// findPaneSession locates a pane by ID anywhere on the tmux server and
// returns the session holding it, or "". Pane IDs are stable across
// join-pane moves, unlike window names.
func (s *Server) findPaneSession(paneID string) string {
	if s.runner == nil || paneID == "" {
		return ""
	}
	out, err := s.runner.Output("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && parts[0] == paneID {
			return parts[1]
		}
	}
	return ""
}

func (s *Server) taskWorkdir(task *db.Task) string {
	if task.WorktreePath != "" {
		return task.WorktreePath
	}
	if task.Project != "" {
		if project, err := s.db.GetProjectByName(task.Project); err == nil && project != nil {
			return project.Path
		}
	}
	return ""
}

func (s *Server) terminalInfo(task *db.Task) terminalInfoJSON {
	target := s.findTaskWindowTarget(task.ID)
	info := terminalInfoJSON{
		DaemonSession: task.DaemonSession,
		TmuxWindowID:  task.TmuxWindowID,
		ClaudePaneID:  task.ClaudePaneID,
		ShellPaneID:   task.ShellPaneID,
		WindowTarget:  target,
		WindowExists:  target != "",
		Workdir:       s.taskWorkdir(task),
	}
	// No daemon window, but the executor pane may still be alive inside
	// another session (TUI detail view borrows panes via join-pane).
	if target == "" {
		if session := s.findPaneSession(task.ClaudePaneID); session != "" {
			info.PaneBorrowedBy = session
		}
	}
	return info
}

func (s *Server) handleTerminalInfo(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	jsonOK(w, s.terminalInfo(task))
}

// handleEnsureSession makes sure an interactive executor session (tmux window)
// exists for the task, starting one when needed, and returns terminal info for
// attaching to it.
func (s *Server) handleEnsureSession(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	if s.sessions == nil {
		jsonErr(w, "executor manager not configured", http.StatusServiceUnavailable)
		return
	}

	// Refuse to start a second session while the live pane is joined into
	// another session (e.g. a TUI detail view) — it would duplicate the
	// executor. The pane returns to a daemon window when released.
	if s.findTaskWindowTarget(task.ID) == "" {
		if session := s.findPaneSession(task.ClaudePaneID); session != "" {
			jsonErr(w, "executor pane is currently attached to "+session, http.StatusConflict)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if _, _, err := s.sessions.EnsureTaskWindow(ctx, task, task.ClaudeSessionID, ""); err != nil {
		jsonErr(w, "failed to start session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-read the task: EnsureTaskWindow persists pane IDs and daemon session.
	if fresh, err := s.db.GetTask(task.ID); err == nil && fresh != nil {
		task = fresh
	}
	jsonOK(w, s.terminalInfo(task))
}

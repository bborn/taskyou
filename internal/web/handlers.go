package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/textutil"

	"github.com/bborn/workflow/internal/agentsend"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
	"github.com/bborn/workflow/internal/tasksummary"
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

// jsonErrCode is jsonErr with a stable machine-readable code beside the prose,
// for failures a client is expected to branch on rather than just display.
func jsonErrCode(w http.ResponseWriter, msg, errCode string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": errCode})
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

	// `filter` runs the shared query grammar (internal/taskfilter) server-side.
	// It exists so a browser client never has to reimplement that grammar: a
	// second parser is a second set of answers, and a saved view would then mean
	// one thing on the board and another in the filter bar.
	//
	// The match happens in Go after the query, so a SQL LIMIT here would cap the
	// rows BEFORE filtering. Widen the fetch and re-apply the limit below.
	filterQuery := strings.TrimSpace(q.Get("filter"))
	if filterQuery != "" {
		opts.Limit = 0
		opts.IncludeClosed = true
	}

	tasks, err := s.db.ListTasks(opts)
	if err != nil {
		jsonErr(w, "failed to list tasks", http.StatusInternalServerError)
		return
	}

	if filterQuery != "" {
		tasks = s.parseViewQuery(filterQuery).Filter(tasks)
		if limit > 0 && len(tasks) > limit {
			tasks = tasks[:limit]
		}
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
	// Placement is a host chosen by hand instead of by the resolver: "" or
	// "auto" leaves the choice to it, "local" pins the task here, anything else
	// is an SSH destination from GET /api/placement/hosts. PlacementWorkDir is
	// that project's directory on that host; it is looked up when omitted.
	Placement        string `json:"placement"`
	PlacementWorkDir string `json:"placement_workdir"`
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
			title = textutil.Truncate(title, 53, "...")
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

	// A hand-picked host is recorded as the task's placement decision, which the
	// executor prefers over asking the resolver. The task exists either way: a
	// placement that cannot be recorded is reported, not a reason to lose what
	// the user just wrote.
	if err := executor.ChoosePlacement(r.Context(), s.db, task, req.Placement, req.PlacementWorkDir); err != nil {
		jsonErr(w, "task created, but its host could not be set: "+err.Error(), http.StatusBadRequest)
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

	var latest *db.TaskLog
	if len(logs) > 0 {
		latest = logs[len(logs)-1]
	}
	if tasksummary.NeedsRefresh(task, latest) {
		tasksummary.KickoffRewrite(s.db, task.ID)
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
	// Status is decoded only to REFUSE it. This route edits a task's fields;
	// status is not a field, it is a transition, and it has exactly one entry
	// point (POST /api/tasks/{id}/status → SetTaskStatus) where the actor, the
	// reason and the completion gates live. Without this the field would simply
	// not decode, and a client would get a cheerful 200 with nothing changed —
	// the silent no-op is how a caller comes to believe it closed a task it did
	// not close.
	Status *string `json:"status"`
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
	if req.Status != nil {
		jsonErr(w, "status is not editable here — POST /api/tasks/{id}/status, which records who and why and runs the completion gates",
			http.StatusBadRequest)
		return
	}
	if req.Model != nil {
		task.Model = *req.Model
		// Validated only when the request is actually setting a model, and against
		// the executor as it stands after this update. Updates that leave the model
		// alone must keep working even if the stored value predates this check.
		if err := s.db.ValidateTaskModel(task); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
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

	oldStatus := ""
	if existing, _ := s.db.GetTask(id); existing != nil {
		oldStatus = existing.Status
	}

	// A status change over HTTP is a person dragging a card or hitting a button;
	// record that, and let the gates in SetTaskStatus decide whether it is
	// allowed. A refusal is a 409 with the gate's own explanation, not a 500 —
	// the request was understood and deliberately declined.
	if err := s.db.SetTaskStatus(id, req.Status, db.ActorWeb,
		"status changed from the web API",
		db.ByHuman("PUT /api/tasks/%d/status → %s", id, req.Status)); err != nil {
		if db.IsRefused(err) {
			jsonErr(w, err.Error(), http.StatusConflict)
			return
		}
		jsonErr(w, "failed to update status", http.StatusInternalServerError)
		return
	}
	tasksummary.KickoffOnStatusChange(s.db, oldStatus, req.Status, id)

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

	if err := s.db.SetTaskStatus(task.ID, db.StatusQueued, db.ActorWeb,
		"execution requested from the web API",
		db.ByHuman("POST /api/tasks/%d/execute", task.ID)); err != nil {
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

	if err := s.db.SetTaskStatus(task.ID, db.StatusDone, db.ActorWeb,
		"closed from the web API",
		db.ByHuman("POST /api/tasks/%d/close", task.ID)); err != nil {
		if db.IsRefused(err) {
			jsonErr(w, err.Error(), http.StatusConflict)
			return
		}
		jsonErr(w, "failed to close task", http.StatusInternalServerError)
		return
	}
	// Skip-if-exists: freeze a stand that was written on block; fill one in
	// if the task never blocked.
	tasksummary.KickoffGenerate(s.db, task.ID)

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
	AttachmentIDs []int64 `json:"attachment_ids"`
	Message       string  `json:"message"`
	Enter         bool    `json:"enter"`
	Key           string  `json:"key"`
	// Force sends the message even while the agent is working. The GUI sets it
	// only after telling the user the agent is busy and being told to go ahead.
	Force bool `json:"force"`
	// NoSubmit types the text without pressing Enter, leaving it in the agent's
	// input box (the API twin of `ty input --no-submit`).
	NoSubmit bool `json:"no_submit"`
	// Wait holds the response until the agent has answered THIS message, rather
	// than returning as soon as the text is delivered.
	Wait      bool `json:"wait"`
	TimeoutMs int  `json:"timeout_ms"`
}

// defaultReplyWait bounds a wait=true input request. Long enough for an agent to
// think, short enough that a browser request does not hang on a dead session.
const defaultReplyWait = 3 * time.Minute

// handleTaskInput types into a task's live agent.
//
// The pane is resolved by the task's tmux tag, never from task.ClaudePaneID:
// tmux reuses pane ids, and a stale one names whatever pane took that id next —
// which is how one task's input once landed in another task's session. A task
// with no tagged pane is refused.
func (s *Server) handleTaskInput(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req inputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.AttachmentIDs) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		r = r.WithContext(ctx)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Minute))
	}

	// A remote task's pane is on another machine's tmux server, so it is found
	// by the code that owns that connection rather than by a tag lookup here —
	// but the delivery itself is the same one every other surface uses, so a
	// remote agent is no more likely to be typed over mid-turn than a local one.
	if task.PlacementTarget != "" && task.PlacementTarget != "local" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		info := s.remoteTerminalInfo(ctx, task, false)
		cancel()
		if info.Error != "" {
			jsonErr(w, info.Error, http.StatusBadGateway)
			return
		}
		if info.ClaudePaneID == "" {
			jsonErr(w, "task has no remote executor pane", http.StatusConflict)
			return
		}
		if len(req.AttachmentIDs) > 0 {
			runner := executor.RemoteRunner{Host: info.RemoteHost, WorkDir: info.Workdir}
			paths, err := executor.StageAttachments(r.Context(), s.db, task.ID, info.Workdir, &runner, req.AttachmentIDs)
			if err != nil {
				jsonErr(w, err.Error(), http.StatusBadGateway)
				return
			}
			req.Message += executor.AttachmentPrompt(paths)
		}
		terminal := paneTerminal{ctx: r.Context(), runner: executor.RemoteRunner{Host: info.RemoteHost}}
		sender := agentsend.NewForHost(terminalRunner{terminal}, s.db, info.RemoteHost)
		s.writeInput(w, r, sender, inputTarget{task: task, pane: info.ClaudePaneID}, req)
		return
	}

	if s.runner == nil {
		jsonErr(w, "command runner not configured", http.StatusInternalServerError)
		return
	}
	if len(req.AttachmentIDs) > 0 {
		paths, err := executor.StageAttachments(r.Context(), s.db, task.ID, s.taskWorkdir(task), nil, req.AttachmentIDs)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.Message += executor.AttachmentPrompt(paths)
	}
	s.writeInput(w, r, s.agentSender(), inputTarget{task: task}, req)
}

// inputTarget is where one input request is going: a task, and — for a remote
// task — the pane already resolved on its host. An empty pane means "resolve it
// by tag", which is what a local task does.
type inputTarget struct {
	task *db.Task
	pane string
}

func (t inputTarget) send(sender *agentsend.Sender, p agentsend.Prompt) error {
	if t.pane != "" {
		return sender.SendToPane(t.pane, p)
	}
	return sender.Send(p)
}

// sendAndWait delivers and then waits for the agent's answer to THIS prompt.
func (t inputTarget) sendAndWait(ctx context.Context, sender *agentsend.Sender, p agentsend.Prompt, timeout time.Duration) (db.AgentTurn, error) {
	if t.pane != "" {
		return sender.SendToPaneAndWait(ctx, t.pane, p, timeout)
	}
	return sender.SendAndWait(ctx, p, timeout)
}

func (t inputTarget) sendKeys(sender *agentsend.Sender, keys ...string) error {
	if t.pane != "" {
		return sender.SendKeysToPane(t.pane, keys...)
	}
	return sender.SendKeys(t.task.ID, keys...)
}

// writeInput performs one input request and writes its response. Local and
// remote differ only in how the pane was found.
func (s *Server) writeInput(w http.ResponseWriter, r *http.Request, sender *agentsend.Sender, target inputTarget, req inputRequest) {
	task := target.task

	if req.Key != "" {
		if err := target.sendKeys(sender, req.Key); err != nil {
			writeSendErr(w, err, "failed to send key")
			return
		}
	}

	switch {
	case req.Message != "":
		prompt := agentsend.Prompt{
			TaskID: task.ID,
			Text:   req.Message,
			Force:  req.Force,
			Submit: !req.NoSubmit,
		}
		if req.Wait {
			timeout := defaultReplyWait
			if req.TimeoutMs > 0 {
				timeout = time.Duration(req.TimeoutMs) * time.Millisecond
			}
			turn, err := target.sendAndWait(r.Context(), sender, prompt, timeout)
			if err != nil {
				writeSendErr(w, err, "failed to send input")
				return
			}
			jsonOK(w, map[string]interface{}{"ok": true, "turn": turn.Started})
			return
		}
		if err := target.send(sender, prompt); err != nil {
			writeSendErr(w, err, "failed to send input")
			return
		}
	case req.Enter:
		if err := target.sendKeys(sender, "Enter"); err != nil {
			writeSendErr(w, err, "failed to send enter")
			return
		}
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// writeSendErr turns a delivery failure into a response the GUI can act on. The
// machine-readable code matters: "agent_busy" is the one the composer offers to
// override, and it must not be told apart from a dead session by string match.
func writeSendErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, agentsend.ErrBusy):
		jsonErrCode(w, err.Error()+" — wait for it to finish, or send again to interrupt",
			"agent_busy", http.StatusConflict)
	case errors.Is(err, agentsend.ErrNoPane):
		jsonErrCode(w, err.Error()+" (its session is not running)", "no_agent_pane", http.StatusConflict)
	case errors.Is(err, db.ErrReplyTimeout):
		jsonErrCode(w, "the message was delivered, but the agent did not answer in time",
			"reply_timeout", http.StatusGatewayTimeout)
	default:
		jsonErr(w, fallback+": "+err.Error(), http.StatusInternalServerError)
	}
}

// --- Task logs ---

func (s *Server) handleTaskOutput(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	if s.runner == nil {
		jsonErr(w, "command runner not configured", http.StatusInternalServerError)
		return
	}

	// Read from the pane tmux says is this task's agent. The stored id is the
	// fallback for windows made before panes were tagged; on its own it can name
	// another task's pane and show its output as this task's.
	paneID, err := s.agentSender().AgentPane(task.ID)
	if err != nil {
		paneID = task.ClaudePaneID
	}
	if paneID == "" {
		jsonErr(w, "task has no executor pane", http.StatusBadRequest)
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

	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	logs, err := s.db.GetTaskLogsBefore(id, before, limit)
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
	PlacementTarget string        `json:"placement_target"`
	PlacementReason string        `json:"placement_reason"`
	ID              int64         `json:"id"`
	Title           string        `json:"title"`
	Body            string        `json:"body"`
	Status          string        `json:"status"`
	Type            string        `json:"type"`
	Project         string        `json:"project"`
	Executor        string        `json:"executor"`
	Pinned          bool          `json:"pinned"`
	Tags            string        `json:"tags"`
	PermissionMode  string        `json:"permission_mode"`
	BranchName      string        `json:"branch_name"`
	Port            int           `json:"port,omitempty"`
	WorktreePath    string        `json:"worktree_path,omitempty"`
	HasExecutor     bool          `json:"has_executor"`
	EffortLevel     string        `json:"effort_level,omitempty"`
	Model           string        `json:"model,omitempty"`
	SourceBranch    string        `json:"source_branch,omitempty"`
	DaemonSession   string        `json:"daemon_session,omitempty"`
	TmuxWindowID    string        `json:"tmux_window_id,omitempty"`
	ClaudePaneID    string        `json:"claude_pane_id,omitempty"`
	ShellPaneID     string        `json:"shell_pane_id,omitempty"`
	PRURL           string        `json:"pr_url"`
	PRNumber        int           `json:"pr_number,omitempty"`
	PR              *prStatusJSON `json:"pr,omitempty"`
	Summary         string        `json:"summary,omitempty"`
	Stand           string        `json:"stand,omitempty"`
	CreatedAt       string        `json:"created_at"`
	UpdatedAt       string        `json:"updated_at"`
	StartedAt       string        `json:"started_at,omitempty"`
	CompletedAt     string        `json:"completed_at,omitempty"`
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
		PlacementTarget: t.PlacementTarget, PlacementReason: t.PlacementReason,
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
		Stand:          tasksummary.DisplayStand(t.Summary),
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

// Package mcp provides an MCP (Model Context Protocol) server for workflow tools.
// This allows Claude to directly signal task completion and request input
// instead of relying on text parsing.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/bborn/workflow/internal/textutil"

	"github.com/bborn/workflow/internal/completion"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
	"github.com/bborn/workflow/internal/question"
)

// Server is an MCP server that provides workflow tools to Claude.
type Server struct {
	db     *db.DB
	taskID int64
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex

	// Callbacks for task state changes
	onComplete   func()
	onNeedsInput func(question string)

	// Track if context was requested but empty (for reminder on completion)
	contextWasEmpty bool
}

// NewServer creates a new MCP server for a specific task.
func NewServer(database *db.DB, taskID int64) *Server {
	return &Server{
		db:     database,
		taskID: taskID,
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
	}
}

// SetCallbacks sets the callbacks for task state changes.
func (s *Server) SetCallbacks(onComplete func(), onNeedsInput func(question string)) {
	s.onComplete = onComplete
	s.onNeedsInput = onNeedsInput
}

// PR lookup moved to internal/completion.LookupPR so the CLI completion path
// resolves PRs identically (see completion.Complete).

// JSON-RPC types
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	ServerInfo      serverInfo             `json:"serverInfo"`
	Capabilities    map[string]interface{} `json:"capabilities"`
}

type tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []tool `json:"tools"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type toolCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Run starts the MCP server and processes requests until EOF.
func (s *Server) Run() error {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		s.handleRequest(&req)
	}
}

func (s *Server) handleRequest(req *jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		s.sendResult(req.ID, initializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo: serverInfo{
				Name:    "taskyou-mcp",
				Version: "1.0.0",
			},
			Capabilities: map[string]interface{}{
				"tools": map[string]interface{}{},
			},
		})

	case "notifications/initialized":
		// No response needed for notifications

	case "tools/list":
		s.sendResult(req.ID, toolsListResult{
			Tools: []tool{
				{
					Name:        "taskyou_complete",
					Description: "Mark the current task as complete. Call this when you have finished the task successfully.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"summary": map[string]interface{}{
								"type":        "string",
								"description": "Brief summary of what was accomplished",
							},
						},
						"required": []string{"summary"},
					},
				},
				{
					Name:        "taskyou_needs_input",
					Description: needsInputDescription,
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"question": map[string]interface{}{
								"type":        "string",
								"description": "The question to ask the user",
							},
							"kind": map[string]interface{}{
								"type":        "string",
								"enum":        question.Kinds(),
								"description": "How the user answers. 'text' (default): in their own words. 'choice': pick exactly one of options. 'multi_choice': pick any of options. 'confirm': Yes or No (no options). Defaults to 'choice' when options are given.",
							},
							"options": map[string]interface{}{
								"type":        "array",
								"minItems":    question.MinOptions,
								"maxItems":    question.MaxOptions,
								"description": fmt.Sprintf("The answers to pick from, for choice and multi_choice: %d to %d, each a short label (the answer you get back) with an optional one-line description.", question.MinOptions, question.MaxOptions),
								"items": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"label": map[string]interface{}{
											"type":        "string",
											"description": "A short, distinct answer, e.g. \"Postgres\"",
										},
										"description": map[string]interface{}{
											"type":        "string",
											"description": "Optional detail shown under the label, e.g. the trade-off",
										},
									},
									"required": []string{"label"},
								},
							},
							"allow_other": map[string]interface{}{
								"type":        "boolean",
								"description": "Also let the user answer in their own words instead of (or, for multi_choice, as well as) picking an option.",
							},
						},
						"required": []string{"question"},
					},
				},
				{
					Name:        "taskyou_show_task",
					Description: "Get full details of any task by ID — title, status, body, recent activity logs, summary. Restricted to tasks in the same project as the current task. Useful for reading the spec/context of your own task or for inspecting a related task before creating a follow-up.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"task_id": map[string]interface{}{
								"type":        "integer",
								"description": "The ID of the task to retrieve",
							},
						},
						"required": []string{"task_id"},
					},
				},
				{
					Name:        "taskyou_create_task",
					Description: "Create a new task in the system. Use this to break down complex work or track future tasks.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Title of the task",
							},
							"body": map[string]interface{}{
								"type":        "string",
								"description": "Detailed description of the task",
							},
							"project": map[string]interface{}{
								"type":        "string",
								"description": "Project name (defaults to current project)",
							},
							"type": map[string]interface{}{
								"type":        "string",
								"description": "Task type (code, writing, thinking)",
							},
							"status": map[string]interface{}{
								"type":        "string",
								"description": "Initial status (backlog, queued, defaults to backlog)",
							},
							"dangerous_mode": map[string]interface{}{
								"type":        "boolean",
								"description": "Execute in dangerous mode (skip permission prompts). Only applies when status is 'queued'. Prefer 'permission_mode' instead.",
							},
							"permission_mode": map[string]interface{}{
								"type":        "string",
								"description": "Permission mode for execution (most to least gated): 'default' (prompt for each permission), 'accept-edits' (Claude's acceptEdits / --permission-mode acceptEdits: auto-accept file edits but still prompt for risky actions), 'auto' (Claude Code's auto mode / --permission-mode auto: an AI classifier auto-approves safe actions, including safe commands, while still blocking dangerous ones), or 'dangerous' (skip all prompts / --dangerously-skip-permissions). Note: 'auto' and 'accept-edits' are DIFFERENT — 'auto' is more autonomous. Defaults to the project's configured default.",
								"enum":        []string{"default", "accept-edits", "auto", "dangerous"},
							},
							"remote_control": map[string]interface{}{
								"type":        "boolean",
								"description": "Launch the task's Claude session with --remote-control (interactive, remote-drivable)",
							},
						},
						"required": []string{"title"},
					},
				},
				{
					Name:        "taskyou_create_pipeline",
					Description: "Create a multi-model workflow for a goal: one goal is split into an ordered chain of phase tasks, each routed to its own executor/model, all on one shared git branch. There are no built-in workflows — pass a 'definition' naming a workflow kind (a YAML file; see the enum, or define one with ty pipeline new). Steps advance automatically — sequential where they depend on each other, parallel where they don't. Use this instead of a single task when a goal benefits from a plan/code/review split across different models. The first phase is queued immediately. Requires a git-worktree project with a remote to push to.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"goal": map[string]interface{}{
								"type":        "string",
								"description": "The overall goal, threaded into every phase's prompt.",
							},
							"project": map[string]interface{}{
								"type":        "string",
								"description": "Project name (defaults to the current task's project).",
							},
							"definition": map[string]interface{}{
								"type":        "string",
								"description": "Workflow kind name — a YAML file of the same name (required; there is no default workflow).",
								"enum":        pipeline.DefinitionNames(),
							},
							"permission_mode": map[string]interface{}{
								"type":        "string",
								"description": "Permission mode for every phase (default, accept-edits, auto, dangerous). Defaults to the project's configured default.",
								"enum":        []string{"default", "accept-edits", "auto", "dangerous"},
							},
						},
						"required": []string{"goal"},
					},
				},
				{
					Name:        "taskyou_list_tasks",
					Description: "List active tasks (queued, processing, blocked, backlog) in the project. Use this to see what work is pending or in progress.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"status": map[string]interface{}{
								"type":        "string",
								"description": "Filter by status (queued, processing, blocked, backlog). If omitted, shows all active tasks.",
							},
							"limit": map[string]interface{}{
								"type":        "integer",
								"description": "Maximum number of tasks to return (default: 10, max: 50)",
							},
							"project": map[string]interface{}{
								"type":        "string",
								"description": "Filter by project (defaults to current project)",
							},
						},
					},
				},
				{
					Name:        "taskyou_get_project_context",
					Description: "Get cached project context (codebase structure, patterns, conventions). Call this FIRST before exploring the codebase. If context exists, use it to skip exploration. If empty, explore the codebase once and save a summary via taskyou_set_project_context.",
					InputSchema: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					Name:        "taskyou_set_project_context",
					Description: "Save auto-generated project context for future tasks. Call this after exploring a codebase to cache your findings (structure, patterns, key files, conventions). Future tasks will skip exploration by reading this context.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"context": map[string]interface{}{
								"type":        "string",
								"description": "The project context to cache. Include: codebase structure, key directories, architectural patterns, coding conventions, important files, and any other information useful for future tasks.",
							},
						},
						"required": []string{"context"},
					},
				},
				{
					Name:        "taskyou_set_artifact",
					Description: "Save a workflow phase document (e.g. research, design, plan) so a later phase in the same workflow can read it. Artifacts are shared across the workflow's steps via the pipeline branch — this is how one phase hands its full output document to the next WITHOUT committing docs to git. Call this at the end of a document phase with the complete markdown you produced.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name": map[string]interface{}{
								"type":        "string",
								"description": "A short kebab-case name identifying this artifact, typically the phase name (e.g. \"research-questions\", \"research\", \"design\", \"structure-outline\", \"plan\").",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The full markdown document to store. Re-saving the same name overwrites the previous content.",
							},
						},
						"required": []string{"name", "content"},
					},
				},
				{
					Name:        "taskyou_get_artifact",
					Description: "Read a workflow phase document produced by an earlier phase in this workflow. Call this to pick up the previous phase's output (e.g. the research phase reads the research-questions artifact). Pass a name to fetch one artifact; omit name to list all artifacts for this workflow with their contents.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name": map[string]interface{}{
								"type":        "string",
								"description": "The artifact name to fetch (e.g. \"research-questions\"). Omit to return all artifacts for this workflow.",
							},
						},
					},
				},
			},
		})

	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.sendError(req.ID, -32602, "Invalid params")
			return
		}
		s.handleToolCall(req.ID, &params)

	default:
		s.sendError(req.ID, -32601, "Method not found")
	}
}

func (s *Server) handleToolCall(id interface{}, params *toolCallParams) {
	switch params.Name {
	case "taskyou_complete":
		summary, _ := params.Arguments["summary"].(string)

		task, _ := s.db.GetTask(s.taskID)

		// Check if we should remind about saving project context. This is
		// MCP-specific (it keys off session state), so it stays here and is appended
		// to whatever the shared completion decision produces.
		var contextReminder string
		if s.contextWasEmpty && task != nil && task.Project != "" {
			if ctx, err := s.db.GetProjectContext(task.Project); err == nil && ctx == "" {
				contextReminder = "\n\n⚠️ REMINDER: You explored this codebase but didn't save project context. Consider calling taskyou_set_project_context to help future tasks skip exploration."
			}
		}

		// The whole decision — evidence gate, human gate, PR routing, done — lives in
		// internal/completion so `ty complete` takes the identical path. Only the
		// agent-facing wording and the session-teardown callback are MCP's job.
		outcome, err := completion.Complete(s.db, s.taskID, summary, completion.Options{AsyncSummary: true, Actor: db.ActorMCP})
		if err != nil {
			s.sendError(id, -32603, err.Error())
			return
		}

		if outcome.Kind == completion.KindVerifyFailed {
			// Completion was rejected: the step stays running so the agent can fix it.
			// No teardown callback — the agent's turn is NOT over.
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: fmt.Sprintf("❌ Verification failed — this step is NOT complete.\n\nThe configured check exited non-zero, so taskyou_complete was rejected:\n    %s\n\nFix the problem, then call taskyou_complete again.\n\n--- verify output (tail) ---\n%s", outcome.VerifyCommand, outcome.VerifyOutput)},
				},
			})
			return
		}

		// The agent's turn is over; let the executor tear down the session.
		if s.onComplete != nil {
			s.onComplete()
		}

		var resultText string
		switch outcome.Kind {
		case completion.KindGateParked:
			resultText = "Output saved. This is a human-review gate — the step is now 'blocked' awaiting a human to approve it (`ty close`), which releases the next phase. Do not call taskyou_complete again."
		case completion.KindPRReview:
			resultText = fmt.Sprintf("Work finished. PR #%d is up for review — the task is now 'blocked' awaiting a human merge, and a human will close it after merging. Do not call taskyou_complete again.", outcome.PRNumber)
		case completion.KindReview:
			resultText = "Work finished. The task is now 'blocked' awaiting a human to review and close it. Do not call taskyou_complete again."
		default:
			resultText = "Workflow step marked done; the next steps can start."
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: resultText + contextReminder},
			},
		})

	case "taskyou_needs_input":
		q, err := parseNeedsInput(params.Arguments)
		if err != nil {
			// A tool error, not a protocol error: the agent reads it and can fix
			// the call, where a JSON-RPC error reads as the tool being broken.
			s.sendResult(id, toolCallResult{
				IsError: true,
				Content: []contentBlock{{Type: "text", Text: "taskyou_needs_input: " + err.Error() + ". Nothing was asked; fix the call and try again."}},
			})
			return
		}
		q.TaskID = s.taskID

		// Log the question
		s.db.AppendTaskLog(s.taskID, "question", q.Question)

		// Record it where every surface can read it — the options are what let a
		// person answer from a phone with a tap. Stored before the status moves,
		// so whatever wakes on the task turning blocked finds its question.
		if err := s.db.SetPendingQuestion(q); err != nil {
			s.db.AppendTaskLog(s.taskID, "system", "Could not record the question's options: "+err.Error())
		}

		// Update task status to blocked
		if err := s.db.SetTaskStatus(s.taskID, db.StatusBlocked, db.ActorMCP,
			"the agent called taskyou_needs_input and is waiting on a human",
			db.Observedf("agent question: %s", truncateForEvidence(q.Question))); err != nil {
			// A question is only live while the task is blocked on it. One left
			// behind on a task that never got there would surface the next time
			// the task blocks for some other reason.
			if t, _ := s.db.GetTask(s.taskID); t == nil || t.Status != db.StatusBlocked {
				s.db.ClearPendingQuestion(s.taskID)
			}
		}

		// Trigger callback
		if s.onNeedsInput != nil {
			s.onNeedsInput(q.Question)
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: needsInputResult(q)},
			},
		})

	case "taskyou_show_task":
		taskIDFloat, ok := params.Arguments["task_id"].(float64)
		if !ok {
			s.sendError(id, -32602, "task_id is required")
			return
		}
		targetTaskID := int64(taskIDFloat)

		// Get current task's project for access control
		currentTask, err := s.db.GetTask(s.taskID)
		if err != nil || currentTask == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}

		// Get the requested task
		targetTask, err := s.db.GetTask(targetTaskID)
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to get task: %v", err))
			return
		}
		if targetTask == nil {
			s.sendError(id, -32602, fmt.Sprintf("Task #%d not found", targetTaskID))
			return
		}

		// Enforce project isolation
		if targetTask.Project != currentTask.Project {
			s.sendError(id, -32602, fmt.Sprintf("Task #%d is in a different project and cannot be accessed", targetTaskID))
			return
		}

		// Build response
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Task #%d: %s\n\n", targetTask.ID, targetTask.Title))
		sb.WriteString(fmt.Sprintf("**Status:** %s\n", targetTask.Status))
		sb.WriteString(fmt.Sprintf("**Type:** %s\n", targetTask.Type))
		sb.WriteString(fmt.Sprintf("**Project:** %s\n", targetTask.Project))
		if targetTask.Tags != "" {
			sb.WriteString(fmt.Sprintf("**Tags:** %s\n", targetTask.Tags))
		}
		sb.WriteString(fmt.Sprintf("**Created:** %s\n", targetTask.CreatedAt.Format("2006-01-02 15:04")))
		if targetTask.CompletedAt != nil {
			sb.WriteString(fmt.Sprintf("**Completed:** %s\n", targetTask.CompletedAt.Format("2006-01-02 15:04")))
		}

		sb.WriteString("\n## Description\n\n")
		if targetTask.Body != "" {
			sb.WriteString(targetTask.Body)
		} else {
			sb.WriteString("(no description)")
		}
		sb.WriteString("\n")

		// Include summary if available
		if targetTask.Summary != "" {
			sb.WriteString("\n## Summary\n\n")
			sb.WriteString(targetTask.Summary)
			sb.WriteString("\n")
		}

		// Include recent logs for context on what the task did
		logs, _ := s.db.GetTaskLogs(targetTaskID, 50)
		if len(logs) > 0 {
			sb.WriteString("\n## Recent Activity\n\n")
			// Logs come newest-first; reverse for chronological order
			for i := len(logs) - 1; i >= 0; i-- {
				l := logs[i]
				ts := l.CreatedAt.Time.Format("15:04:05")
				content := l.Content
				if len(content) > 300 {
					content = textutil.Truncate(content, 303, "...")
				}
				sb.WriteString(fmt.Sprintf("- `%s` [%s] %s\n", ts, l.LineType, content))
			}
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: sb.String()},
			},
		})

	case "taskyou_create_task":
		title, _ := params.Arguments["title"].(string)
		if title == "" {
			s.sendError(id, -32602, "title is required")
			return
		}
		body, _ := params.Arguments["body"].(string)
		project, _ := params.Arguments["project"].(string)
		taskType, _ := params.Arguments["type"].(string)
		status, _ := params.Arguments["status"].(string)
		dangerousMode, _ := params.Arguments["dangerous_mode"].(bool)
		permissionMode, _ := params.Arguments["permission_mode"].(string)
		remoteControl, _ := params.Arguments["remote_control"].(bool)

		// Default project to current task's project
		if project == "" {
			currentTask, err := s.db.GetTask(s.taskID)
			if err == nil && currentTask != nil {
				project = currentTask.Project
			}
		}

		if status == "" {
			status = db.StatusBacklog
		}

		// Resolve permission mode: explicit permission_mode wins, then the legacy
		// dangerous_mode bool; empty falls back to the project default in CreateTask.
		permissionMode = db.NormalizePermissionMode(permissionMode)
		if permissionMode == "" && dangerousMode {
			permissionMode = db.PermissionModeDangerous
		}

		newTask := &db.Task{
			Title:          title,
			Body:           body,
			Project:        project,
			Type:           taskType,
			Status:         status,
			PermissionMode: permissionMode,
			RemoteControl:  remoteControl,
		}

		if err := s.db.CreateTask(newTask); err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to create task: %v", err))
			return
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: fmt.Sprintf("Created task #%d: %s", newTask.ID, newTask.Title)},
			},
		})

	case "taskyou_create_pipeline":
		goal, _ := params.Arguments["goal"].(string)
		if strings.TrimSpace(goal) == "" {
			s.sendError(id, -32602, "goal is required")
			return
		}
		project, _ := params.Arguments["project"].(string)
		definition, _ := params.Arguments["definition"].(string)
		permissionMode, _ := params.Arguments["permission_mode"].(string)

		// Default project to the current task's project.
		if project == "" {
			currentTask, err := s.db.GetTask(s.taskID)
			if err == nil && currentTask != nil {
				project = currentTask.Project
			}
		}

		result, err := pipeline.Create(s.db, pipeline.Options{
			Goal:           goal,
			Project:        project,
			Definition:     definition,
			PermissionMode: db.NormalizePermissionMode(permissionMode),
			Execute:        true,
		})
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to create pipeline: %v", err))
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Created %s workflow on branch %s:\n", result.Definition.Name, result.Branch))
		for i, t := range result.Tasks {
			s := result.Definition.Steps[i]
			model := s.Model
			if model == "" {
				model = "default"
			}
			dep := ""
			if len(s.Deps) > 0 {
				dep = " ← " + strings.Join(s.Deps, "+")
			}
			sb.WriteString(fmt.Sprintf("- #%d %s (%s/%s) — %s%s\n", t.ID, s.Name, t.Executor, model, t.Status, dep))
		}
		sb.WriteString("The root step is running; steps advance automatically, with the two reviewers running in parallel.")

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: sb.String()},
			},
		})

	case "taskyou_list_tasks":
		status, _ := params.Arguments["status"].(string)
		project, _ := params.Arguments["project"].(string)

		limit := 10
		if l, ok := params.Arguments["limit"].(float64); ok {
			limit = int(l)
			if limit > 50 {
				limit = 50
			}
			if limit < 1 {
				limit = 1
			}
		}

		// Default to current project if not specified
		if project == "" {
			currentTask, err := s.db.GetTask(s.taskID)
			if err == nil && currentTask != nil {
				project = currentTask.Project
			}
		}

		tasks, err := s.db.ListTasks(db.ListTasksOptions{
			Status:  status,
			Project: project,
			Limit:   limit,
		})
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to list tasks: %v", err))
			return
		}

		if len(tasks) == 0 {
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: "No tasks found."},
				},
			})
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d task(s) in project '%s':\n\n", len(tasks), project))
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("- **#%d %s** (%s)\n", t.ID, t.Title, t.Status))
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: sb.String()},
			},
		})

	case "taskyou_get_project_context":
		// Get current task's project
		currentTask, err := s.db.GetTask(s.taskID)
		if err != nil || currentTask == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}

		if currentTask.Project == "" {
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: "No project associated with this task. Please explore the codebase manually."},
				},
			})
			return
		}

		context, err := s.db.GetProjectContext(currentTask.Project)
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to get project context: %v", err))
			return
		}

		if context == "" {
			s.contextWasEmpty = true
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: `No cached project context found.

⚠️ IMPORTANT: After exploring this codebase, you MUST save context using taskyou_set_project_context.

Include in your context:
- Project structure (key directories and their purposes)
- Tech stack and frameworks
- Architectural patterns and conventions
- Important files and entry points
- Common workflows

Example format:
## Project Structure
- src/ - Main source code
- tests/ - Test files
...

## Tech Stack
- Framework: Next.js
- Database: PostgreSQL
...

## Key Patterns
- Uses repository pattern for data access
...

This saves future tasks from re-exploring the codebase.`},
				},
			})
			return
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: fmt.Sprintf("## Cached Project Context\n\n%s", context)},
			},
		})

	case "taskyou_set_project_context":
		context, _ := params.Arguments["context"].(string)
		if context == "" {
			s.sendError(id, -32602, "context is required")
			return
		}

		// Get current task's project
		currentTask, err := s.db.GetTask(s.taskID)
		if err != nil || currentTask == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}

		if currentTask.Project == "" {
			s.sendError(id, -32602, "No project associated with this task")
			return
		}

		if err := s.db.SetProjectContext(currentTask.Project, context); err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to save project context: %v", err))
			return
		}

		s.db.AppendTaskLog(s.taskID, "system", fmt.Sprintf("Project context saved for '%s' (%d bytes)", currentTask.Project, len(context)))

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: fmt.Sprintf("Project context saved for '%s'. Future tasks will use this context to skip codebase exploration.", currentTask.Project)},
			},
		})

	case "taskyou_set_artifact":
		name, _ := params.Arguments["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			s.sendError(id, -32602, "name is required")
			return
		}
		content, _ := params.Arguments["content"].(string)
		if content == "" {
			s.sendError(id, -32602, "content is required")
			return
		}

		currentTask, err := s.db.GetTask(s.taskID)
		if err != nil || currentTask == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}
		// Derive the branch key from the task itself — never trust a client-supplied
		// branch — so an artifact is always scoped to the workflow the caller runs in.
		branch := pipeline.GroupKey(currentTask)
		if branch == "" {
			s.sendError(id, -32602, "This task is not part of a workflow — artifacts are only available inside a pipeline.")
			return
		}

		if err := s.db.SetPipelineArtifact(branch, name, content); err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to save artifact: %v", err))
			return
		}

		s.db.AppendTaskLog(s.taskID, "system", fmt.Sprintf("Workflow artifact '%s' saved (%d bytes)", name, len(content)))

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: fmt.Sprintf("Artifact '%s' saved. Later phases in this workflow can read it with taskyou_get_artifact.", name)},
			},
		})

	case "taskyou_get_artifact":
		currentTask, err := s.db.GetTask(s.taskID)
		if err != nil || currentTask == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}
		branch := pipeline.GroupKey(currentTask)
		if branch == "" {
			s.sendError(id, -32602, "This task is not part of a workflow — artifacts are only available inside a pipeline.")
			return
		}

		name, _ := params.Arguments["name"].(string)
		name = strings.TrimSpace(name)

		if name != "" {
			// Fetch one named artifact.
			content, err := s.db.GetPipelineArtifact(branch, name)
			if err != nil {
				s.sendError(id, -32603, fmt.Sprintf("Failed to read artifact: %v", err))
				return
			}
			if content == "" {
				s.sendResult(id, toolCallResult{
					Content: []contentBlock{
						{Type: "text", Text: fmt.Sprintf("No artifact named '%s' has been produced by this workflow yet.", name)},
					},
				})
				return
			}
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: fmt.Sprintf("## Artifact: %s\n\n%s", name, content)},
				},
			})
			return
		}

		// No name — list every artifact for this workflow with its contents.
		artifacts, err := s.db.ListPipelineArtifacts(branch)
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to list artifacts: %v", err))
			return
		}
		if len(artifacts) == 0 {
			s.sendResult(id, toolCallResult{
				Content: []contentBlock{
					{Type: "text", Text: "No artifacts have been produced by this workflow yet."},
				},
			})
			return
		}
		var sb strings.Builder
		for i, a := range artifacts {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("## Artifact: %s\n\n%s", a.Name, a.Content))
		}
		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: sb.String()},
			},
		})

	default:
		s.sendError(id, -32602, fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

func (s *Server) sendResult(id interface{}, result interface{}) {
	s.send(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *Server) sendError(id interface{}, code int, message string) {
	s.send(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

func (s *Server) send(resp jsonRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.writer.Write(data)
	s.writer.Write([]byte("\n"))
}

// truncateForEvidence keeps an agent's own words in the status log without
// letting a runaway question become the log entry.
func truncateForEvidence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// needsInputDescription is what an agent reads to decide how to ask. The
// options are the point: an agent that knows the answer is one of a few things
// should let a person give it with a tap, not make them type it.
const needsInputDescription = `Request input from the user. Call this when you need clarification or additional information to proceed. The task moves to 'blocked' and a human is notified; their answer arrives as your next message.

When the answer is a discrete decision — which of a few approaches, which of several items, yes or no — offer the answers as options so the user can reply with one tap on their phone or one keypress in the terminal:
- kind "choice": pick exactly one of 2-6 options. You receive "Selected: <label>".
- kind "multi_choice": pick any of 2-6 options. You receive "Selected: <label>, <label>".
- kind "confirm": yes or no (no options). You receive "Yes" or "No".
Keep labels short and distinct (they are what you get back) and put the trade-off in each option's description. Set allow_other to also accept an answer in the user's own words, which arrives as they wrote it.

Use a plain question (kind "text", the default) when the answer needs explaining — open-ended requirements, missing credentials, anything you cannot enumerate. The user may always reply in their own words instead of picking.`

// parseNeedsInput builds the question from taskyou_needs_input's arguments and
// checks it. The errors are for the agent: they say what to change.
func parseNeedsInput(args map[string]interface{}) (*db.PendingQuestion, error) {
	q := &db.PendingQuestion{}

	text, ok := args["question"].(string)
	if !ok || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("question is required")
	}
	q.Question = text

	if v, present := args["kind"]; present && v != nil {
		kind, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("kind must be a string, one of %s", strings.Join(question.Kinds(), ", "))
		}
		q.Kind = kind
	}

	if v, present := args["allow_other"]; present && v != nil {
		allow, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("allow_other must be true or false")
		}
		q.AllowOther = allow
	}

	if v, present := args["options"]; present && v != nil {
		opts, err := parseOptions(v)
		if err != nil {
			return nil, err
		}
		q.Options = opts
	}

	if err := question.Normalize(q); err != nil {
		return nil, err
	}
	return q, nil
}

// parseOptions accepts the documented [{label, description}] shape, plus two
// that agents send in practice: bare strings, and the whole array serialised
// as a JSON string.
func parseOptions(v interface{}) ([]db.QuestionOption, error) {
	const shape = `options must be an array of {"label": "...", "description": "..."} objects`
	if s, ok := v.(string); ok {
		var decoded interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("%s", shape)
		}
		v = decoded
	}
	items, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s", shape)
	}
	opts := make([]db.QuestionOption, 0, len(items))
	for i, item := range items {
		switch o := item.(type) {
		case string:
			opts = append(opts, db.QuestionOption{Label: o})
		case map[string]interface{}:
			label, ok := o["label"].(string)
			if !ok {
				return nil, fmt.Errorf("option %d needs a string label", i+1)
			}
			desc := ""
			if d, present := o["description"]; present && d != nil {
				if desc, ok = d.(string); !ok {
					return nil, fmt.Errorf("option %d's description must be a string", i+1)
				}
			}
			opts = append(opts, db.QuestionOption{Label: label, Description: desc})
		default:
			return nil, fmt.Errorf("%s (option %d is neither)", shape, i+1)
		}
	}
	return opts, nil
}

// needsInputResult tells the agent what happens next. A plain question keeps
// the reply it always had.
func needsInputResult(q *db.PendingQuestion) string {
	const asked = "Input requested. The user will be notified."
	if !question.IsStructured(q) {
		return asked
	}
	var example string
	switch q.Kind {
	case db.QuestionConfirm:
		example = `"Yes" or "No"`
	case db.QuestionMultiChoice:
		example = `"Selected: <label>, <label>"`
	default:
		example = `"Selected: <label>"`
	}
	if q.AllowOther {
		example += ", or an answer in their own words"
	}
	return fmt.Sprintf("%s They can answer by picking; the answer arrives as your next message: %s. Stop here and wait for it.", asked, example)
}

// Package mcp provides an MCP (Model Context Protocol) server for workflow tools.
// This allows Claude to directly signal task completion and request input
// instead of relying on text parsing.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
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
					Description: "Request input from the user. Call this when you need clarification or additional information to proceed.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"question": map[string]interface{}{
								"type":        "string",
								"description": "The question to ask the user",
							},
						},
						"required": []string{"question"},
					},
				},
				{
					Name:        "taskyou_screenshot",
					Description: "Take a screenshot of the entire screen and save it as an attachment to the current task. Use this to capture visual output of your work, especially for frontend/UI tasks. Screenshots are saved and can be reviewed by the user or included in PRs.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"filename": map[string]interface{}{
								"type":        "string",
								"description": "Optional filename for the screenshot (defaults to screenshot-{timestamp}.png)",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional description of what the screenshot shows",
							},
						},
					},
				},
				{
					Name:        "taskyou_show_task",
					Description: "Get details of a specific past task by ID. Use this after taskyou_search_tasks to get full details of a relevant task. Only works for tasks in the same project.",
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
						},
						"required": []string{"title"},
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
					Name:        "taskyou_spotlight",
					Description: "Enable spotlight mode to sync worktree changes back to the main repository for testing. This bridges the gap between isolated task development and application runtime by syncing git-tracked files to where your app runs. Use 'start' to enable, 'stop' to restore original state, 'sync' for manual sync, or 'status' to check current state.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"action": map[string]interface{}{
								"type":        "string",
								"enum":        []string{"start", "stop", "sync", "status"},
								"description": "Action to perform: 'start' enables spotlight mode and syncs files, 'stop' disables and restores original state, 'sync' manually syncs files (while active), 'status' shows current spotlight state",
							},
						},
						"required": []string{"action"},
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

		// Check if we should remind about saving project context
		var contextReminder string
		if s.contextWasEmpty {
			// Check if context is still empty
			if task, err := s.db.GetTask(s.taskID); err == nil && task != nil && task.Project != "" {
				if ctx, err := s.db.GetProjectContext(task.Project); err == nil && ctx == "" {
					contextReminder = "\n\n⚠️ REMINDER: You explored this codebase but didn't save project context. Consider calling taskyou_set_project_context to help future tasks skip exploration."
				}
			}
		}

		// Log the completion
		s.db.AppendTaskLog(s.taskID, "system", fmt.Sprintf("Task completed: %s", summary))

		// Update task status
		s.db.UpdateTaskStatus(s.taskID, db.StatusDone)

		// Trigger callback
		if s.onComplete != nil {
			s.onComplete()
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: "Task marked as complete." + contextReminder},
			},
		})

	case "taskyou_needs_input":
		question, _ := params.Arguments["question"].(string)

		// Log the question
		s.db.AppendTaskLog(s.taskID, "question", question)

		// Update task status to blocked
		s.db.UpdateTaskStatus(s.taskID, db.StatusBlocked)

		// Trigger callback
		if s.onNeedsInput != nil {
			s.onNeedsInput(question)
		}

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: "Input requested. The user will be notified."},
			},
		})

	case "taskyou_screenshot":
		filename, _ := params.Arguments["filename"].(string)
		description, _ := params.Arguments["description"].(string)

		// Create temp file for screenshot
		tmpFile, err := os.CreateTemp("", "screenshot-*.png")
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to create temp file: %v", err))
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		// Take screenshot based on OS
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			// macOS: use screencapture -x (silent, no sound)
			cmd = exec.Command("screencapture", "-x", tmpPath)
		case "linux":
			// Linux: try various screenshot tools
			// First try gnome-screenshot, then scrot, then import (ImageMagick)
			if _, err := exec.LookPath("gnome-screenshot"); err == nil {
				cmd = exec.Command("gnome-screenshot", "-f", tmpPath)
			} else if _, err := exec.LookPath("scrot"); err == nil {
				cmd = exec.Command("scrot", tmpPath)
			} else if _, err := exec.LookPath("import"); err == nil {
				cmd = exec.Command("import", "-window", "root", tmpPath)
			} else {
				s.sendError(id, -32603, "No screenshot tool found. Install gnome-screenshot, scrot, or imagemagick.")
				return
			}
		default:
			s.sendError(id, -32603, fmt.Sprintf("Screenshot not supported on %s", runtime.GOOS))
			return
		}

		// Run the screenshot command
		if output, err := cmd.CombinedOutput(); err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to take screenshot: %v - %s", err, string(output)))
			return
		}

		// Read the screenshot file
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to read screenshot: %v", err))
			return
		}

		// Generate filename if not provided
		if filename == "" {
			filename = fmt.Sprintf("screenshot-%s.png", time.Now().Format("20060102-150405"))
		} else {
			// Ensure the filename has .png extension
			if !strings.HasSuffix(strings.ToLower(filename), ".png") {
				filename += ".png"
			}
		}

		// Save as attachment
		attachment, err := s.db.AddAttachment(s.taskID, filename, "image/png", data)
		if err != nil {
			s.sendError(id, -32603, fmt.Sprintf("Failed to save screenshot: %v", err))
			return
		}

		// Log the screenshot
		logMsg := fmt.Sprintf("Screenshot captured: %s (%d bytes)", filename, len(data))
		if description != "" {
			logMsg = fmt.Sprintf("Screenshot captured: %s - %s (%d bytes)", filename, description, len(data))
		}
		s.db.AppendTaskLog(s.taskID, "system", logMsg)

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: fmt.Sprintf("Screenshot captured and saved as attachment #%d: %s (%d bytes)", attachment.ID, filename, len(data))},
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

		newTask := &db.Task{
			Title:   title,
			Body:    body,
			Project: project,
			Type:    taskType,
			Status:  status,
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

	case "taskyou_spotlight":
		action, _ := params.Arguments["action"].(string)
		if action == "" {
			s.sendError(id, -32602, "action is required")
			return
		}

		// Get current task
		task, err := s.db.GetTask(s.taskID)
		if err != nil || task == nil {
			s.sendError(id, -32603, "Failed to get current task")
			return
		}

		if task.WorktreePath == "" {
			s.sendError(id, -32602, "Task has no worktree (spotlight requires a worktree)")
			return
		}

		// Get the project directory (main repo)
		project, err := s.db.GetProjectByName(task.Project)
		if err != nil || project == nil {
			s.sendError(id, -32603, "Failed to get project directory")
			return
		}
		mainRepoDir := project.Path

		// Handle spotlight actions
		result, err := s.handleSpotlight(action, task.WorktreePath, mainRepoDir)
		if err != nil {
			s.sendError(id, -32603, err.Error())
			return
		}

		s.db.AppendTaskLog(s.taskID, "system", fmt.Sprintf("Spotlight %s: %s", action, result))

		s.sendResult(id, toolCallResult{
			Content: []contentBlock{
				{Type: "text", Text: result},
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

// spotlightStateFile returns the path to the spotlight state file in the worktree.
func spotlightStateFile(worktreePath string) string {
	return filepath.Join(worktreePath, ".spotlight-active")
}

// isSpotlightActive checks if spotlight mode is currently active for the worktree.
func isSpotlightActive(worktreePath string) bool {
	_, err := os.Stat(spotlightStateFile(worktreePath))
	return err == nil
}

// handleSpotlight handles spotlight mode operations.
func (s *Server) handleSpotlight(action, worktreePath, mainRepoDir string) (string, error) {
	switch action {
	case "start":
		return s.spotlightStart(worktreePath, mainRepoDir)
	case "stop":
		return s.spotlightStop(worktreePath, mainRepoDir)
	case "sync":
		if !isSpotlightActive(worktreePath) {
			return "", fmt.Errorf("spotlight mode is not active. Use 'start' to enable spotlight before syncing")
		}
		return s.spotlightSync(worktreePath, mainRepoDir)
	case "status":
		return s.spotlightStatus(worktreePath, mainRepoDir)
	default:
		return "", fmt.Errorf("unknown spotlight action: %s", action)
	}
}

// spotlightStart enables spotlight mode and performs initial sync.
func (s *Server) spotlightStart(worktreePath, mainRepoDir string) (string, error) {
	if isSpotlightActive(worktreePath) {
		return "Spotlight mode is already active. Use 'sync' to sync changes or 'stop' to disable.", nil
	}

	// Check if main repo has uncommitted changes using git diff --quiet (more reliable than parsing output)
	hasChanges := false
	diffCmd := exec.Command("git", "diff", "--quiet")
	diffCmd.Dir = mainRepoDir
	if err := diffCmd.Run(); err != nil {
		hasChanges = true // non-zero exit means changes exist
	}
	diffCachedCmd := exec.Command("git", "diff", "--cached", "--quiet")
	diffCachedCmd.Dir = mainRepoDir
	if err := diffCachedCmd.Run(); err != nil {
		hasChanges = true // staged changes exist
	}

	// Stash changes if any exist
	stashCreated := false
	if hasChanges {
		stashCmd := exec.Command("git", "stash", "push", "-m", "spotlight-backup-"+time.Now().Format("20060102-150405"))
		stashCmd.Dir = mainRepoDir
		if err := stashCmd.Run(); err == nil {
			stashCreated = true
		}
	}

	// Create the state file to track that spotlight is active
	stateContent := fmt.Sprintf("started=%s\nstash_created=%t\n", time.Now().Format(time.RFC3339), stashCreated)
	if err := os.WriteFile(spotlightStateFile(worktreePath), []byte(stateContent), 0644); err != nil {
		return "", fmt.Errorf("failed to create spotlight state file: %w", err)
	}

	// Perform initial sync
	syncResult, err := s.spotlightSync(worktreePath, mainRepoDir)
	if err != nil {
		// Clean up state file if sync failed
		os.Remove(spotlightStateFile(worktreePath))
		return "", err
	}

	msg := "🔦 Spotlight mode enabled!\n\n"
	if stashCreated {
		msg += "✓ Main repo changes stashed (will be restored on stop)\n"
	}
	msg += syncResult
	msg += "\n\nTip: Your main repo now has the worktree changes. Run your app from there for testing."
	msg += "\nUse 'sync' to push more changes or 'stop' when done."

	return msg, nil
}

// spotlightStop disables spotlight mode and restores the main repo.
func (s *Server) spotlightStop(worktreePath, mainRepoDir string) (string, error) {
	if !isSpotlightActive(worktreePath) {
		return "Spotlight mode is not active.", nil
	}

	// Read state file to check if we created a stash
	stateData, _ := os.ReadFile(spotlightStateFile(worktreePath))
	stashCreated := strings.Contains(string(stateData), "stash_created=true")

	// Restore the main repo to its original state
	// First, discard any uncommitted changes from spotlight
	checkoutCmd := exec.Command("git", "checkout", ".")
	checkoutCmd.Dir = mainRepoDir
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to restore main repo (git checkout): %s", strings.TrimSpace(string(output)))
	}

	// Clean any untracked files that were added
	cleanCmd := exec.Command("git", "clean", "-fd")
	cleanCmd.Dir = mainRepoDir
	if output, err := cleanCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to clean main repo (git clean): %s", strings.TrimSpace(string(output)))
	}

	// Pop the stash if we created one
	var stashMsg string
	stashPopFailed := false
	if stashCreated {
		stashPopCmd := exec.Command("git", "stash", "pop")
		stashPopCmd.Dir = mainRepoDir
		if output, err := stashPopCmd.CombinedOutput(); err != nil {
			stashPopFailed = true
			stashMsg = fmt.Sprintf("⚠️ Failed to restore stash: %s\n   Run 'git stash list' in %s to see available stashes.", strings.TrimSpace(string(output)), mainRepoDir)
		} else {
			stashMsg = "✓ Original main repo changes restored from stash"
		}
	}

	// Only remove state file if restoration succeeded (including stash pop)
	if !stashPopFailed {
		os.Remove(spotlightStateFile(worktreePath))
	}

	msg := "🔦 Spotlight mode disabled!\n\n"
	msg += "✓ Main repo restored to original state\n"
	if stashMsg != "" {
		msg += stashMsg + "\n"
	}
	if stashPopFailed {
		msg += "\n⚠️ State file preserved due to stash pop failure. Run 'stop' again after resolving."
	}

	return msg, nil
}

// spotlightSync syncs git-tracked files from worktree to main repo.
// It compares files between the worktree and main repo, copying any that differ.
// Also handles file deletions by detecting files that exist in main but not in worktree.
func (s *Server) spotlightSync(worktreePath, mainRepoDir string) (string, error) {
	// Get list of all git-tracked files in the worktree
	lsFilesCmd := exec.Command("git", "ls-files")
	lsFilesCmd.Dir = worktreePath
	lsFilesOutput, err := lsFilesCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list tracked files: %w", err)
	}

	// Also get untracked files (new files not yet added)
	untrackedCmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = worktreePath
	untrackedOutput, _ := untrackedCmd.Output()

	// Get deleted files (tracked but removed from worktree)
	deletedCmd := exec.Command("git", "diff", "--name-only", "--diff-filter=D", "HEAD")
	deletedCmd.Dir = worktreePath
	deletedOutput, _ := deletedCmd.Output()

	// Build set of all files to sync
	fileSet := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(string(lsFilesOutput)), "\n") {
		if file != "" {
			fileSet[file] = true
		}
	}
	for _, file := range strings.Split(strings.TrimSpace(string(untrackedOutput)), "\n") {
		if file != "" {
			fileSet[file] = true
		}
	}

	// Build set of deleted files
	deletedSet := make(map[string]bool)
	for _, file := range strings.Split(strings.TrimSpace(string(deletedOutput)), "\n") {
		if file != "" {
			deletedSet[file] = true
		}
	}

	// Clean paths for validation
	cleanWorktree := filepath.Clean(worktreePath)
	cleanMainRepo := filepath.Clean(mainRepoDir)

	// Copy files that differ between worktree and main repo
	var synced, unchanged, deleted, failed int
	for file := range fileSet {
		if file == ".spotlight-active" || file == "" {
			continue
		}

		// Validate path to prevent path traversal attacks
		cleanFile := filepath.Clean(file)
		if cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanFile) {
			failed++
			continue
		}

		srcPath := filepath.Join(cleanWorktree, cleanFile)
		dstPath := filepath.Join(cleanMainRepo, cleanFile)

		// Ensure destination is within mainRepoDir
		if !strings.HasPrefix(filepath.Clean(dstPath), cleanMainRepo+string(os.PathSeparator)) && filepath.Clean(dstPath) != cleanMainRepo {
			failed++
			continue
		}

		// Check if source exists
		srcInfo, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File tracked but doesn't exist - skip
				continue
			}
			failed++
			continue
		}

		// Skip directories
		if srcInfo.IsDir() {
			continue
		}

		// Read source file
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			failed++
			continue
		}

		// Check if destination exists and is the same (use bytes.Equal for efficiency)
		dstData, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(srcData, dstData) {
			unchanged++
			continue
		}

		// Ensure destination directory exists
		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			failed++
			continue
		}

		// Copy the file
		if err := os.WriteFile(dstPath, srcData, srcInfo.Mode()); err != nil {
			failed++
			continue
		}

		synced++
	}

	// Handle deleted files - remove them from main repo
	for file := range deletedSet {
		if file == "" {
			continue
		}

		cleanFile := filepath.Clean(file)
		if cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanFile) {
			continue
		}

		dstPath := filepath.Join(cleanMainRepo, cleanFile)
		if !strings.HasPrefix(filepath.Clean(dstPath), cleanMainRepo+string(os.PathSeparator)) {
			continue
		}

		if err := os.Remove(dstPath); err == nil {
			deleted++
		}
	}

	if synced == 0 && deleted == 0 && failed == 0 {
		return "No changes to sync (worktree matches main repo).", nil
	}

	result := fmt.Sprintf("✓ Synced %d file(s) from worktree to main repo", synced)
	if deleted > 0 {
		result += fmt.Sprintf(", deleted %d", deleted)
	}
	if unchanged > 0 {
		result += fmt.Sprintf(" (%d unchanged)", unchanged)
	}
	if failed > 0 {
		result += fmt.Sprintf(" (%d failed)", failed)
	}

	return result, nil
}

// spotlightStatus returns the current spotlight status.
func (s *Server) spotlightStatus(worktreePath, mainRepoDir string) (string, error) {
	if !isSpotlightActive(worktreePath) {
		return "🔦 Spotlight mode: INACTIVE\n\nUse 'start' to enable spotlight mode and sync worktree changes to the main repo for testing.", nil
	}

	// Read state file for details
	stateData, _ := os.ReadFile(spotlightStateFile(worktreePath))

	// Count pending changes
	diffCmd := exec.Command("git", "diff", "--name-only", "HEAD")
	diffCmd.Dir = worktreePath
	diffOutput, _ := diffCmd.Output()
	changedCount := len(strings.Split(strings.TrimSpace(string(diffOutput)), "\n"))
	if strings.TrimSpace(string(diffOutput)) == "" {
		changedCount = 0
	}

	msg := "🔦 Spotlight mode: ACTIVE\n\n"
	msg += fmt.Sprintf("Worktree: %s\n", worktreePath)
	msg += fmt.Sprintf("Main repo: %s\n", mainRepoDir)
	if len(stateData) > 0 {
		for _, line := range strings.Split(string(stateData), "\n") {
			if strings.HasPrefix(line, "started=") {
				msg += fmt.Sprintf("Started: %s\n", strings.TrimPrefix(line, "started="))
			}
		}
	}
	msg += fmt.Sprintf("\nPending changes: %d file(s)\n", changedCount)
	msg += "\nUse 'sync' to push changes or 'stop' to disable and restore main repo."

	return msg, nil
}

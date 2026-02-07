// Package ai provides LLM-powered command interpretation for task management.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// CommandType represents the type of action to perform.
type CommandType string

const (
	CommandCreateTask   CommandType = "create_task"
	CommandUpdateStatus CommandType = "update_status"
	CommandSelectTask   CommandType = "select_task"
	CommandSearchTasks  CommandType = "search_tasks"
	CommandUnknown      CommandType = "unknown"
)

// Command represents a parsed command from user input.
type Command struct {
	Type    CommandType `json:"type"`
	TaskID  int64       `json:"task_id,omitempty"`
	Title   string      `json:"title,omitempty"`
	Body    string      `json:"body,omitempty"`
	Status  string      `json:"status,omitempty"`
	Project string      `json:"project,omitempty"`
	Query   string      `json:"query,omitempty"`
	Message string      `json:"message,omitempty"` // Human-readable response
}

// CommandService handles AI command interpretation.
type CommandService struct {
	apiKey     string
	httpClient *http.Client
}

// NewCommandService creates a new command service.
func NewCommandService(apiKey string) *CommandService {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}

	return &CommandService{
		apiKey: apiKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}
}

// IsAvailable returns true if the service has an API key.
func (s *CommandService) IsAvailable() bool {
	return s.apiKey != ""
}

// Context provides information about the current state for command interpretation.
type Context struct {
	Tasks    []*db.Task
	Projects []*db.Project
}

// InterpretCommand uses an LLM to interpret a natural language command.
func (s *CommandService) InterpretCommand(ctx context.Context, input string, cmdCtx *Context) (*Command, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("no API key available")
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty input")
	}

	prompt := s.buildPrompt(input, cmdCtx)
	response, err := s.callAPI(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return s.parseResponse(response, input)
}

func (s *CommandService) buildPrompt(input string, cmdCtx *Context) string {
	var sb strings.Builder

	sb.WriteString(`You are a task management command interpreter. Interpret the user's natural language input and return a JSON command.

Available command types:
- create_task: Create a new task
- update_status: Change a task's status (statuses: backlog, queued, processing, blocked, done, archived)
- select_task: Jump to/open a specific task
- search_tasks: Search for tasks matching criteria
- unknown: When intent is unclear

Return a JSON object with these fields:
{
  "type": "<command_type>",
  "task_id": <number if applicable>,
  "title": "<task title for create_task>",
  "body": "<task description if provided>",
  "status": "<status for update_status>",
  "project": "<project name if specified>",
  "query": "<search query for search_tasks>",
  "message": "<brief human-readable response describing what will happen>"
}

`)

	// Add available projects
	if cmdCtx != nil && len(cmdCtx.Projects) > 0 {
		sb.WriteString("Available projects:\n")
		for _, p := range cmdCtx.Projects {
			sb.WriteString(fmt.Sprintf("- %s\n", p.Name))
		}
		sb.WriteString("\n")
	}

	// Add sample of recent tasks for context
	if cmdCtx != nil && len(cmdCtx.Tasks) > 0 {
		sb.WriteString("Recent tasks (for reference):\n")
		count := min(10, len(cmdCtx.Tasks))
		for i := 0; i < count; i++ {
			t := cmdCtx.Tasks[i]
			sb.WriteString(fmt.Sprintf("- #%d [%s] %s (%s)\n", t.ID, t.Project, t.Title, t.Status))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Examples:\n")
	sb.WriteString(`- "create task about fixing auth bug" -> {"type":"create_task","title":"Fix auth bug","message":"Creating task: Fix auth bug"}
- "move #42 to done" -> {"type":"update_status","task_id":42,"status":"done","message":"Marking task #42 as done"}
- "close task 15" -> {"type":"update_status","task_id":15,"status":"done","message":"Closing task #15"}
- "new task in offerlab: add dark mode" -> {"type":"create_task","title":"Add dark mode","project":"offerlab","message":"Creating task in offerlab: Add dark mode"}
- "go to task 7" -> {"type":"select_task","task_id":7,"message":"Opening task #7"}
- "find tasks about authentication" -> {"type":"search_tasks","query":"authentication","message":"Searching for tasks about authentication"}
- "queue task #20" -> {"type":"update_status","task_id":20,"status":"queued","message":"Queuing task #20"}
- "archive #5" -> {"type":"update_status","task_id":5,"status":"archived","message":"Archiving task #5"}

`)

	sb.WriteString(fmt.Sprintf("User input: %s\n\nRespond with only the JSON object, no additional text.", input))

	return sb.String()
}

// Anthropic API types
type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Message string `json:"message"`
}

func (s *CommandService) callAPI(ctx context.Context, prompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 200,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error: %d - %s", resp.StatusCode, string(body))
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return apiResp.Content[0].Text, nil
}

func (s *CommandService) parseResponse(response, originalInput string) (*Command, error) {
	response = strings.TrimSpace(response)

	// Try to extract JSON from the response (sometimes the model adds extra text)
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		response = response[jsonStart : jsonEnd+1]
	}

	var cmd Command
	if err := json.Unmarshal([]byte(response), &cmd); err != nil {
		// If parsing fails, treat it as a task search with the original input
		return &Command{
			Type:    CommandSearchTasks,
			Query:   originalInput,
			Message: fmt.Sprintf("Searching for: %s", originalInput),
		}, nil
	}

	// Validate and normalize the command
	switch cmd.Type {
	case CommandCreateTask:
		if cmd.Title == "" {
			cmd.Title = originalInput
		}
		if cmd.Message == "" {
			if cmd.Project != "" {
				cmd.Message = fmt.Sprintf("Creating task in %s: %s", cmd.Project, cmd.Title)
			} else {
				cmd.Message = fmt.Sprintf("Creating task: %s", cmd.Title)
			}
		}
	case CommandUpdateStatus:
		if cmd.TaskID == 0 {
			// Try to extract task ID from original input
			cmd.TaskID = extractTaskID(originalInput)
		}
		if cmd.Status == "" {
			cmd.Status = db.StatusDone
		}
		if cmd.Message == "" {
			cmd.Message = fmt.Sprintf("Updating task #%d to %s", cmd.TaskID, cmd.Status)
		}
	case CommandSelectTask:
		if cmd.TaskID == 0 {
			cmd.TaskID = extractTaskID(originalInput)
		}
		if cmd.Message == "" {
			cmd.Message = fmt.Sprintf("Opening task #%d", cmd.TaskID)
		}
	case CommandSearchTasks:
		if cmd.Query == "" {
			cmd.Query = originalInput
		}
		if cmd.Message == "" {
			cmd.Message = fmt.Sprintf("Searching for: %s", cmd.Query)
		}
	default:
		cmd.Type = CommandUnknown
		if cmd.Message == "" {
			cmd.Message = "I couldn't understand that command. Try something like 'create task about X' or 'move #123 to done'."
		}
	}

	return &cmd, nil
}

// extractTaskID tries to find a task ID in the input string.
func extractTaskID(input string) int64 {
	// Look for #N or just a number
	words := strings.Fields(input)
	for _, word := range words {
		word = strings.TrimPrefix(word, "#")
		if id, err := strconv.ParseInt(word, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return 0
}

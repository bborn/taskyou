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
	"sync"

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
	ProtocolVersion string            `json:"protocolVersion"`
	ServerInfo      serverInfo        `json:"serverInfo"`
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
				Name:    "workflow-mcp",
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
					Name:        "workflow_complete",
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
					Name:        "workflow_needs_input",
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
	case "workflow_complete":
		summary, _ := params.Arguments["summary"].(string)

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
				{Type: "text", Text: "Task marked as complete."},
			},
		})

	case "workflow_needs_input":
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

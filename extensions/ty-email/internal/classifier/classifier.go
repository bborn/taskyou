// Package classifier provides LLM-based email classification.
package classifier

import (
	"context"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
)

// Action represents the classified intent of an email.
type Action struct {
	// Type of action to take
	Type ActionType `json:"type"`

	// For "create" action
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	Project string `json:"project,omitempty"`
	TaskType string `json:"task_type,omitempty"` // code, writing, thinking
	Execute bool   `json:"execute,omitempty"`    // Queue immediately

	// For "input" action
	TaskID    int64  `json:"task_id,omitempty"`
	InputText string `json:"input_text,omitempty"`

	// For "query" action (status check, list)
	Query string `json:"query,omitempty"`

	// Reply to send back to the sender
	Reply string `json:"reply,omitempty"`

	// Reasoning for the classification (for debugging)
	Reasoning string `json:"reasoning,omitempty"`

	// Confidence score (0-1)
	Confidence float64 `json:"confidence,omitempty"`
}

// ActionType represents the type of action to take.
type ActionType string

const (
	ActionCreate  ActionType = "create"  // Create a new task
	ActionInput   ActionType = "input"   // Provide input to a blocked task
	ActionExecute ActionType = "execute" // Queue a task for execution
	ActionQuery   ActionType = "query"   // Query task status
	ActionIgnore  ActionType = "ignore"  // Ignore the email (spam, irrelevant)
)

// Task represents a TaskYou task (for context).
type Task struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Project string `json:"project"`
	Body    string `json:"body,omitempty"`
}

// Classifier understands email intent and translates to TaskYou actions.
type Classifier interface {
	// Name returns the classifier name (e.g., "claude", "openai").
	Name() string

	// Classify analyzes an email and returns the action to take.
	// The tasks parameter provides context about current tasks.
	Classify(ctx context.Context, email *adapter.Email, tasks []Task, threadTaskID *int64) (*Action, error)

	// IsAvailable checks if the classifier is properly configured.
	IsAvailable() bool
}

// Config holds classifier configuration.
type Config struct {
	Provider  string `yaml:"provider"`      // "claude", "openai", "ollama"
	Model     string `yaml:"model"`         // Model name/ID
	APIKeyCmd string `yaml:"api_key_cmd"`   // Command to get API key
	APIKey    string `yaml:"api_key"`       // Direct API key (less secure)
	BaseURL   string `yaml:"base_url"`      // For Ollama or custom endpoints
}

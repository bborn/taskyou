// Package compute provides the interface and implementations for ephemeral compute platforms.
package compute

import (
	"context"
	"time"
)

// WorkflowRun represents a single workflow execution.
type WorkflowRun struct {
	ID          string            `json:"id"`
	WorkflowID  string            `json:"workflow_id"`
	Status      RunStatus         `json:"status"`
	Input       map[string]any    `json:"input"`
	Output      map[string]any    `json:"output,omitempty"`
	Error       string            `json:"error,omitempty"`
	Steps       []StepAttempt     `json:"steps,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// RunStatus represents the current state of a workflow run.
type RunStatus string

const (
	StatusPending   RunStatus = "pending"
	StatusRunning   RunStatus = "running"
	StatusSleeping  RunStatus = "sleeping"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
	StatusCanceled  RunStatus = "canceled"
)

// StepAttempt records the execution of a single workflow step.
type StepAttempt struct {
	ID          string     `json:"id"`
	StepName    string     `json:"step_name"`
	StepType    StepType   `json:"step_type"`
	Status      RunStatus  `json:"status"`
	Input       any        `json:"input,omitempty"`
	Output      any        `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	RetryCount  int        `json:"retry_count"`
}

// StepType indicates what kind of step this is.
type StepType string

const (
	StepTypeRun   StepType = "run"
	StepTypeSleep StepType = "sleep"
	StepTypeWait  StepType = "wait"
)

// WorkflowDefinition describes a workflow that can be executed.
type WorkflowDefinition struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version"`
	Code        string         `json:"code"` // The workflow code to execute
	Runtime     string         `json:"runtime"` // e.g., "node", "python", "go"
	Timeout     time.Duration  `json:"timeout,omitempty"`
	MaxRetries  int            `json:"max_retries,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Adapter defines the interface for ephemeral compute platforms.
// Implementations include Cloudflare Workers, local exec, Docker, Modal, etc.
type Adapter interface {
	// Name returns the adapter identifier.
	Name() string

	// IsAvailable checks if the compute platform is configured and accessible.
	IsAvailable() bool

	// Deploy uploads a workflow definition to the compute platform.
	Deploy(ctx context.Context, workflow *WorkflowDefinition) error

	// Start initiates a new workflow run.
	Start(ctx context.Context, workflowID string, input map[string]any) (*WorkflowRun, error)

	// Status retrieves the current status of a workflow run.
	Status(ctx context.Context, runID string) (*WorkflowRun, error)

	// Cancel attempts to cancel a running workflow.
	Cancel(ctx context.Context, runID string) error

	// Logs retrieves execution logs for a workflow run.
	Logs(ctx context.Context, runID string) ([]string, error)

	// Cleanup removes completed workflow resources.
	Cleanup(ctx context.Context, runID string) error

	// ListRuns returns recent workflow runs, optionally filtered.
	ListRuns(ctx context.Context, workflowID string, limit int) ([]*WorkflowRun, error)

	// SetWebhook configures the callback URL for workflow completion.
	SetWebhook(url string)
}

// Factory creates compute adapters based on configuration.
type Factory struct {
	adapters map[string]Adapter
}

// NewFactory creates a new adapter factory.
func NewFactory() *Factory {
	return &Factory{
		adapters: make(map[string]Adapter),
	}
}

// Register adds an adapter to the factory.
func (f *Factory) Register(adapter Adapter) {
	f.adapters[adapter.Name()] = adapter
}

// Get retrieves an adapter by name.
func (f *Factory) Get(name string) Adapter {
	return f.adapters[name]
}

// Available returns the names of all registered adapters.
func (f *Factory) Available() []string {
	names := make([]string, 0, len(f.adapters))
	for name, adapter := range f.adapters {
		if adapter.IsAvailable() {
			names = append(names, name)
		}
	}
	return names
}

// All returns all registered adapters.
func (f *Factory) All() []Adapter {
	adapters := make([]Adapter, 0, len(f.adapters))
	for _, adapter := range f.adapters {
		adapters = append(adapters, adapter)
	}
	return adapters
}

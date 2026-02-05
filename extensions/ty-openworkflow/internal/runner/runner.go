// Package runner orchestrates workflow execution and TaskYou integration.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/compute"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/state"
)

// Runner manages workflow execution and task integration.
type Runner struct {
	factory   *compute.Factory
	state     *state.DB
	bridge    *bridge.Bridge
	webhook   string
	pollInterval time.Duration
	mu        sync.RWMutex
}

// Config holds runner configuration.
type Config struct {
	WebhookURL   string
	PollInterval time.Duration
}

// NewRunner creates a new workflow runner.
func NewRunner(factory *compute.Factory, stateDB *state.DB, br *bridge.Bridge, cfg Config) *Runner {
	interval := cfg.PollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	return &Runner{
		factory:      factory,
		state:        stateDB,
		bridge:       br,
		webhook:      cfg.WebhookURL,
		pollInterval: interval,
	}
}

// DeployWorkflow deploys a workflow to the specified adapter.
func (r *Runner) DeployWorkflow(ctx context.Context, workflow *compute.WorkflowDefinition, adapterName string) error {
	adapter := r.factory.Get(adapterName)
	if adapter == nil {
		return fmt.Errorf("adapter not found: %s", adapterName)
	}

	if !adapter.IsAvailable() {
		return fmt.Errorf("adapter not available: %s", adapterName)
	}

	// Set webhook for callbacks
	if r.webhook != "" {
		adapter.SetWebhook(r.webhook)
	}

	// Deploy to compute platform
	if err := adapter.Deploy(ctx, workflow); err != nil {
		return fmt.Errorf("deploy workflow: %w", err)
	}

	// Save to state
	stateWorkflow := &state.Workflow{
		ID:          workflow.ID,
		Name:        workflow.Name,
		Description: workflow.Description,
		Version:     workflow.Version,
		Runtime:     workflow.Runtime,
		Code:        workflow.Code,
		Adapter:     adapterName,
	}

	if err := r.state.SaveWorkflow(stateWorkflow); err != nil {
		return fmt.Errorf("save workflow state: %w", err)
	}

	return nil
}

// StartWorkflow starts a workflow run, optionally linked to a task.
func (r *Runner) StartWorkflow(ctx context.Context, workflowID string, input map[string]any, taskID int64) (*compute.WorkflowRun, error) {
	// Get workflow from state
	workflow, err := r.state.GetWorkflow(workflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	if workflow == nil {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}

	// Get adapter
	adapter := r.factory.Get(workflow.Adapter)
	if adapter == nil {
		return nil, fmt.Errorf("adapter not found: %s", workflow.Adapter)
	}

	// Set webhook
	if r.webhook != "" {
		adapter.SetWebhook(r.webhook)
	}

	// Start workflow
	run, err := adapter.Start(ctx, workflowID, input)
	if err != nil {
		return nil, fmt.Errorf("start workflow: %w", err)
	}

	// Save run to state
	inputJSON, _ := json.Marshal(input)
	stateRun := &state.WorkflowRun{
		ID:         run.ID,
		WorkflowID: workflowID,
		TaskID:     taskID,
		Adapter:    workflow.Adapter,
		Status:     string(run.Status),
		Input:      string(inputJSON),
		StartedAt:  run.StartedAt,
	}

	if err := r.state.SaveRun(stateRun); err != nil {
		return nil, fmt.Errorf("save run state: %w", err)
	}

	// Link task to workflow if provided
	if taskID > 0 {
		if err := r.state.LinkTaskToWorkflow(taskID, workflowID, run.ID); err != nil {
			log.Printf("Warning: failed to link task to workflow: %v", err)
		}
	}

	return run, nil
}

// StartWorkflowWithTask creates a task and starts a workflow linked to it.
func (r *Runner) StartWorkflowWithTask(ctx context.Context, workflowID string, input map[string]any, taskTitle string) (*compute.WorkflowRun, *bridge.Task, error) {
	// Create task in TaskYou
	body := fmt.Sprintf("Workflow: %s\nInput: %v", workflowID, input)
	task, err := r.bridge.CreateTask(taskTitle, body, "code")
	if err != nil {
		return nil, nil, fmt.Errorf("create task: %w", err)
	}

	// Start workflow with task ID
	run, err := r.StartWorkflow(ctx, workflowID, input, task.ID)
	if err != nil {
		return nil, task, fmt.Errorf("start workflow: %w", err)
	}

	return run, task, nil
}

// GetRunStatus returns the current status of a workflow run.
func (r *Runner) GetRunStatus(ctx context.Context, runID string) (*compute.WorkflowRun, error) {
	// Get from state
	stateRun, err := r.state.GetRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get run from state: %w", err)
	}
	if stateRun == nil {
		return nil, fmt.Errorf("run not found: %s", runID)
	}

	// If terminal state, return from state
	if stateRun.Status == "completed" || stateRun.Status == "failed" || stateRun.Status == "canceled" {
		var output map[string]any
		if stateRun.Output != "" {
			json.Unmarshal([]byte(stateRun.Output), &output)
		}
		var input map[string]any
		if stateRun.Input != "" {
			json.Unmarshal([]byte(stateRun.Input), &input)
		}

		return &compute.WorkflowRun{
			ID:          stateRun.ID,
			WorkflowID:  stateRun.WorkflowID,
			Status:      compute.RunStatus(stateRun.Status),
			Input:       input,
			Output:      output,
			Error:       stateRun.Error,
			StartedAt:   stateRun.StartedAt,
			CompletedAt: stateRun.CompletedAt,
		}, nil
	}

	// Get live status from adapter
	adapter := r.factory.Get(stateRun.Adapter)
	if adapter == nil {
		return nil, fmt.Errorf("adapter not found: %s", stateRun.Adapter)
	}

	run, err := adapter.Status(ctx, runID)
	if err != nil {
		// Return state-based status if adapter check fails
		var input map[string]any
		if stateRun.Input != "" {
			json.Unmarshal([]byte(stateRun.Input), &input)
		}
		return &compute.WorkflowRun{
			ID:         stateRun.ID,
			WorkflowID: stateRun.WorkflowID,
			Status:     compute.RunStatus(stateRun.Status),
			Input:      input,
			StartedAt:  stateRun.StartedAt,
		}, nil
	}

	return run, nil
}

// CancelRun cancels a workflow run.
func (r *Runner) CancelRun(ctx context.Context, runID string) error {
	stateRun, err := r.state.GetRun(runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if stateRun == nil {
		return fmt.Errorf("run not found: %s", runID)
	}

	adapter := r.factory.Get(stateRun.Adapter)
	if adapter == nil {
		return fmt.Errorf("adapter not found: %s", stateRun.Adapter)
	}

	if err := adapter.Cancel(ctx, runID); err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}

	// Update state
	if err := r.state.UpdateRunStatus(runID, "canceled", "", ""); err != nil {
		return fmt.Errorf("update run status: %w", err)
	}

	// Update linked task if any
	if stateRun.TaskID > 0 {
		if err := r.bridge.FailTask(stateRun.TaskID, "Workflow canceled"); err != nil {
			log.Printf("Warning: failed to update task status: %v", err)
		}
	}

	return nil
}

// HandleWebhook processes incoming webhook callbacks from compute platforms.
func (r *Runner) HandleWebhook(runID, status string, output map[string]any, errMsg string) error {
	stateRun, err := r.state.GetRun(runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if stateRun == nil {
		return fmt.Errorf("run not found: %s", runID)
	}

	// Update state
	outputJSON := ""
	if output != nil {
		data, _ := json.Marshal(output)
		outputJSON = string(data)
	}

	if err := r.state.UpdateRunStatus(runID, status, outputJSON, errMsg); err != nil {
		return fmt.Errorf("update run status: %w", err)
	}

	// Update linked task
	if stateRun.TaskID > 0 {
		switch status {
		case "completed":
			result := "Workflow completed"
			if outputJSON != "" {
				result = fmt.Sprintf("Workflow completed. Output: %s", outputJSON)
			}
			if err := r.bridge.CompleteTask(stateRun.TaskID, result); err != nil {
				log.Printf("Warning: failed to complete task: %v", err)
			}
		case "failed":
			if err := r.bridge.FailTask(stateRun.TaskID, errMsg); err != nil {
				log.Printf("Warning: failed to fail task: %v", err)
			}
		}
	}

	return nil
}

// ListWorkflows returns all deployed workflows.
func (r *Runner) ListWorkflows() ([]*state.Workflow, error) {
	return r.state.ListWorkflows()
}

// ListRuns returns workflow runs.
func (r *Runner) ListRuns(workflowID, status string, limit int) ([]*state.WorkflowRun, error) {
	return r.state.ListRuns(workflowID, status, limit)
}

// GetWorkflow retrieves a workflow by ID.
func (r *Runner) GetWorkflow(workflowID string) (*state.Workflow, error) {
	return r.state.GetWorkflow(workflowID)
}

// DeleteWorkflow removes a workflow.
func (r *Runner) DeleteWorkflow(workflowID string) error {
	return r.state.DeleteWorkflow(workflowID)
}

// PollPendingRuns checks for and updates pending workflow runs.
func (r *Runner) PollPendingRuns(ctx context.Context) error {
	runs, err := r.state.GetPendingRuns()
	if err != nil {
		return fmt.Errorf("get pending runs: %w", err)
	}

	for _, stateRun := range runs {
		adapter := r.factory.Get(stateRun.Adapter)
		if adapter == nil {
			continue
		}

		run, err := adapter.Status(ctx, stateRun.ID)
		if err != nil {
			log.Printf("Warning: failed to get run status for %s: %v", stateRun.ID, err)
			continue
		}

		// Update if status changed
		if string(run.Status) != stateRun.Status {
			outputJSON := ""
			if run.Output != nil {
				data, _ := json.Marshal(run.Output)
				outputJSON = string(data)
			}

			if err := r.state.UpdateRunStatus(stateRun.ID, string(run.Status), outputJSON, run.Error); err != nil {
				log.Printf("Warning: failed to update run status: %v", err)
				continue
			}

			// Update linked task
			if stateRun.TaskID > 0 {
				switch run.Status {
				case compute.StatusCompleted:
					result := "Workflow completed"
					if outputJSON != "" {
						result = fmt.Sprintf("Workflow completed. Output: %s", outputJSON)
					}
					r.bridge.CompleteTask(stateRun.TaskID, result)
				case compute.StatusFailed:
					r.bridge.FailTask(stateRun.TaskID, run.Error)
				}
			}
		}
	}

	return nil
}

// StartPolling begins background polling for run status updates.
func (r *Runner) StartPolling(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.PollPendingRuns(ctx); err != nil {
				log.Printf("Warning: poll pending runs failed: %v", err)
			}
		}
	}
}

// GetRunForTask retrieves the workflow run associated with a task.
func (r *Runner) GetRunForTask(taskID int64) (*state.WorkflowRun, error) {
	return r.state.GetRunByTaskID(taskID)
}

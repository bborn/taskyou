// Package bridge provides the interface to TaskYou via the ty CLI.
package bridge

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/bborn/workflow/extensions/ty-email/internal/classifier"
)

// Bridge executes TaskYou commands via the ty CLI.
type Bridge struct {
	tyPath string
}

// New creates a new bridge to TaskYou.
func New(tyPath string) *Bridge {
	if tyPath == "" {
		tyPath = "ty"
	}
	return &Bridge{tyPath: tyPath}
}

// Task represents a task from TaskYou.
type Task struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Status  string `json:"status"`
	Project string `json:"project"`
	Type    string `json:"type"`
}

// CreateResult is returned when creating a task.
type CreateResult struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Project string `json:"project"`
}

// ListTasks returns current tasks, optionally filtered by status.
func (b *Bridge) ListTasks(status string) ([]Task, error) {
	args := []string{"list", "--json"}
	if status != "" {
		args = append(args, "--status", status)
	}
	// Include all active tasks for context
	args = append(args, "--all")

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	if err := json.Unmarshal(out, &tasks); err != nil {
		// Try to parse as empty or error
		if strings.TrimSpace(string(out)) == "" || strings.TrimSpace(string(out)) == "[]" {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("failed to parse tasks: %w", err)
	}

	return tasks, nil
}

// GetTask returns a specific task by ID.
func (b *Bridge) GetTask(id int64) (*Task, error) {
	out, err := b.run("show", strconv.FormatInt(id, 10), "--json")
	if err != nil {
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(out, &task); err != nil {
		return nil, fmt.Errorf("failed to parse task: %w", err)
	}

	return &task, nil
}

// CreateTask creates a new task and returns its ID.
func (b *Bridge) CreateTask(action *classifier.Action) (*CreateResult, error) {
	args := []string{"create", action.Title, "--json"}

	if action.Body != "" {
		args = append(args, "--body", action.Body)
	}
	if action.Project != "" {
		args = append(args, "--project", action.Project)
	}
	if action.TaskType != "" {
		args = append(args, "--type", action.TaskType)
	}
	if action.Execute {
		args = append(args, "--execute")
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var result CreateResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse create result: %w", err)
	}

	return &result, nil
}

// SendInput sends input to a blocked task.
func (b *Bridge) SendInput(taskID int64, input string) error {
	_, err := b.run("input", strconv.FormatInt(taskID, 10), input)
	return err
}

// ExecuteTask queues a task for execution.
func (b *Bridge) ExecuteTask(taskID int64) error {
	_, err := b.run("execute", strconv.FormatInt(taskID, 10))
	return err
}

// GetBlockedTasks returns tasks that are waiting for input.
func (b *Bridge) GetBlockedTasks() ([]Task, error) {
	return b.ListTasks("blocked")
}

// GetTaskOutput returns recent output from a task.
func (b *Bridge) GetTaskOutput(taskID int64, lines int) (string, error) {
	args := []string{"output", strconv.FormatInt(taskID, 10)}
	if lines > 0 {
		args = append(args, "--last", strconv.Itoa(lines))
	}

	out, err := b.run(args...)
	if err != nil {
		return "", err
	}

	return string(out), nil
}

// run executes a ty command and returns the output.
func (b *Bridge) run(args ...string) ([]byte, error) {
	cmd := exec.Command(b.tyPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ty %s failed: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("ty %s failed: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// IsAvailable checks if the ty CLI is available.
func (b *Bridge) IsAvailable() bool {
	_, err := exec.LookPath(b.tyPath)
	return err == nil
}

// ToClassifierTasks converts bridge tasks to classifier tasks.
func ToClassifierTasks(tasks []Task) []classifier.Task {
	result := make([]classifier.Task, len(tasks))
	for i, t := range tasks {
		result[i] = classifier.Task{
			ID:      t.ID,
			Title:   t.Title,
			Status:  t.Status,
			Project: t.Project,
			Body:    t.Body,
		}
	}
	return result
}

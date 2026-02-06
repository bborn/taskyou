// Package bridge provides the interface to TaskYou via the ty CLI.
package bridge

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

// FeedbackInput holds the data needed to create a task from feedback.
type FeedbackInput struct {
	Title   string
	Body    string
	Project string
	Type    string
	Tags    string
	Execute bool
}

// CreateTask creates a new task from feedback.
func (b *Bridge) CreateTask(input *FeedbackInput) (*CreateResult, error) {
	args := []string{"create", input.Title, "--json"}

	if input.Body != "" {
		args = append(args, "--body", input.Body)
	}
	if input.Project != "" {
		args = append(args, "--project", input.Project)
	}
	if input.Type != "" {
		args = append(args, "--type", input.Type)
	}
	if input.Tags != "" {
		args = append(args, "--tags", input.Tags)
	}
	if input.Execute {
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

// ListTasks returns current tasks, optionally filtered by project and status.
func (b *Bridge) ListTasks(project, status string) ([]Task, error) {
	args := []string{"list", "--json", "--all"}
	if status != "" {
		args = append(args, "--status", status)
	}

	out, err := b.run(args...)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	if err := json.Unmarshal(out, &tasks); err != nil {
		if strings.TrimSpace(string(out)) == "" || strings.TrimSpace(string(out)) == "[]" {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("failed to parse tasks: %w", err)
	}

	// Filter by project if specified
	if project != "" {
		var filtered []Task
		for _, t := range tasks {
			if t.Project == project {
				filtered = append(filtered, t)
			}
		}
		return filtered, nil
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

// SendInput sends input to a blocked task.
func (b *Bridge) SendInput(taskID int64, input string) error {
	_, err := b.run("input", strconv.FormatInt(taskID, 10), input)
	return err
}

// IsAvailable checks if the ty CLI is available.
func (b *Bridge) IsAvailable() bool {
	_, err := exec.LookPath(b.tyPath)
	return err == nil
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

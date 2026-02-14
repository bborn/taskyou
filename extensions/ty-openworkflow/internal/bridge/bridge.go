// Package bridge provides the interface to communicate with TaskYou via CLI.
package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Bridge communicates with TaskYou via the ty CLI.
type Bridge struct {
	tyPath  string
	project string
}

// Task represents a TaskYou task.
type Task struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Status      string `json:"status"`
	Type        string `json:"type"`
	Project     string `json:"project"`
	CreatedAt   string `json:"created_at"`
	ExecutingAt string `json:"executing_at,omitempty"`
}

// NewBridge creates a new TaskYou bridge.
func NewBridge(tyPath, project string) *Bridge {
	if tyPath == "" {
		tyPath = "ty"
	}
	return &Bridge{
		tyPath:  tyPath,
		project: project,
	}
}

// CreateTask creates a new task in TaskYou.
func (b *Bridge) CreateTask(title, body, taskType string) (*Task, error) {
	args := []string{"create"}

	if title != "" {
		args = append(args, "--title", title)
	}
	if body != "" {
		args = append(args, "--body", body)
	}
	if taskType != "" {
		args = append(args, "--type", taskType)
	}
	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	args = append(args, "--json")

	cmd := exec.Command(b.tyPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("create task: %s", stderr.String())
	}

	var task Task
	if err := json.Unmarshal(stdout.Bytes(), &task); err != nil {
		// Try to parse task ID from output
		output := strings.TrimSpace(stdout.String())
		if strings.Contains(output, "Created task") {
			// Parse "Created task #123"
			parts := strings.Split(output, "#")
			if len(parts) > 1 {
				id, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
				return &Task{ID: id, Title: title, Body: body, Type: taskType}, nil
			}
		}
		return nil, fmt.Errorf("parse task: %w (output: %s)", err, output)
	}

	return &task, nil
}

// GetTask retrieves a task by ID.
func (b *Bridge) GetTask(taskID int64) (*Task, error) {
	args := []string{"show", strconv.FormatInt(taskID, 10), "--json"}

	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("get task: %s", stderr.String())
	}

	var task Task
	if err := json.Unmarshal(stdout.Bytes(), &task); err != nil {
		return nil, fmt.Errorf("parse task: %w", err)
	}

	return &task, nil
}

// ListTasks returns tasks, optionally filtered by status.
func (b *Bridge) ListTasks(status string) ([]*Task, error) {
	args := []string{"list", "--json"}

	if status != "" {
		args = append(args, "--status", status)
	}
	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list tasks: %s", stderr.String())
	}

	var tasks []*Task
	if err := json.Unmarshal(stdout.Bytes(), &tasks); err != nil {
		return nil, fmt.Errorf("parse tasks: %w", err)
	}

	return tasks, nil
}

// SendInput sends input to a blocked task.
func (b *Bridge) SendInput(taskID int64, input string) error {
	args := []string{"input", strconv.FormatInt(taskID, 10), input}

	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("send input: %s", stderr.String())
	}

	return nil
}

// QueueTask queues a task for execution.
func (b *Bridge) QueueTask(taskID int64) error {
	args := []string{"queue", strconv.FormatInt(taskID, 10)}

	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("queue task: %s", stderr.String())
	}

	return nil
}

// CompleteTask marks a task as complete.
func (b *Bridge) CompleteTask(taskID int64, result string) error {
	args := []string{"complete", strconv.FormatInt(taskID, 10)}

	if result != "" {
		args = append(args, "--message", result)
	}
	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("complete task: %s", stderr.String())
	}

	return nil
}

// FailTask marks a task as failed.
func (b *Bridge) FailTask(taskID int64, reason string) error {
	args := []string{"fail", strconv.FormatInt(taskID, 10)}

	if reason != "" {
		args = append(args, "--reason", reason)
	}
	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fail task: %s", stderr.String())
	}

	return nil
}

// GetBlockedTasks returns tasks waiting for input.
func (b *Bridge) GetBlockedTasks() ([]*Task, error) {
	return b.ListTasks("blocked")
}

// GetQueuedTasks returns tasks queued for execution.
func (b *Bridge) GetQueuedTasks() ([]*Task, error) {
	return b.ListTasks("queued")
}

// AddTaskComment adds a comment/note to a task.
func (b *Bridge) AddTaskComment(taskID int64, comment string) error {
	args := []string{"comment", strconv.FormatInt(taskID, 10), comment}

	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("add comment: %s", stderr.String())
	}

	return nil
}

// UpdateTaskBody updates a task's body/description.
func (b *Bridge) UpdateTaskBody(taskID int64, body string) error {
	args := []string{"update", strconv.FormatInt(taskID, 10), "--body", body}

	if b.project != "" {
		args = append(args, "--project", b.project)
	}

	cmd := exec.Command(b.tyPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update task: %s", stderr.String())
	}

	return nil
}

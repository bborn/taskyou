// Package bridge provides integration with the TaskYou CLI for task tracking.
// When sessions are created/completed, corresponding tasks can be created/updated
// in the TaskYou system.
package bridge

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// Bridge connects the sandbox agent to a TaskYou instance via the ty CLI.
type Bridge struct {
	tyPath  string
	enabled bool
}

// New creates a new TaskYou bridge.
// If tyPath is empty, auto-detection is attempted.
func New(tyPath string) *Bridge {
	if tyPath == "" {
		if p, err := exec.LookPath("ty"); err == nil {
			tyPath = p
		}
	}
	return &Bridge{
		tyPath:  tyPath,
		enabled: tyPath != "",
	}
}

// IsAvailable returns true if the ty CLI is accessible.
func (b *Bridge) IsAvailable() bool {
	return b.enabled
}

// CreateTask creates a task in TaskYou for a sandbox session.
func (b *Bridge) CreateTask(title, body, project string) (int64, error) {
	if !b.enabled {
		return 0, nil
	}

	args := []string{"create", title, "--json"}
	if body != "" {
		args = append(args, "--body", body)
	}
	if project != "" {
		args = append(args, "--project", project)
	}

	out, err := b.run(args...)
	if err != nil {
		return 0, fmt.Errorf("create task: %w", err)
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		// Try to parse just the ID from output
		log.Printf("bridge: could not parse task creation output: %s", out)
		return 0, nil
	}

	return result.ID, nil
}

// UpdateTaskStatus updates the status of a TaskYou task.
func (b *Bridge) UpdateTaskStatus(taskID int64, status string) error {
	if !b.enabled {
		return nil
	}

	_, err := b.run("status", fmt.Sprintf("%d", taskID), status)
	return err
}

// CloseTask marks a TaskYou task as done.
func (b *Bridge) CloseTask(taskID int64) error {
	if !b.enabled {
		return nil
	}

	_, err := b.run("close", fmt.Sprintf("%d", taskID))
	return err
}

func (b *Bridge) run(args ...string) (string, error) {
	cmd := exec.Command(b.tyPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ty %s: %s (%w)", strings.Join(args, " "), string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

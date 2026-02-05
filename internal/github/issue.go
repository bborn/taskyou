// Package github provides GitHub integration for issues and PRs.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// IssueInfo contains information about a created GitHub Issue.
type IssueInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// ghIssueCreateResponse is the JSON response from gh issue create.
type ghIssueCreateResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// CreateIssue creates a GitHub Issue for a task and returns the issue information.
// Uses the gh CLI to create the issue in the repository at repoDir.
// Returns nil if the gh CLI is not available or the creation fails.
func CreateIssue(repoDir, title, body string) (*IssueInfo, error) {
	// Check if gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found: %w", err)
	}

	// Use timeout to prevent blocking on slow network or GitHub API
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build arguments
	args := []string{"issue", "create", "--title", title, "--json", "number,url,title"}

	// Add body if provided
	if body != "" {
		args = append(args, "--body", body)
	} else {
		// Empty body is required or gh will open an editor
		args = append(args, "--body", "")
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh issue create failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("gh issue create failed: %w", err)
	}

	var resp ghIssueCreateResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &IssueInfo{
		Number: resp.Number,
		URL:    resp.URL,
		Title:  resp.Title,
	}, nil
}

// CloseIssue closes a GitHub Issue by number.
// Used when a task is completed or archived.
func CloseIssue(repoDir string, issueNumber int) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "issue", "close", fmt.Sprintf("%d", issueNumber))
	cmd.Dir = repoDir

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("gh issue close failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("gh issue close failed: %w", err)
	}

	return nil
}

// IsGitHubRepo checks if the directory is a GitHub repository.
// Returns true if 'gh repo view' succeeds.
func IsGitHubRepo(repoDir string) bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "name")
	cmd.Dir = repoDir

	return cmd.Run() == nil
}

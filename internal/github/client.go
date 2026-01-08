// Package github provides a client for interacting with GitHub Issues.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/task"
)

// Client wraps GitHub API operations using the gh CLI.
type Client struct {
	Repo string
}

// NewClient creates a new GitHub client.
func NewClient(repo string) *Client {
	if repo == "" {
		repo = "bborn/workflow"
	}
	return &Client{Repo: repo}
}

// ListTasks fetches tasks from GitHub Issues.
func (c *Client) ListTasks(ctx context.Context, opts task.FilterOptions) ([]task.Task, error) {
	args := []string{"issue", "list", "--repo", c.Repo, "--json", "number,title,body,url,state,labels,createdAt,updatedAt"}

	// State filter
	if opts.State != "" {
		args = append(args, "--state", opts.State)
	}

	// Limit
	if opts.Limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", opts.Limit))
	}

	// Label filters
	if opts.Project != "" {
		args = append(args, "--label", fmt.Sprintf("project:%s", opts.Project))
	}
	if opts.Type != "" {
		args = append(args, "--label", fmt.Sprintf("type:%s", opts.Type))
	}
	if opts.Status != "" {
		args = append(args, "--label", fmt.Sprintf("status:%s", opts.Status))
	}
	if opts.Priority != "" {
		args = append(args, "--label", fmt.Sprintf("priority:%s", opts.Priority))
	}

	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	return parseIssuesJSON(out)
}

// GetTask fetches a single task by number.
func (c *Client) GetTask(ctx context.Context, number int) (*task.Task, error) {
	args := []string{"issue", "view", fmt.Sprintf("%d", number), "--repo", c.Repo, "--json", "number,title,body,url,state,labels,createdAt,updatedAt,comments"}

	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get issue %d: %w", number, err)
	}

	return parseIssueJSON(out)
}

// CreateTask creates a new task.
func (c *Client) CreateTask(ctx context.Context, title, body string, labels []string) (*task.Task, error) {
	args := []string{"issue", "create", "--repo", c.Repo, "--title", title}

	if body != "" {
		args = append(args, "--body", body)
	} else {
		args = append(args, "--body", " ")
	}

	for _, label := range labels {
		args = append(args, "--label", label)
	}

	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	// Parse the URL from output to get the issue number
	url := strings.TrimSpace(string(out))
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return nil, fmt.Errorf("failed to parse issue URL from output")
	}

	var number int
	fmt.Sscanf(parts[len(parts)-1], "%d", &number)

	return &task.Task{
		Number: number,
		Title:  title,
		Body:   body,
		URL:    url,
		State:  "open",
		Labels: labels,
	}, nil
}

// AddLabel adds a label to a task.
func (c *Client) AddLabel(ctx context.Context, number int, label string) error {
	args := []string{"issue", "edit", fmt.Sprintf("%d", number), "--repo", c.Repo, "--add-label", label}
	_, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return fmt.Errorf("failed to add label: %w", err)
	}
	return nil
}

// RemoveLabel removes a label from a task.
func (c *Client) RemoveLabel(ctx context.Context, number int, label string) error {
	args := []string{"issue", "edit", fmt.Sprintf("%d", number), "--repo", c.Repo, "--remove-label", label}
	exec.CommandContext(ctx, "gh", args...).Output() // Ignore error if label doesn't exist
	return nil
}

// QueueTask adds the status:in_progress label to a task.
func (c *Client) QueueTask(ctx context.Context, number int) error {
	return c.AddLabel(ctx, number, "status:in_progress")
}

// CloseTask closes a task.
func (c *Client) CloseTask(ctx context.Context, number int) error {
	args := []string{"issue", "close", fmt.Sprintf("%d", number), "--repo", c.Repo}
	_, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return fmt.Errorf("failed to close issue: %w", err)
	}
	return nil
}

// RequeueTask removes blocked/done and adds in_progress label.
func (c *Client) RequeueTask(ctx context.Context, number int, comment string) error {
	// Add comment if provided
	if comment != "" {
		args := []string{"issue", "comment", fmt.Sprintf("%d", number), "--repo", c.Repo, "--body", comment}
		exec.CommandContext(ctx, "gh", args...).Output()
	}

	// Remove blocked/done
	c.RemoveLabel(ctx, number, "status:blocked")
	c.RemoveLabel(ctx, number, "status:done")

	// Add in_progress
	return c.AddLabel(ctx, number, "status:in_progress")
}

// OpenInBrowser opens the task in the default browser.
func (c *Client) OpenInBrowser(ctx context.Context, number int) error {
	args := []string{"issue", "view", fmt.Sprintf("%d", number), "--repo", c.Repo, "--web"}
	cmd := exec.CommandContext(ctx, "gh", args...)
	return cmd.Start()
}

// parseIssuesJSON parses the JSON output from gh issue list.
func parseIssuesJSON(data []byte) ([]task.Task, error) {
	type ghLabel struct {
		Name string `json:"name"`
	}
	type ghIssue struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
		URL       string    `json:"url"`
		State     string    `json:"state"`
		Labels    []ghLabel `json:"labels"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	var issues []ghIssue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, err
	}

	tasks := make([]task.Task, len(issues))
	for i, issue := range issues {
		labels := make([]string, len(issue.Labels))
		for j, l := range issue.Labels {
			labels[j] = l.Name
		}

		t := task.Task{
			Number:    issue.Number,
			Title:     issue.Title,
			Body:      issue.Body,
			URL:       issue.URL,
			State:     issue.State,
			Labels:    labels,
			CreatedAt: issue.CreatedAt,
			UpdatedAt: issue.UpdatedAt,
		}
		t.ParseLabels()
		tasks[i] = t
	}

	return tasks, nil
}

// parseIssueJSON parses the JSON output from gh issue view.
func parseIssueJSON(data []byte) (*task.Task, error) {
	type ghLabel struct {
		Name string `json:"name"`
	}
	type ghComment struct {
		Author    struct{ Login string } `json:"author"`
		Body      string                 `json:"body"`
		CreatedAt time.Time              `json:"createdAt"`
	}
	type ghIssue struct {
		Number    int         `json:"number"`
		Title     string      `json:"title"`
		Body      string      `json:"body"`
		URL       string      `json:"url"`
		State     string      `json:"state"`
		Labels    []ghLabel   `json:"labels"`
		Comments  []ghComment `json:"comments"`
		CreatedAt time.Time   `json:"createdAt"`
		UpdatedAt time.Time   `json:"updatedAt"`
	}

	var issue ghIssue
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, err
	}

	labels := make([]string, len(issue.Labels))
	for i, l := range issue.Labels {
		labels[i] = l.Name
	}

	comments := make([]task.Comment, len(issue.Comments))
	for i, c := range issue.Comments {
		comments[i] = task.Comment{
			Author:    c.Author.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		}
	}

	t := &task.Task{
		Number:    issue.Number,
		Title:     issue.Title,
		Body:      issue.Body,
		URL:       issue.URL,
		State:     issue.State,
		Labels:    labels,
		Comments:  comments,
		CreatedAt: issue.CreatedAt,
		UpdatedAt: issue.UpdatedAt,
	}
	t.ParseLabels()

	return t, nil
}

// Package github provides GitHub integration for querying PR status.
package github

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PRState represents the state of a pull request.
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
	PRStateDraft  PRState = "DRAFT"
)

// CheckState represents the state of CI checks.
type CheckState string

const (
	CheckStatePending CheckState = "PENDING"
	CheckStatePassing CheckState = "SUCCESS"
	CheckStateFailing CheckState = "FAILURE"
	CheckStateNone    CheckState = ""
)

// PRInfo contains information about a pull request.
type PRInfo struct {
	Number     int        `json:"number"`
	URL        string     `json:"url"`
	State      PRState    `json:"state"`
	IsDraft    bool       `json:"isDraft"`
	Title      string     `json:"title"`
	CheckState CheckState `json:"checkState"`
	Mergeable  string     `json:"mergeable"` // "MERGEABLE", "CONFLICTING", "UNKNOWN"
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// ghPRResponse is the JSON response from gh pr view.
type ghPRResponse struct {
	Number              int    `json:"number"`
	URL                 string `json:"url"`
	State               string `json:"state"`
	IsDraft             bool   `json:"isDraft"`
	Title               string `json:"title"`
	MergeStateStatus    string `json:"mergeStateStatus"`
	StatusCheckRollup   []ghCheck `json:"statusCheckRollup"`
	UpdatedAt           string `json:"updatedAt"`
}

type ghCheck struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// PRCache caches PR information to avoid repeated API calls.
type PRCache struct {
	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

type cacheEntry struct {
	info      *PRInfo
	fetchedAt time.Time
}

const cacheTTL = 30 * time.Second

// NewPRCache creates a new PR cache.
func NewPRCache() *PRCache {
	return &PRCache{
		cache: make(map[string]*cacheEntry),
	}
}

// GetPRForBranch queries GitHub for a PR associated with the given branch.
// Uses caching to avoid repeated API calls.
// Returns nil if no PR exists or gh CLI is not available.
func (c *PRCache) GetPRForBranch(repoDir, branchName string) *PRInfo {
	if branchName == "" {
		return nil
	}

	cacheKey := repoDir + ":" + branchName

	// Check cache first
	c.mu.RLock()
	entry, ok := c.cache[cacheKey]
	c.mu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < cacheTTL {
		return entry.info
	}

	// Fetch from GitHub
	info := fetchPRInfo(repoDir, branchName)

	// Update cache
	c.mu.Lock()
	c.cache[cacheKey] = &cacheEntry{
		info:      info,
		fetchedAt: time.Now(),
	}
	c.mu.Unlock()

	return info
}

// InvalidateCache clears the cache for a specific branch.
func (c *PRCache) InvalidateCache(repoDir, branchName string) {
	cacheKey := repoDir + ":" + branchName
	c.mu.Lock()
	delete(c.cache, cacheKey)
	c.mu.Unlock()
}

// fetchPRInfo queries GitHub for PR information using gh CLI.
func fetchPRInfo(repoDir, branchName string) *PRInfo {
	// Check if gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}

	// Query for PR associated with this branch
	// gh pr view <branch> --json number,url,state,isDraft,title,mergeStateStatus,statusCheckRollup,updatedAt
	cmd := exec.Command("gh", "pr", "view", branchName,
		"--json", "number,url,state,isDraft,title,mergeStateStatus,statusCheckRollup,updatedAt")
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		// No PR exists for this branch or other error
		return nil
	}

	var resp ghPRResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil
	}

	// Parse the response
	info := &PRInfo{
		Number:    resp.Number,
		URL:       resp.URL,
		Title:     resp.Title,
		IsDraft:   resp.IsDraft,
		Mergeable: resp.MergeStateStatus,
	}

	// Parse state
	switch strings.ToUpper(resp.State) {
	case "OPEN":
		if resp.IsDraft {
			info.State = PRStateDraft
		} else {
			info.State = PRStateOpen
		}
	case "CLOSED":
		info.State = PRStateClosed
	case "MERGED":
		info.State = PRStateMerged
	default:
		info.State = PRStateOpen
	}

	// Parse check state from statusCheckRollup
	info.CheckState = parseCheckState(resp.StatusCheckRollup)

	// Parse updated time
	if t, err := time.Parse(time.RFC3339, resp.UpdatedAt); err == nil {
		info.UpdatedAt = t
	}

	return info
}

// parseCheckState determines the overall check state from individual checks.
func parseCheckState(checks []ghCheck) CheckState {
	if len(checks) == 0 {
		return CheckStateNone
	}

	hasFailure := false
	hasPending := false

	for _, check := range checks {
		// For status checks, look at state
		// For check runs, look at conclusion
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)
		status := strings.ToUpper(check.Status)

		// Check if pending
		if status == "QUEUED" || status == "IN_PROGRESS" || status == "PENDING" ||
			state == "PENDING" || state == "EXPECTED" {
			hasPending = true
			continue
		}

		// Check if failed
		if conclusion == "FAILURE" || conclusion == "ERROR" || conclusion == "TIMED_OUT" ||
			conclusion == "CANCELLED" || state == "FAILURE" || state == "ERROR" {
			hasFailure = true
		}
	}

	if hasFailure {
		return CheckStateFailing
	}
	if hasPending {
		return CheckStatePending
	}
	return CheckStatePassing
}

// StatusIcon returns a unicode icon representing the PR state.
func (p *PRInfo) StatusIcon() string {
	if p == nil {
		return ""
	}

	switch p.State {
	case PRStateMerged:
		return "M" // Merged
	case PRStateClosed:
		return "X" // Closed
	case PRStateDraft:
		return "D" // Draft
	case PRStateOpen:
		switch p.CheckState {
		case CheckStatePassing:
			return "P" // PR open, checks passing
		case CheckStateFailing:
			return "F" // PR open, checks failing
		case CheckStatePending:
			return "R" // PR open, checks running
		default:
			return "O" // PR open, no checks
		}
	}
	return ""
}

// StatusDescription returns a human-readable description.
func (p *PRInfo) StatusDescription() string {
	if p == nil {
		return ""
	}

	switch p.State {
	case PRStateMerged:
		return "Merged"
	case PRStateClosed:
		return "Closed"
	case PRStateDraft:
		return "Draft PR"
	case PRStateOpen:
		switch p.CheckState {
		case CheckStatePassing:
			if p.Mergeable == "MERGEABLE" {
				return "Ready to merge"
			}
			if p.Mergeable == "CONFLICTING" {
				return "Has conflicts"
			}
			return "Checks passing"
		case CheckStateFailing:
			return "Checks failing"
		case CheckStatePending:
			return "Checks running"
		default:
			return "Open PR"
		}
	}
	return ""
}

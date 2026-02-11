// Package github provides GitHub integration for querying PR status.
package github

import (
	"context"
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
	Additions  int        `json:"additions"`  // Lines added
	Deletions  int        `json:"deletions"`  // Lines deleted
}

// ghPRResponse is the JSON response from gh pr view.
type ghPRResponse struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	Title             string    `json:"title"`
	Mergeable         string    `json:"mergeable"`
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
	UpdatedAt         string    `json:"updatedAt"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
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

const cacheTTL = 15 * time.Second

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

	// Use timeout to prevent blocking on slow network or GitHub API
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Query for PR associated with this branch
	// gh pr view <branch> --json number,url,state,isDraft,title,mergeable,statusCheckRollup,updatedAt,additions,deletions
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", branchName,
		"--json", "number,url,state,isDraft,title,mergeable,statusCheckRollup,updatedAt,additions,deletions")
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		// No PR exists for this branch, timeout, or other error
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
		Mergeable: resp.Mergeable,
		Additions: resp.Additions,
		Deletions: resp.Deletions,
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
		// Check for merge conflicts first - this takes priority over check status
		if p.Mergeable == "CONFLICTING" {
			return "Has conflicts"
		}
		switch p.CheckState {
		case CheckStatePassing:
			if p.Mergeable == "MERGEABLE" {
				return "Ready to merge"
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

// ghPRListResponse is a single PR from gh pr list.
type ghPRListResponse struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	Title             string    `json:"title"`
	HeadRefName       string    `json:"headRefName"`
	Mergeable         string    `json:"mergeable"`
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
	UpdatedAt         string    `json:"updatedAt"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
}

// FetchAllPRsForRepo fetches all open and recently merged PRs for a repo in a single API call.
// Returns a map of branch name -> PRInfo. This is much more efficient than fetching per-branch.
func FetchAllPRsForRepo(repoDir string) map[string]*PRInfo {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}

	result := make(map[string]*PRInfo)

	// Fetch open PRs
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Get all open PRs in one call
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--state", "open",
		"--json", "number,url,state,isDraft,title,headRefName,mergeable,statusCheckRollup,updatedAt,additions,deletions",
		"--limit", "100")
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		return result
	}

	var prs []ghPRListResponse
	if err := json.Unmarshal(output, &prs); err != nil {
		return result
	}

	for _, pr := range prs {
		info := parsePRListResponse(&pr)
		if info != nil && pr.HeadRefName != "" {
			result[pr.HeadRefName] = info
		}
	}

	// Also fetch recently merged PRs (last 20) to catch merges
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	cmd2 := exec.CommandContext(ctx2, "gh", "pr", "list",
		"--state", "merged",
		"--json", "number,url,state,isDraft,title,headRefName,mergeable,updatedAt,additions,deletions",
		"--limit", "20")
	cmd2.Dir = repoDir

	output2, err := cmd2.Output()
	if err == nil {
		var mergedPRs []ghPRListResponse
		if json.Unmarshal(output2, &mergedPRs) == nil {
			for _, pr := range mergedPRs {
				// Only add if not already present (open PR takes precedence)
				if _, exists := result[pr.HeadRefName]; !exists && pr.HeadRefName != "" {
					info := parsePRListResponse(&pr)
					if info != nil {
						result[pr.HeadRefName] = info
					}
				}
			}
		}
	}

	// Also fetch recently closed PRs (last 10) to catch closures
	ctx3, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()

	cmd3 := exec.CommandContext(ctx3, "gh", "pr", "list",
		"--state", "closed",
		"--json", "number,url,state,isDraft,title,headRefName,mergeable,updatedAt,additions,deletions",
		"--limit", "10")
	cmd3.Dir = repoDir

	output3, err := cmd3.Output()
	if err == nil {
		var closedPRs []ghPRListResponse
		if json.Unmarshal(output3, &closedPRs) == nil {
			for _, pr := range closedPRs {
				// Only add if not already present (open/merged PR takes precedence)
				if _, exists := result[pr.HeadRefName]; !exists && pr.HeadRefName != "" {
					info := parsePRListResponse(&pr)
					if info != nil {
						result[pr.HeadRefName] = info
					}
				}
			}
		}
	}

	return result
}

// parsePRListResponse converts a gh pr list response to PRInfo.
func parsePRListResponse(pr *ghPRListResponse) *PRInfo {
	info := &PRInfo{
		Number:    pr.Number,
		URL:       pr.URL,
		Title:     pr.Title,
		IsDraft:   pr.IsDraft,
		Mergeable: pr.Mergeable,
		Additions: pr.Additions,
		Deletions: pr.Deletions,
	}

	// Parse state
	switch strings.ToUpper(pr.State) {
	case "OPEN":
		if pr.IsDraft {
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

	// Parse check state
	info.CheckState = parseCheckState(pr.StatusCheckRollup)

	// Parse updated time
	if t, err := time.Parse(time.RFC3339, pr.UpdatedAt); err == nil {
		info.UpdatedAt = t
	}

	return info
}

// UpdateCacheForRepo updates the cache with batch-fetched PR data for a repo.
// This is more efficient than individual fetches.
func (c *PRCache) UpdateCacheForRepo(repoDir string, prsByBranch map[string]*PRInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for branchName, info := range prsByBranch {
		cacheKey := repoDir + ":" + branchName
		c.cache[cacheKey] = &cacheEntry{
			info:      info,
			fetchedAt: now,
		}
	}
}

// MarshalPRInfo converts a PRInfo to JSON string for database storage.
func MarshalPRInfo(info *PRInfo) string {
	if info == nil {
		return ""
	}
	data, err := json.Marshal(info)
	if err != nil {
		return ""
	}
	return string(data)
}

// UnmarshalPRInfo converts a JSON string from database back to PRInfo.
func UnmarshalPRInfo(data string) *PRInfo {
	if data == "" {
		return nil
	}
	var info PRInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return nil
	}
	return &info
}

// GetCachedPR returns cached PR info without fetching. Returns nil if not cached or expired.
func (c *PRCache) GetCachedPR(repoDir, branchName string) *PRInfo {
	if branchName == "" {
		return nil
	}

	cacheKey := repoDir + ":" + branchName

	c.mu.RLock()
	entry, ok := c.cache[cacheKey]
	c.mu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < cacheTTL {
		return entry.info
	}
	return nil
}

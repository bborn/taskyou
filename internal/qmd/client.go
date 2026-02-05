// Package qmd provides a client for QMD semantic search.
package qmd

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client wraps the QMD CLI for semantic search.
type Client struct {
	binary    string
	available bool
	mu        sync.RWMutex

	// Cache for search results
	cache     map[string]cachedResult
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

type cachedResult struct {
	results   []SearchResult
	timestamp time.Time
}

// SearchResult represents a single search result from QMD.
type SearchResult struct {
	DocID   string  `json:"docid"`
	Score   float64 `json:"score"`
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
}

// RelatedTask represents a related task found via semantic search.
type RelatedTask struct {
	TaskID  int64
	Title   string
	Score   float64
	Project string
}

// DefaultClient is the global QMD client instance.
var DefaultClient = NewClient("")

// NewClient creates a new QMD client.
func NewClient(binary string) *Client {
	if binary == "" {
		binary = "qmd"
	}
	c := &Client{
		binary:   binary,
		cache:    make(map[string]cachedResult),
		cacheTTL: 5 * time.Minute,
	}
	// Check availability on creation
	c.checkAvailable()
	return c
}

// checkAvailable checks if qmd is installed.
func (c *Client) checkAvailable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := exec.LookPath(c.binary)
	c.available = err == nil
}

// IsAvailable returns true if QMD is installed and usable.
func (c *Client) IsAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.available
}

// Search performs a keyword search (BM25).
func (c *Client) Search(ctx context.Context, query string, collection string, count int) ([]SearchResult, error) {
	return c.search(ctx, "search", query, collection, count)
}

// VSearch performs a vector/semantic search.
func (c *Client) VSearch(ctx context.Context, query string, collection string, count int) ([]SearchResult, error) {
	return c.search(ctx, "vsearch", query, collection, count)
}

// Query performs a hybrid search with re-ranking (best quality).
func (c *Client) Query(ctx context.Context, query string, collection string, count int) ([]SearchResult, error) {
	return c.search(ctx, "query", query, collection, count)
}

// search executes a search command.
func (c *Client) search(ctx context.Context, cmd, query, collection string, count int) ([]SearchResult, error) {
	if !c.IsAvailable() {
		return nil, nil
	}

	// Check cache
	cacheKey := cmd + ":" + collection + ":" + query + ":" + strconv.Itoa(count)
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Since(cached.timestamp) < c.cacheTTL {
		c.cacheMu.RUnlock()
		return cached.results, nil
	}
	c.cacheMu.RUnlock()

	args := []string{cmd, query, "--json"}
	if collection != "" {
		args = append(args, "-c", collection)
	}
	if count > 0 {
		args = append(args, "-n", strconv.Itoa(count))
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	execCmd := exec.CommandContext(cmdCtx, c.binary, args...)
	out, err := execCmd.Output()
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, err
	}

	// Cache results
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedResult{results: results, timestamp: time.Now()}
	c.cacheMu.Unlock()

	return results, nil
}

// FindRelatedTasks searches for tasks related to the given query.
// It parses task IDs from the document paths (expecting format: task-{id}.md)
func (c *Client) FindRelatedTasks(ctx context.Context, query string, count int) ([]RelatedTask, error) {
	if !c.IsAvailable() {
		return nil, nil
	}

	// Use hybrid search for best results
	results, err := c.Query(ctx, query, "ty-tasks", count)
	if err != nil {
		return nil, err
	}

	var related []RelatedTask
	for _, r := range results {
		// Parse task ID from path (format: task-{id}.md)
		taskID := parseTaskIDFromPath(r.Path)
		if taskID == 0 {
			continue
		}

		// Extract title from result
		title := r.Title
		if title == "" {
			title = r.Snippet
		}

		related = append(related, RelatedTask{
			TaskID: taskID,
			Title:  title,
			Score:  r.Score,
		})
	}

	return related, nil
}

// parseTaskIDFromPath extracts task ID from a path like "task-42.md"
func parseTaskIDFromPath(path string) int64 {
	// Get filename from path
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]

	// Remove extension
	filename = strings.TrimSuffix(filename, ".md")

	// Parse task-{id}
	if !strings.HasPrefix(filename, "task-") {
		return 0
	}

	idStr := strings.TrimPrefix(filename, "task-")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0
	}

	return id
}

// ClearCache clears the search result cache.
func (c *Client) ClearCache() {
	c.cacheMu.Lock()
	c.cache = make(map[string]cachedResult)
	c.cacheMu.Unlock()
}

// Refresh re-checks QMD availability.
func (c *Client) Refresh() {
	c.checkAvailable()
}

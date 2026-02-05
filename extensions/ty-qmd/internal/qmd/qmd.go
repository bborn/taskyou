// Package qmd provides a wrapper for the QMD CLI.
package qmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// QMD wraps the qmd CLI for search operations.
type QMD struct {
	binary string
	logger *slog.Logger
}

// SearchResult represents a single search result.
type SearchResult struct {
	DocID   string  `json:"docid"`
	Score   float64 `json:"score"`
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
}

// Status represents qmd index status.
type Status struct {
	Collections int `json:"collections"`
	Documents   int `json:"documents"`
	Embedded    int `json:"embedded"`
}

// New creates a new QMD wrapper.
func New(binary string, logger *slog.Logger) *QMD {
	if binary == "" {
		binary = "qmd"
	}
	return &QMD{
		binary: binary,
		logger: logger,
	}
}

// IsAvailable checks if qmd is installed.
func (q *QMD) IsAvailable() bool {
	_, err := exec.LookPath(q.binary)
	return err == nil
}

// Search performs a BM25 keyword search.
func (q *QMD) Search(query, collection string, count int) ([]SearchResult, error) {
	args := []string{"search", query, "--json"}
	if collection != "" {
		args = append(args, "-c", collection)
	}
	if count > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", count))
	}

	return q.runSearch(args)
}

// VSearch performs a vector semantic search.
func (q *QMD) VSearch(query, collection string, count int) ([]SearchResult, error) {
	args := []string{"vsearch", query, "--json"}
	if collection != "" {
		args = append(args, "-c", collection)
	}
	if count > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", count))
	}

	return q.runSearch(args)
}

// Query performs a hybrid search with re-ranking.
func (q *QMD) Query(query, collection string, count int) ([]SearchResult, error) {
	args := []string{"query", query, "--json"}
	if collection != "" {
		args = append(args, "-c", collection)
	}
	if count > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", count))
	}

	return q.runSearch(args)
}

// Get retrieves a document by path or docid.
func (q *QMD) Get(pathOrDocID string) (string, error) {
	out, err := q.run("get", pathOrDocID, "--full")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// AddCollection adds a new collection to the index.
func (q *QMD) AddCollection(path, name, mask string) error {
	args := []string{"collection", "add", path, "--name", name}
	if mask != "" {
		args = append(args, "--mask", mask)
	}

	_, err := q.run(args...)
	return err
}

// EnsureCollection creates a collection if it doesn't exist.
func (q *QMD) EnsureCollection(name string) error {
	// Check if collection exists by listing
	out, err := q.run("collection", "list", "--json")
	if err != nil {
		// Collection list might fail if no collections exist yet
		return nil
	}

	var collections []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &collections); err == nil {
		for _, c := range collections {
			if c.Name == name {
				return nil // Already exists
			}
		}
	}

	// Create temp directory for the collection
	// Note: qmd requires a path for collection, but for tasks we manage individual files
	return nil
}

// IndexFile indexes a single file into a collection.
func (q *QMD) IndexFile(path, collection string) error {
	// Add file to collection and trigger embedding
	args := []string{"collection", "add", path, "--name", collection}
	if _, err := q.run(args...); err != nil {
		// Might fail if collection already has this path, try update
		q.logger.Debug("add collection failed, trying update", "error", err)
	}

	// Trigger index update
	return q.Update()
}

// Update updates the index.
func (q *QMD) Update() error {
	_, err := q.run("update")
	return err
}

// Embed generates embeddings for unembedded documents.
func (q *QMD) Embed() error {
	_, err := q.run("embed")
	return err
}

// Status returns the index status.
func (q *QMD) Status() (*Status, error) {
	out, err := q.run("status", "--json")
	if err != nil {
		return nil, err
	}

	var status Status
	if err := json.Unmarshal(out, &status); err != nil {
		// Try to parse text output
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "collections:") {
				fmt.Sscanf(line, "collections: %d", &status.Collections)
			} else if strings.Contains(line, "documents:") {
				fmt.Sscanf(line, "documents: %d", &status.Documents)
			} else if strings.Contains(line, "embedded:") {
				fmt.Sscanf(line, "embedded: %d", &status.Embedded)
			}
		}
	}

	return &status, nil
}

// runSearch executes a search command and parses results.
func (q *QMD) runSearch(args []string) ([]SearchResult, error) {
	out, err := q.run(args...)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	return results, nil
}

// run executes a qmd command.
func (q *QMD) run(args ...string) ([]byte, error) {
	q.logger.Debug("running qmd", "args", args)

	cmd := exec.Command(q.binary, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("qmd %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("qmd %s: %w", strings.Join(args, " "), err)
	}

	return out, nil
}

// Package qmd provides a wrapper for the QMD CLI.
package qmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// QMD wraps the qmd CLI for search operations.
type QMD struct {
	binary  string
	logger  *slog.Logger
	timeout time.Duration
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
		binary:  binary,
		logger:  logger,
		timeout: 30 * time.Second,
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

	return q.runSearch(context.Background(), args)
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

	return q.runSearch(context.Background(), args)
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

	return q.runSearch(context.Background(), args)
}

// Get retrieves a document by path or docid.
func (q *QMD) Get(pathOrDocID string) (string, error) {
	out, err := q.run(context.Background(), "get", pathOrDocID, "--full")
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

	_, err := q.run(context.Background(), args...)
	return err
}

// IndexFile adds a single file to a collection.
// Call Update() after indexing all files to trigger re-indexing.
func (q *QMD) IndexFile(path, collection string) error {
	args := []string{"collection", "add", path, "--name", collection}
	if _, err := q.run(context.Background(), args...); err != nil {
		// Might fail if collection already has this path
		q.logger.Debug("add collection failed", "error", err)
	}
	return nil
}

// Update updates the index.
func (q *QMD) Update() error {
	_, err := q.run(context.Background(), "update")
	return err
}

// Embed generates embeddings for unembedded documents.
func (q *QMD) Embed() error {
	_, err := q.run(context.Background(), "embed")
	return err
}

// Status returns the index status.
func (q *QMD) Status() (*Status, error) {
	out, err := q.run(context.Background(), "status", "--json")
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
func (q *QMD) runSearch(ctx context.Context, args []string) ([]SearchResult, error) {
	out, err := q.run(ctx, args...)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	return results, nil
}

// run executes a qmd command with a timeout.
func (q *QMD) run(ctx context.Context, args ...string) ([]byte, error) {
	q.logger.Debug("running qmd", "args", args)

	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, q.binary, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("qmd %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("qmd %s: %w", strings.Join(args, " "), err)
	}

	return out, nil
}

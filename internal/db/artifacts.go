package db

import (
	"database/sql"
	"fmt"
	"time"
)

// PipelineArtifact is one inter-phase document produced by a workflow step and
// read by a later step on the same shared branch. It is the TaskYou-native
// hand-off store: document phases (research-questions, research, design, ...)
// write their output here instead of committing docs to git.
type PipelineArtifact struct {
	Branch    string
	Name      string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SetPipelineArtifact upserts an artifact document keyed by (branch, name). A
// second write to the same key overwrites the previous content (and bumps
// updated_at), mirroring SetProjectContext's overwrite semantics.
func (db *DB) SetPipelineArtifact(branch, name, content string) error {
	if branch == "" {
		return fmt.Errorf("pipeline artifact requires a branch")
	}
	if name == "" {
		return fmt.Errorf("pipeline artifact requires a name")
	}
	_, err := db.Exec(`
		INSERT INTO pipeline_artifacts (branch, name, content, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(branch, name) DO UPDATE SET
			content = excluded.content,
			updated_at = CURRENT_TIMESTAMP
	`, branch, name, content)
	if err != nil {
		return fmt.Errorf("set pipeline artifact: %w", err)
	}
	return nil
}

// GetPipelineArtifact returns the content of a single artifact by (branch, name).
// A missing artifact returns an empty string and no error (same convenience as
// GetProjectContext) so callers can treat "not written yet" as empty.
func (db *DB) GetPipelineArtifact(branch, name string) (string, error) {
	var content string
	err := db.QueryRow(
		`SELECT content FROM pipeline_artifacts WHERE branch = ? AND name = ?`,
		branch, name,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pipeline artifact: %w", err)
	}
	return content, nil
}

// ListPipelineArtifacts returns every artifact for a branch, oldest first, so a
// phase can enumerate all upstream documents when no specific name is requested.
func (db *DB) ListPipelineArtifacts(branch string) ([]PipelineArtifact, error) {
	rows, err := db.Query(
		`SELECT branch, name, content, created_at, updated_at
		 FROM pipeline_artifacts WHERE branch = ? ORDER BY id ASC`,
		branch,
	)
	if err != nil {
		return nil, fmt.Errorf("list pipeline artifacts: %w", err)
	}
	defer rows.Close()

	var out []PipelineArtifact
	for rows.Next() {
		var a PipelineArtifact
		if err := rows.Scan(&a.Branch, &a.Name, &a.Content, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan pipeline artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Package panel owns task workspace resources independently of their UI viewers.
package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

var ErrNotFound = errors.New("task or panel not found")

// Instance is a durable resource descriptor, never a process or connection.
type Instance struct {
	ID         string `json:"id"`
	TaskID     int64  `json:"task_id"`
	ProviderID string `json:"provider_id"`
	Resource   string `json:"resource"`
	Title      string `json:"title"`
}

type Entry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
}

type Content struct {
	Kind    string  `json:"kind"`
	Text    string  `json:"text,omitempty"`
	URL     string  `json:"url,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
}

// Provider registers one resource type. Renderers consume Kind, not Go code.
// Adding a kind requires renderers in both interfaces and parity coverage.
type Provider struct {
	ID        string                                                   `json:"id"`
	Title     string                                                   `json:"title"`
	Kind      string                                                   `json:"kind"`
	Normalize func(string) (string, error)                             `json:"-"`
	Load      func(context.Context, *db.Task, string) (Content, error) `json:"-"`
}

type Service struct {
	DB        *db.DB
	providers []Provider
}

func New(database *db.DB) *Service {
	s := &Service{DB: database}
	s.providers = []Provider{
		{"shell", "Shell", "shell", noResource, func(context.Context, *db.Task, string) (Content, error) { return Content{Kind: "shell"}, nil }},
		{"pr", "Pull request", "markdown", noResource, s.pullRequest},
		{"files", "Files", "files", fileResource, s.files},
		{"file", "File preview", "text", fileResource, s.file},
	}
	return s
}

func (s *Service) Providers() []Provider { return append([]Provider(nil), s.providers...) }
func noResource(v string) (string, error) {
	if v != "" {
		return "", fmt.Errorf("this provider takes no resource")
	}
	return "", nil
}
func fileResource(v string) (string, error) {
	if strings.ContainsAny(v, "\x00\r\n\\") || strings.HasPrefix(v, "/") {
		return "", fmt.Errorf("use a relative workspace path")
	}
	v = path.Clean(v)
	if v == ".." || strings.HasPrefix(v, "../") {
		return "", fmt.Errorf("path is outside the workspace")
	}
	return v, nil
}
func (s *Service) task(id int64) (*db.Task, error) {
	t, err := s.DB.GetTask(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	return t, nil
}
func instance(taskID int64, provider, resource, title string) Instance {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", taskID, provider, resource)))
	return Instance{hex.EncodeToString(hash[:16]), taskID, provider, resource, title}
}

// List initializes Shell once. An intentionally empty workspace stays empty.
func (s *Service) List(taskID int64) ([]Instance, error) {
	if _, err := s.task(taskID); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r, err := tx.Exec("INSERT OR IGNORE INTO task_workspaces(task_id) VALUES(?)", taskID)
	if err != nil {
		return nil, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		p := instance(taskID, "shell", "", "Shell")
		if _, err = tx.Exec("INSERT INTO task_panels(id,task_id,provider_id,resource,title) VALUES(?,?,?,?,?)", p.ID, taskID, p.ProviderID, p.Resource, p.Title); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query("SELECT id,task_id,provider_id,resource,title FROM task_panels WHERE task_id=? ORDER BY sequence", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		var p Instance
		if err := rows.Scan(&p.ID, &p.TaskID, &p.ProviderID, &p.Resource, &p.Title); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Open(taskID int64, providerID, resource string) (Instance, error) {
	var provider *Provider
	for i := range s.providers {
		if s.providers[i].ID == providerID {
			provider = &s.providers[i]
			break
		}
	}
	if provider == nil {
		return Instance{}, fmt.Errorf("unknown panel provider %q", providerID)
	}
	normalized, err := provider.Normalize(resource)
	if err != nil {
		return Instance{}, err
	}
	if len(normalized) > 4096 {
		return Instance{}, fmt.Errorf("resource path is too long")
	}
	if _, err = s.List(taskID); err != nil {
		return Instance{}, err
	}
	title := provider.Title
	if providerID == "file" {
		title = path.Base(normalized)
	}
	p := instance(taskID, providerID, normalized, title)
	_, err = s.DB.Exec("INSERT OR IGNORE INTO task_panels(id,task_id,provider_id,resource,title) VALUES(?,?,?,?,?)", p.ID, p.TaskID, p.ProviderID, p.Resource, p.Title)
	return p, err
}

func (s *Service) Close(taskID int64, id string) error {
	if _, err := s.task(taskID); err != nil {
		return err
	}
	_, err := s.DB.Exec("DELETE FROM task_panels WHERE task_id=? AND id=?", taskID, id)
	return err
}

func (s *Service) Content(ctx context.Context, taskID int64, id string) (Content, error) {
	t, err := s.task(taskID)
	if err != nil {
		return Content{}, err
	}
	var provider, resource string
	if err = s.DB.QueryRow("SELECT provider_id,resource FROM task_panels WHERE task_id=? AND id=?", taskID, id).Scan(&provider, &resource); err != nil {
		return Content{}, ErrNotFound
	}
	for _, p := range s.providers {
		if p.ID == provider {
			return p.Load(ctx, t, resource)
		}
	}
	return Content{}, fmt.Errorf("panel provider %q is unavailable; close this tab or restore the provider", provider)
}

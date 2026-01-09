// Package config provides application configuration from database.
package config

import (
	"os"
	"path/filepath"

	"github.com/bborn/workflow/internal/db"
)

// Config holds application configuration loaded from database.
type Config struct {
	db          *db.DB
	ProjectsDir string
}

// Setting keys
const (
	SettingProjectsDir      = "projects_dir"
	SettingTheme            = "theme"
	SettingDetailPaneHeight = "detail_pane_height"
)

// New creates a config from database.
func New(database *db.DB) *Config {
	cfg := &Config{db: database}
	cfg.load()
	return cfg
}

func (c *Config) load() {
	// Load projects_dir or use default
	if dir, err := c.db.GetSetting(SettingProjectsDir); err == nil && dir != "" {
		c.ProjectsDir = expandPath(dir)
	} else {
		home, _ := os.UserHomeDir()
		c.ProjectsDir = filepath.Join(home, "Projects")
	}
}

// GetProjectDir returns the directory for a project name.
func (c *Config) GetProjectDir(project string) string {
	if project == "" {
		return c.ProjectsDir
	}

	// Look up project in database
	p, err := c.db.GetProjectByName(project)
	if err == nil && p != nil {
		return expandPath(p.Path)
	}

	// Default: projects_dir/project
	return filepath.Join(c.ProjectsDir, project)
}

// SetProjectsDir sets the default projects directory.
func (c *Config) SetProjectsDir(dir string) error {
	if err := c.db.SetSetting(SettingProjectsDir, dir); err != nil {
		return err
	}
	c.ProjectsDir = expandPath(dir)
	return nil
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}

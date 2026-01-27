package executor

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .taskyou.yml configuration file in a project root.
type ProjectConfig struct {
	Worktree WorktreeConfig `yaml:"worktree"`
}

// WorktreeConfig contains worktree-specific configuration.
type WorktreeConfig struct {
	// InitScript is the path to a script that runs after worktree creation.
	// Can be relative to the project root (e.g., "bin/worktree-setup").
	InitScript string `yaml:"init_script"`
	// TeardownScript is the path to a script that runs before worktree deletion.
	// Can be relative to the project root (e.g., "bin/worktree-teardown").
	TeardownScript string `yaml:"teardown_script"`
}

// ConfigFileNames are the supported configuration file names, in order of precedence.
var ConfigFileNames = []string{".taskyou.yml", ".taskyou.yaml", "taskyou.yml", "taskyou.yaml"}

// ConventionalInitScript is the conventional location for a worktree init script.
// If no config file exists but this script is present and executable, it will be used.
const ConventionalInitScript = "bin/worktree-setup"

// ConventionalTeardownScript is the conventional location for a worktree teardown script.
// If no config file exists but this script is present and executable, it will be used.
const ConventionalTeardownScript = "bin/worktree-teardown"

// LoadProjectConfig loads the project configuration from the given directory.
// It returns nil if no configuration file exists.
func LoadProjectConfig(projectDir string) (*ProjectConfig, error) {
	for _, filename := range ConfigFileNames {
		configPath := filepath.Join(projectDir, filename)
		if _, err := os.Stat(configPath); err == nil {
			return loadConfigFile(configPath)
		}
	}
	return nil, nil
}

// loadConfigFile reads and parses a YAML configuration file.
func loadConfigFile(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config ProjectConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// GetWorktreeInitScript returns the path to the worktree init script for a project.
// It checks:
// 1. The init_script configured in .taskyou.yml
// 2. The conventional bin/worktree-setup script if it exists and is executable
// Returns empty string if no init script is configured or found.
func GetWorktreeInitScript(projectDir string) string {
	// Check configuration file first
	config, err := LoadProjectConfig(projectDir)
	if err == nil && config != nil && config.Worktree.InitScript != "" {
		scriptPath := config.Worktree.InitScript
		// Make relative paths absolute
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(projectDir, scriptPath)
		}
		// Verify the script exists
		if _, err := os.Stat(scriptPath); err == nil {
			return scriptPath
		}
	}

	// Fall back to conventional location
	conventionalPath := filepath.Join(projectDir, ConventionalInitScript)
	if info, err := os.Stat(conventionalPath); err == nil {
		// Check if executable (on Unix-like systems)
		if info.Mode()&0111 != 0 {
			return conventionalPath
		}
	}

	return ""
}

// GetWorktreeTeardownScript returns the path to the worktree teardown script for a project.
// It checks:
// 1. The teardown_script configured in .taskyou.yml
// 2. The conventional bin/worktree-teardown script if it exists and is executable
// Returns empty string if no teardown script is configured or found.
func GetWorktreeTeardownScript(projectDir string) string {
	// Check configuration file first
	config, err := LoadProjectConfig(projectDir)
	if err == nil && config != nil && config.Worktree.TeardownScript != "" {
		scriptPath := config.Worktree.TeardownScript
		// Make relative paths absolute
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(projectDir, scriptPath)
		}
		// Verify the script exists
		if _, err := os.Stat(scriptPath); err == nil {
			return scriptPath
		}
	}

	// Fall back to conventional location
	conventionalPath := filepath.Join(projectDir, ConventionalTeardownScript)
	if info, err := os.Stat(conventionalPath); err == nil {
		// Check if executable (on Unix-like systems)
		if info.Mode()&0111 != 0 {
			return conventionalPath
		}
	}

	return ""
}

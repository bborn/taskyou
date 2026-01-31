// Package sprites provides shared functionality for Fly.io Sprites integration.
// Sprites are isolated VMs used as execution environments for Claude.
//
// This implementation uses the sprite CLI directly (rather than the SDK)
// to leverage the user's existing fly.io authentication.
package sprites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// Settings keys for sprite configuration
const (
	SettingName = "sprite_name" // Name of the sprite to use
)

// Default sprite name
const DefaultName = "task-daemon"

// GetName returns the name of the daemon sprite.
func GetName(database *db.DB) string {
	if database != nil {
		name, _ := database.GetSetting(SettingName)
		if name != "" {
			return name
		}
	}
	return DefaultName
}

// SetName saves the sprite name to the database.
func SetName(database *db.DB, name string) error {
	return database.SetSetting(SettingName, name)
}

// IsAvailable checks if the sprite CLI is installed and authenticated.
func IsAvailable() bool {
	// Check if sprite CLI exists
	if _, err := exec.LookPath("sprite"); err != nil {
		return false
	}

	// Check if authenticated by listing orgs
	cmd := exec.Command("sprite", "org", "list")
	return cmd.Run() == nil
}

// ListSprites returns a list of available sprites.
func ListSprites() ([]string, error) {
	cmd := exec.Command("sprite", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list sprites: %w", err)
	}

	var sprites []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sprites = append(sprites, line)
		}
	}
	return sprites, nil
}

// SpriteInfo contains information about a sprite.
type SpriteInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

// GetSprite returns information about a specific sprite.
func GetSprite(name string) (*SpriteInfo, error) {
	// Use the sprite in the specified directory context
	cmd := exec.Command("sprite", "use", name)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sprite not found: %s", name)
	}

	// Get the URL to verify it's accessible
	urlCmd := exec.Command("sprite", "url")
	urlOutput, _ := urlCmd.Output()

	return &SpriteInfo{
		Name:   name,
		Status: "running", // If use succeeded, it's accessible
		URL:    strings.TrimSpace(string(urlOutput)),
	}, nil
}

// CreateSprite creates a new sprite with the given name.
func CreateSprite(name string) error {
	cmd := exec.Command("sprite", "create", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create sprite: %s", stderr.String())
	}
	return nil
}

// ExecCommand executes a command on the sprite and returns stdout.
func ExecCommand(spriteName string, args ...string) (string, error) {
	// First ensure we're using the right sprite
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return "", fmt.Errorf("select sprite: %w", err)
	}

	// Execute the command
	execArgs := append([]string{"exec"}, args...)
	cmd := exec.Command("sprite", execArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("exec failed: %w: %s", err, output)
	}
	return string(output), nil
}

// ExecCommandStreaming executes a command and streams output line by line.
func ExecCommandStreaming(spriteName string, onLine func(line string), args ...string) error {
	// First ensure we're using the right sprite
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return fmt.Errorf("select sprite: %w", err)
	}

	// Execute the command with streaming
	execArgs := append([]string{"exec"}, args...)
	cmd := exec.Command("sprite", execArgs...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	// Read stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				for _, line := range strings.Split(string(buf[:n]), "\n") {
					if line != "" {
						onLine(line)
					}
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// Read stderr
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				for _, line := range strings.Split(string(buf[:n]), "\n") {
					if line != "" {
						onLine("[stderr] " + line)
					}
				}
			}
			if err != nil {
				break
			}
		}
	}()

	return cmd.Wait()
}

// CheckpointInfo contains information about a checkpoint.
type CheckpointInfo struct {
	ID      string `json:"id"`
	Comment string `json:"comment"`
}

// ListCheckpoints returns available checkpoints for a sprite.
func ListCheckpoints(spriteName string) ([]CheckpointInfo, error) {
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return nil, fmt.Errorf("select sprite: %w", err)
	}

	cmd := exec.Command("sprite", "checkpoint", "list", "--json")
	output, err := cmd.Output()
	if err != nil {
		// Try without --json flag
		cmd = exec.Command("sprite", "checkpoint", "list")
		output, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("list checkpoints: %w", err)
		}
		// Parse text output
		var checkpoints []CheckpointInfo
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "ID") {
				parts := strings.Fields(line)
				if len(parts) > 0 {
					checkpoints = append(checkpoints, CheckpointInfo{ID: parts[0]})
				}
			}
		}
		return checkpoints, nil
	}

	var checkpoints []CheckpointInfo
	if err := json.Unmarshal(output, &checkpoints); err != nil {
		return nil, fmt.Errorf("parse checkpoints: %w", err)
	}
	return checkpoints, nil
}

// CreateCheckpoint creates a new checkpoint.
func CreateCheckpoint(spriteName, comment string) error {
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return fmt.Errorf("select sprite: %w", err)
	}

	args := []string{"checkpoint", "create"}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	cmd := exec.Command("sprite", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}
	return nil
}

// RestoreCheckpoint restores from a checkpoint.
func RestoreCheckpoint(spriteName, checkpointID string) error {
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return fmt.Errorf("select sprite: %w", err)
	}

	cmd := exec.Command("sprite", "restore", checkpointID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restore checkpoint: %w", err)
	}
	return nil
}

// Destroy destroys a sprite.
func Destroy(spriteName string) error {
	useCmd := exec.Command("sprite", "use", spriteName)
	if err := useCmd.Run(); err != nil {
		return fmt.Errorf("select sprite: %w", err)
	}

	cmd := exec.Command("sprite", "destroy", "--force")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("destroy sprite: %w", err)
	}
	return nil
}

// IsEnabled returns true if sprites CLI is available and authenticated.
func IsEnabled(database *db.DB) bool {
	return IsAvailable()
}

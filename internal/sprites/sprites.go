// Package sprites provides shared functionality for Fly.io Sprites integration.
// Sprites are isolated VMs used as execution environments for Claude.
package sprites

import (
	"context"
	"fmt"
	"os"

	"github.com/bborn/workflow/internal/db"
	sdk "github.com/superfly/sprites-go"
)

// Settings keys for sprite configuration
const (
	SettingToken = "sprite_token" // Sprites API token
	SettingName  = "sprite_name"  // Name of the daemon sprite
)

// Default sprite name
const DefaultName = "task-daemon"

// GetToken returns the Sprites API token from env or database.
func GetToken(database *db.DB) string {
	// First try environment variable
	token := os.Getenv("SPRITES_TOKEN")
	if token != "" {
		return token
	}

	// Fall back to database setting
	if database != nil {
		token, _ = database.GetSetting(SettingToken)
	}
	return token
}

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

// NewClient creates a Sprites API client.
func NewClient(database *db.DB) (*sdk.Client, error) {
	token := GetToken(database)
	if token == "" {
		return nil, fmt.Errorf("no Sprites token configured. Set SPRITES_TOKEN env var or run: task sprite token <token>")
	}
	return sdk.New(token), nil
}

// EnsureRunning ensures the sprite is running and returns the sprite reference.
// Creates the sprite if it doesn't exist, restores from checkpoint if suspended.
func EnsureRunning(ctx context.Context, database *db.DB) (*sdk.Client, *sdk.Sprite, error) {
	client, err := NewClient(database)
	if err != nil {
		return nil, nil, err
	}

	spriteName := GetName(database)

	// Check if sprite exists
	sprite, err := client.GetSprite(ctx, spriteName)
	if err != nil {
		// Sprite doesn't exist - caller should create and set it up
		return client, nil, nil
	}

	// Restore from checkpoint if suspended
	if sprite.Status == "suspended" || sprite.Status == "stopped" {
		checkpoints, err := sprite.ListCheckpoints(ctx, "")
		if err != nil || len(checkpoints) == 0 {
			return nil, nil, fmt.Errorf("sprite is suspended but no checkpoints available")
		}

		restoreStream, err := sprite.RestoreCheckpoint(ctx, checkpoints[0].ID)
		if err != nil {
			return nil, nil, fmt.Errorf("restore checkpoint: %w", err)
		}

		if err := restoreStream.ProcessAll(func(msg *sdk.StreamMessage) error {
			return nil
		}); err != nil {
			return nil, nil, fmt.Errorf("restore failed: %w", err)
		}

		// Refresh sprite status
		sprite, err = client.GetSprite(ctx, spriteName)
		if err != nil {
			return nil, nil, err
		}
	}

	return client, sprite, nil
}

// IsEnabled returns true if sprites are configured (token is set).
func IsEnabled(database *db.DB) bool {
	return GetToken(database) != ""
}

// Package sprite provides Fly Sprites integration for isolated user environments.
package sprite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	sprites "github.com/superfly/sprites-go"
)

// Client wraps the Fly Sprites SDK client.
type Client struct {
	client *sprites.Client
}

// NewClient creates a new Sprites client.
// Uses SPRITES_TOKEN environment variable for authentication.
func NewClient() (*Client, error) {
	token := os.Getenv("SPRITES_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("SPRITES_TOKEN environment variable not set")
	}

	client := sprites.New(token)
	return &Client{client: client}, nil
}

// NewClientWithToken creates a new Sprites client with an explicit token.
func NewClientWithToken(token string) *Client {
	return &Client{client: sprites.New(token)}
}

// Sprite returns a handle to a specific sprite.
func (c *Client) Sprite(name string) *sprites.Sprite {
	return c.client.Sprite(name)
}

// CreateSprite creates a new sprite with the given name.
func (c *Client) CreateSprite(ctx context.Context, name string, opts *CreateOptions) error {
	sprite := c.client.Sprite(name)

	// Start the sprite (this creates it if it doesn't exist)
	cmd := sprite.CommandContext(ctx, "true") // Just run true to initialize
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create sprite: %w", err)
	}

	return nil
}

// CreateOptions contains options for creating a sprite.
type CreateOptions struct {
	Region string
}

// DestroySprite destroys a sprite.
func (c *Client) DestroySprite(ctx context.Context, name string) error {
	// The sprites-go SDK handles destruction through the API
	// For now, we'll use a direct HTTP call since the SDK may not expose this
	req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("https://api.sprites.dev/v1/sprites/%s", name), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+os.Getenv("SPRITES_TOKEN"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("destroy sprite: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("destroy sprite failed: %s - %s", resp.Status, string(body))
	}

	return nil
}

// RunCommand runs a command on a sprite and returns its output.
func (c *Client) RunCommand(ctx context.Context, name string, command string, args ...string) ([]byte, error) {
	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, command, args...)
	return cmd.Output()
}

// RunCommandCombined runs a command on a sprite and returns combined stdout/stderr.
func (c *Client) RunCommandCombined(ctx context.Context, name string, command string, args ...string) ([]byte, error) {
	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, command, args...)
	return cmd.CombinedOutput()
}

// StartCommand starts a command on a sprite without waiting for completion.
func (c *Client) StartCommand(ctx context.Context, name string, command string, args ...string) error {
	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, command, args...)
	return cmd.Start()
}

// GetSpriteStatus checks if a sprite is running.
func (c *Client) GetSpriteStatus(ctx context.Context, name string) (string, error) {
	// Try to run a simple command to check if sprite is accessible
	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, "echo", "ok")
	_, err := cmd.Output()
	if err != nil {
		return "stopped", nil
	}
	return "running", nil
}

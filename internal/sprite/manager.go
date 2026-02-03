package sprite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/hostdb"
)

// Manager handles sprite lifecycle management.
type Manager struct {
	client *Client
	db     *hostdb.DB
}

// NewManager creates a new sprite manager.
func NewManager(client *Client, db *hostdb.DB) *Manager {
	return &Manager{
		client: client,
		db:     db,
	}
}

// generateSpriteName generates a unique sprite name for a user.
func generateSpriteName() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("taskyou-%s", hex.EncodeToString(b))
}

// ProvisionSprite provisions a new sprite for a user.
// If the user already has a sprite, returns the existing one.
func (m *Manager) ProvisionSprite(ctx context.Context, userID string) (*hostdb.Sprite, error) {
	// Check if user already has a sprite
	existing, err := m.db.GetSpriteByUser(userID)
	if err != nil {
		return nil, fmt.Errorf("get existing sprite: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Create new sprite record
	spriteName := generateSpriteName()
	region := "ord" // Default to Chicago

	sprite, err := m.db.CreateSprite(userID, spriteName, region)
	if err != nil {
		return nil, fmt.Errorf("create sprite record: %w", err)
	}

	// Update status to creating
	if err := m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusCreating); err != nil {
		return nil, fmt.Errorf("update sprite status: %w", err)
	}

	// Create the actual Fly Sprite
	if err := m.client.CreateSprite(ctx, spriteName, &CreateOptions{Region: region}); err != nil {
		m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusError)
		return nil, fmt.Errorf("create fly sprite: %w", err)
	}

	// Initialize the sprite with taskd
	if err := m.initializeSprite(ctx, spriteName); err != nil {
		m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusError)
		return nil, fmt.Errorf("initialize sprite: %w", err)
	}

	// Update status to running
	if err := m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusRunning); err != nil {
		return nil, fmt.Errorf("update sprite status: %w", err)
	}

	sprite.Status = hostdb.SpriteStatusRunning
	return sprite, nil
}

// initializeSprite sets up a sprite with taskd and required dependencies.
func (m *Manager) initializeSprite(ctx context.Context, spriteName string) error {
	// Install taskd binary and start the web API
	// The sprite image should have taskd pre-installed, but we ensure it's running
	commands := []struct {
		name string
		args []string
	}{
		// Ensure data directory exists
		{"mkdir", []string{"-p", "/data"}},
		// Start taskd with web API in background
		// Note: In production, this would be managed by a process supervisor
		{"sh", []string{"-c", "nohup /usr/local/bin/taskd --web-api --addr :8080 > /var/log/taskd.log 2>&1 &"}},
	}

	for _, cmd := range commands {
		if _, err := m.client.RunCommandCombined(ctx, spriteName, cmd.name, cmd.args...); err != nil {
			// Log but don't fail - some commands might fail on subsequent runs
			continue
		}
	}

	// Wait for taskd to be ready
	for i := 0; i < 30; i++ {
		output, err := m.client.RunCommand(ctx, spriteName, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "http://localhost:8080/health")
		if err == nil && strings.TrimSpace(string(output)) == "200" {
			return nil
		}
		time.Sleep(time.Second)
	}

	return fmt.Errorf("taskd did not become ready in time")
}

// GetUserSprite retrieves the sprite for a user.
func (m *Manager) GetUserSprite(ctx context.Context, userID string) (*hostdb.Sprite, error) {
	sprite, err := m.db.GetSpriteByUser(userID)
	if err != nil {
		return nil, fmt.Errorf("get sprite: %w", err)
	}
	return sprite, nil
}

// GetSpriteStatus checks the current status of a sprite.
func (m *Manager) GetSpriteStatus(ctx context.Context, sprite *hostdb.Sprite) (hostdb.SpriteStatus, error) {
	status, err := m.client.GetSpriteStatus(ctx, sprite.SpriteName)
	if err != nil {
		return hostdb.SpriteStatusError, fmt.Errorf("get sprite status: %w", err)
	}

	switch status {
	case "running":
		return hostdb.SpriteStatusRunning, nil
	case "stopped":
		return hostdb.SpriteStatusStopped, nil
	default:
		return hostdb.SpriteStatusError, nil
	}
}

// StartSprite starts a stopped sprite.
func (m *Manager) StartSprite(ctx context.Context, sprite *hostdb.Sprite) error {
	// Run a command to wake up the sprite
	if err := m.client.StartCommand(ctx, sprite.SpriteName, "true"); err != nil {
		return fmt.Errorf("start sprite: %w", err)
	}

	// Re-initialize to ensure taskd is running
	if err := m.initializeSprite(ctx, sprite.SpriteName); err != nil {
		return fmt.Errorf("reinitialize sprite: %w", err)
	}

	if err := m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusRunning); err != nil {
		return fmt.Errorf("update sprite status: %w", err)
	}

	return nil
}

// StopSprite stops a running sprite.
func (m *Manager) StopSprite(ctx context.Context, sprite *hostdb.Sprite) error {
	// Sprites auto-stop when idle, but we can explicitly stop them
	// by killing the taskd process
	m.client.RunCommand(ctx, sprite.SpriteName, "pkill", "-f", "taskd")

	if err := m.db.UpdateSpriteStatus(sprite.ID, hostdb.SpriteStatusStopped); err != nil {
		return fmt.Errorf("update sprite status: %w", err)
	}

	return nil
}

// DestroySprite destroys a sprite and removes its record.
func (m *Manager) DestroySprite(ctx context.Context, sprite *hostdb.Sprite) error {
	// Destroy the Fly Sprite
	if err := m.client.DestroySprite(ctx, sprite.SpriteName); err != nil {
		return fmt.Errorf("destroy fly sprite: %w", err)
	}

	// Delete the record
	if err := m.db.DeleteSprite(sprite.ID); err != nil {
		return fmt.Errorf("delete sprite record: %w", err)
	}

	return nil
}

// GetSSHConfig returns SSH configuration for connecting to a sprite.
func (m *Manager) GetSSHConfig(ctx context.Context, sprite *hostdb.Sprite) (string, error) {
	// The SSH host is the sprite URL
	sshHost := fmt.Sprintf("%s.sprites.dev", sprite.SpriteName)

	config := fmt.Sprintf(`Host %s
    HostName %s
    User root
    Port 22
    IdentityFile ~/.ssh/id_ed25519
`, sprite.SpriteName, sshHost)

	return config, nil
}

// ExportDatabase exports the sprite's SQLite database.
func (m *Manager) ExportDatabase(ctx context.Context, sprite *hostdb.Sprite) ([]byte, error) {
	// Create a backup of the database
	_, err := m.client.RunCommand(ctx, sprite.SpriteName, "sqlite3", "/data/tasks.db", ".backup /tmp/export.db")
	if err != nil {
		return nil, fmt.Errorf("backup database: %w", err)
	}

	// Read the backup file
	output, err := m.client.RunCommand(ctx, sprite.SpriteName, "cat", "/tmp/export.db")
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}

	// Clean up
	m.client.RunCommand(ctx, sprite.SpriteName, "rm", "/tmp/export.db")

	return output, nil
}

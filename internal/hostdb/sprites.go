package hostdb

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SpriteStatus represents the status of a sprite instance.
type SpriteStatus string

const (
	SpriteStatusPending  SpriteStatus = "pending"
	SpriteStatusCreating SpriteStatus = "creating"
	SpriteStatusRunning  SpriteStatus = "running"
	SpriteStatusStopped  SpriteStatus = "stopped"
	SpriteStatusError    SpriteStatus = "error"
)

// Sprite represents a Fly Sprite instance for a user.
type Sprite struct {
	ID             string
	UserID         string
	SpriteName     string // Fly sprite name (e.g., "taskyou-abc123")
	Status         SpriteStatus
	Region         string // Fly region (e.g., "ord")
	SSHPublicKey   string
	LastCheckpoint string // Last checkpoint ID for restore
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateSprite creates a new sprite record for a user.
func (db *DB) CreateSprite(userID, spriteName, region string) (*Sprite, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := db.Exec(`
		INSERT INTO sprites (id, user_id, sprite_name, status, region, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, userID, spriteName, SpriteStatusPending, region, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert sprite: %w", err)
	}

	return &Sprite{
		ID:         id,
		UserID:     userID,
		SpriteName: spriteName,
		Status:     SpriteStatusPending,
		Region:     region,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// GetSpriteByID retrieves a sprite by its ID.
func (db *DB) GetSpriteByID(id string) (*Sprite, error) {
	var s Sprite
	err := db.QueryRow(`
		SELECT id, user_id, sprite_name, status, region, ssh_public_key, last_checkpoint, created_at, updated_at
		FROM sprites WHERE id = ?
	`, id).Scan(&s.ID, &s.UserID, &s.SpriteName, &s.Status, &s.Region, &s.SSHPublicKey, &s.LastCheckpoint, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query sprite: %w", err)
	}
	return &s, nil
}

// GetSpriteByUser retrieves the sprite for a user.
// Each user has at most one sprite.
func (db *DB) GetSpriteByUser(userID string) (*Sprite, error) {
	var s Sprite
	err := db.QueryRow(`
		SELECT id, user_id, sprite_name, status, region, ssh_public_key, last_checkpoint, created_at, updated_at
		FROM sprites WHERE user_id = ?
	`, userID).Scan(&s.ID, &s.UserID, &s.SpriteName, &s.Status, &s.Region, &s.SSHPublicKey, &s.LastCheckpoint, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query sprite: %w", err)
	}
	return &s, nil
}

// GetSpriteByName retrieves a sprite by its Fly sprite name.
func (db *DB) GetSpriteByName(spriteName string) (*Sprite, error) {
	var s Sprite
	err := db.QueryRow(`
		SELECT id, user_id, sprite_name, status, region, ssh_public_key, last_checkpoint, created_at, updated_at
		FROM sprites WHERE sprite_name = ?
	`, spriteName).Scan(&s.ID, &s.UserID, &s.SpriteName, &s.Status, &s.Region, &s.SSHPublicKey, &s.LastCheckpoint, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query sprite: %w", err)
	}
	return &s, nil
}

// UpdateSpriteStatus updates the status of a sprite.
func (db *DB) UpdateSpriteStatus(id string, status SpriteStatus) error {
	_, err := db.Exec(`
		UPDATE sprites SET status = ?, updated_at = ?
		WHERE id = ?
	`, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update sprite status: %w", err)
	}
	return nil
}

// UpdateSpriteSSHKey updates the SSH public key for a sprite.
func (db *DB) UpdateSpriteSSHKey(id, sshPublicKey string) error {
	_, err := db.Exec(`
		UPDATE sprites SET ssh_public_key = ?, updated_at = ?
		WHERE id = ?
	`, sshPublicKey, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update sprite ssh key: %w", err)
	}
	return nil
}

// UpdateSpriteCheckpoint updates the last checkpoint ID for a sprite.
func (db *DB) UpdateSpriteCheckpoint(id, checkpointID string) error {
	_, err := db.Exec(`
		UPDATE sprites SET last_checkpoint = ?, updated_at = ?
		WHERE id = ?
	`, checkpointID, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update sprite checkpoint: %w", err)
	}
	return nil
}

// DeleteSprite deletes a sprite record.
func (db *DB) DeleteSprite(id string) error {
	_, err := db.Exec(`DELETE FROM sprites WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete sprite: %w", err)
	}
	return nil
}

// ListSprites retrieves all sprites.
func (db *DB) ListSprites() ([]*Sprite, error) {
	rows, err := db.Query(`
		SELECT id, user_id, sprite_name, status, region, ssh_public_key, last_checkpoint, created_at, updated_at
		FROM sprites ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query sprites: %w", err)
	}
	defer rows.Close()

	var sprites []*Sprite
	for rows.Next() {
		var s Sprite
		if err := rows.Scan(&s.ID, &s.UserID, &s.SpriteName, &s.Status, &s.Region, &s.SSHPublicKey, &s.LastCheckpoint, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sprite: %w", err)
		}
		sprites = append(sprites, &s)
	}

	return sprites, nil
}

// ListSpritesByStatus retrieves all sprites with a specific status.
func (db *DB) ListSpritesByStatus(status SpriteStatus) ([]*Sprite, error) {
	rows, err := db.Query(`
		SELECT id, user_id, sprite_name, status, region, ssh_public_key, last_checkpoint, created_at, updated_at
		FROM sprites WHERE status = ? ORDER BY created_at DESC
	`, status)
	if err != nil {
		return nil, fmt.Errorf("query sprites: %w", err)
	}
	defer rows.Close()

	var sprites []*Sprite
	for rows.Next() {
		var s Sprite
		if err := rows.Scan(&s.ID, &s.UserID, &s.SpriteName, &s.Status, &s.Region, &s.SSHPublicKey, &s.LastCheckpoint, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sprite: %w", err)
		}
		sprites = append(sprites, &s)
	}

	return sprites, nil
}

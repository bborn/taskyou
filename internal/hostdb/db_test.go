package hostdb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	tmpDir, err := os.MkdirTemp("", "hostdb-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestCreateAndGetUser(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create user
	user, err := db.CreateUser("test@example.com", "Test User", "https://example.com/avatar.png")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if user.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", user.Email)
	}
	if user.Name != "Test User" {
		t.Errorf("expected name Test User, got %s", user.Name)
	}

	// Get by ID
	retrieved, err := db.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("failed to get user by ID: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected user, got nil")
	}
	if retrieved.Email != user.Email {
		t.Errorf("expected email %s, got %s", user.Email, retrieved.Email)
	}

	// Get by email
	retrieved, err = db.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("failed to get user by email: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected user, got nil")
	}
}

func TestGetOrCreateUserByOAuth(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	expiresAt := time.Now().Add(time.Hour)

	// First call should create user
	user1, isNew1, err := db.GetOrCreateUserByOAuth(
		"google", "12345", "test@example.com", "Test User", "https://example.com/avatar.png",
		"access_token", "refresh_token", &expiresAt,
	)
	if err != nil {
		t.Fatalf("failed to get or create user: %v", err)
	}
	if !isNew1 {
		t.Error("expected new user to be created")
	}
	if user1.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", user1.Email)
	}

	// Second call with same OAuth should return existing user
	user2, isNew2, err := db.GetOrCreateUserByOAuth(
		"google", "12345", "test@example.com", "Updated Name", "https://example.com/new-avatar.png",
		"new_access_token", "new_refresh_token", &expiresAt,
	)
	if err != nil {
		t.Fatalf("failed to get or create user: %v", err)
	}
	if isNew2 {
		t.Error("expected existing user, not new")
	}
	if user2.ID != user1.ID {
		t.Errorf("expected same user ID %s, got %s", user1.ID, user2.ID)
	}
	// Name should be updated
	if user2.Name != "Updated Name" {
		t.Errorf("expected updated name, got %s", user2.Name)
	}
}

func TestCreateAndGetSprite(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create user first
	user, err := db.CreateUser("test@example.com", "Test User", "")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create sprite
	sprite, err := db.CreateSprite(user.ID, "taskyou-abc123", "ord")
	if err != nil {
		t.Fatalf("failed to create sprite: %v", err)
	}

	if sprite.SpriteName != "taskyou-abc123" {
		t.Errorf("expected sprite name taskyou-abc123, got %s", sprite.SpriteName)
	}
	if sprite.Status != SpriteStatusPending {
		t.Errorf("expected status pending, got %s", sprite.Status)
	}

	// Get by user
	retrieved, err := db.GetSpriteByUser(user.ID)
	if err != nil {
		t.Fatalf("failed to get sprite by user: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected sprite, got nil")
	}
	if retrieved.ID != sprite.ID {
		t.Errorf("expected sprite ID %s, got %s", sprite.ID, retrieved.ID)
	}

	// Update status
	err = db.UpdateSpriteStatus(sprite.ID, SpriteStatusRunning)
	if err != nil {
		t.Fatalf("failed to update sprite status: %v", err)
	}

	retrieved, _ = db.GetSpriteByID(sprite.ID)
	if retrieved.Status != SpriteStatusRunning {
		t.Errorf("expected status running, got %s", retrieved.Status)
	}
}

func TestSessions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create user
	user, err := db.CreateUser("test@example.com", "Test User", "")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create session
	session, err := db.CreateSession(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if session.UserID != user.ID {
		t.Errorf("expected user ID %s, got %s", user.ID, session.UserID)
	}

	// Get session
	retrieved, err := db.GetSession(session.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected session, got nil")
	}

	// Get session with user
	sess, usr, err := db.GetSessionWithUser(session.ID)
	if err != nil {
		t.Fatalf("failed to get session with user: %v", err)
	}
	if sess == nil || usr == nil {
		t.Fatal("expected session and user")
	}
	if usr.ID != user.ID {
		t.Errorf("expected user ID %s, got %s", user.ID, usr.ID)
	}

	// Delete session
	err = db.DeleteSession(session.ID)
	if err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	// Verify deleted
	retrieved, _ = db.GetSession(session.ID)
	if retrieved != nil {
		t.Error("expected session to be deleted")
	}
}

func TestExpiredSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create user
	user, err := db.CreateUser("test@example.com", "Test User", "")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create already-expired session
	session, err := db.CreateSession(user.ID, -time.Hour) // Negative duration = already expired
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Should not find expired session
	retrieved, err := db.GetSession(session.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil for expired session")
	}

	// Cleanup expired sessions
	count, err := db.CleanupExpiredSessions()
	if err != nil {
		t.Fatalf("failed to cleanup sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired session cleaned up, got %d", count)
	}
}

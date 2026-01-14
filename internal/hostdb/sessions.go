package hostdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"
)

// Session represents an authenticated user session.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// DefaultSessionDuration is the default duration for sessions.
const DefaultSessionDuration = 30 * 24 * time.Hour // 30 days

// generateSessionID generates a cryptographically secure session ID.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// CreateSession creates a new session for a user.
func (db *DB) CreateSession(userID string, duration time.Duration) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(duration)

	_, err = db.Exec(`
		INSERT INTO sessions (id, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, id, userID, expiresAt, now)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return &Session{
		ID:        id,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}, nil
}

// GetSession retrieves a session by its ID.
// Returns nil if the session doesn't exist or has expired.
func (db *DB) GetSession(id string) (*Session, error) {
	var s Session
	err := db.QueryRow(`
		SELECT id, user_id, expires_at, created_at
		FROM sessions WHERE id = ? AND expires_at > ?
	`, id, time.Now()).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	return &s, nil
}

// GetSessionWithUser retrieves a session and its associated user.
// Returns nil for both if the session doesn't exist or has expired.
func (db *DB) GetSessionWithUser(sessionID string) (*Session, *User, error) {
	session, err := db.GetSession(sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get session: %w", err)
	}
	if session == nil {
		return nil, nil, nil
	}

	user, err := db.GetUserByID(session.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get user: %w", err)
	}

	return session, user, nil
}

// DeleteSession deletes a session.
func (db *DB) DeleteSession(id string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions deletes all sessions for a user.
func (db *DB) DeleteUserSessions(userID string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// CleanupExpiredSessions removes all expired sessions from the database.
func (db *DB) CleanupExpiredSessions() (int64, error) {
	result, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	count, _ := result.RowsAffected()
	return count, nil
}

// ExtendSession extends the expiration of a session.
func (db *DB) ExtendSession(id string, duration time.Duration) error {
	expiresAt := time.Now().Add(duration)
	_, err := db.Exec(`
		UPDATE sessions SET expires_at = ?
		WHERE id = ?
	`, expiresAt, id)
	if err != nil {
		return fmt.Errorf("extend session: %w", err)
	}
	return nil
}

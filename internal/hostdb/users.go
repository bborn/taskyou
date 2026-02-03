package hostdb

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// User represents an authenticated user.
type User struct {
	ID        string
	Email     string
	Name      string
	AvatarURL string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OAuthAccount represents a linked OAuth provider account.
type OAuthAccount struct {
	ID                string
	UserID            string
	Provider          string // "google", "github"
	ProviderAccountID string
	AccessToken       string
	RefreshToken      string
	ExpiresAt         *time.Time
	CreatedAt         time.Time
}

// CreateUser creates a new user and returns the created user.
func (db *DB) CreateUser(email, name, avatarURL string) (*User, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := db.Exec(`
		INSERT INTO users (id, email, name, avatar_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, email, name, avatarURL, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return &User{
		ID:        id,
		Email:     email,
		Name:      name,
		AvatarURL: avatarURL,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetUserByID retrieves a user by their ID.
func (db *DB) GetUserByID(id string) (*User, error) {
	var u User
	err := db.QueryRow(`
		SELECT id, email, name, avatar_url, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &u, nil
}

// GetUserByEmail retrieves a user by their email.
func (db *DB) GetUserByEmail(email string) (*User, error) {
	var u User
	err := db.QueryRow(`
		SELECT id, email, name, avatar_url, created_at, updated_at
		FROM users WHERE email = ?
	`, email).Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &u, nil
}

// UpdateUser updates a user's profile information.
func (db *DB) UpdateUser(id, name, avatarURL string) error {
	_, err := db.Exec(`
		UPDATE users SET name = ?, avatar_url = ?, updated_at = ?
		WHERE id = ?
	`, name, avatarURL, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// DeleteUser deletes a user and all associated data.
func (db *DB) DeleteUser(id string) error {
	_, err := db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// CreateOAuthAccount creates a new OAuth account link for a user.
func (db *DB) CreateOAuthAccount(userID, provider, providerAccountID, accessToken, refreshToken string, expiresAt *time.Time) (*OAuthAccount, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := db.Exec(`
		INSERT INTO oauth_accounts (id, user_id, provider, provider_account_id, access_token, refresh_token, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, userID, provider, providerAccountID, accessToken, refreshToken, expiresAt, now)
	if err != nil {
		return nil, fmt.Errorf("insert oauth account: %w", err)
	}

	return &OAuthAccount{
		ID:                id,
		UserID:            userID,
		Provider:          provider,
		ProviderAccountID: providerAccountID,
		AccessToken:       accessToken,
		RefreshToken:      refreshToken,
		ExpiresAt:         expiresAt,
		CreatedAt:         now,
	}, nil
}

// GetOAuthAccount retrieves an OAuth account by provider and provider account ID.
func (db *DB) GetOAuthAccount(provider, providerAccountID string) (*OAuthAccount, error) {
	var oa OAuthAccount
	var expiresAt sql.NullTime

	err := db.QueryRow(`
		SELECT id, user_id, provider, provider_account_id, access_token, refresh_token, expires_at, created_at
		FROM oauth_accounts WHERE provider = ? AND provider_account_id = ?
	`, provider, providerAccountID).Scan(
		&oa.ID, &oa.UserID, &oa.Provider, &oa.ProviderAccountID,
		&oa.AccessToken, &oa.RefreshToken, &expiresAt, &oa.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query oauth account: %w", err)
	}

	if expiresAt.Valid {
		oa.ExpiresAt = &expiresAt.Time
	}

	return &oa, nil
}

// UpdateOAuthTokens updates the tokens for an OAuth account.
func (db *DB) UpdateOAuthTokens(id, accessToken, refreshToken string, expiresAt *time.Time) error {
	_, err := db.Exec(`
		UPDATE oauth_accounts SET access_token = ?, refresh_token = ?, expires_at = ?
		WHERE id = ?
	`, accessToken, refreshToken, expiresAt, id)
	if err != nil {
		return fmt.Errorf("update oauth tokens: %w", err)
	}
	return nil
}

// GetOAuthAccountsByUser retrieves all OAuth accounts for a user.
func (db *DB) GetOAuthAccountsByUser(userID string) ([]*OAuthAccount, error) {
	rows, err := db.Query(`
		SELECT id, user_id, provider, provider_account_id, access_token, refresh_token, expires_at, created_at
		FROM oauth_accounts WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query oauth accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*OAuthAccount
	for rows.Next() {
		var oa OAuthAccount
		var expiresAt sql.NullTime

		if err := rows.Scan(
			&oa.ID, &oa.UserID, &oa.Provider, &oa.ProviderAccountID,
			&oa.AccessToken, &oa.RefreshToken, &expiresAt, &oa.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan oauth account: %w", err)
		}

		if expiresAt.Valid {
			oa.ExpiresAt = &expiresAt.Time
		}

		accounts = append(accounts, &oa)
	}

	return accounts, nil
}

// GetOrCreateUserByOAuth gets or creates a user based on OAuth login.
// Returns the user and a boolean indicating if the user was newly created.
func (db *DB) GetOrCreateUserByOAuth(provider, providerAccountID, email, name, avatarURL, accessToken, refreshToken string, expiresAt *time.Time) (*User, bool, error) {
	// Check if OAuth account already exists
	oa, err := db.GetOAuthAccount(provider, providerAccountID)
	if err != nil {
		return nil, false, fmt.Errorf("get oauth account: %w", err)
	}

	if oa != nil {
		// Account exists, update tokens and return user
		if err := db.UpdateOAuthTokens(oa.ID, accessToken, refreshToken, expiresAt); err != nil {
			return nil, false, fmt.Errorf("update oauth tokens: %w", err)
		}

		user, err := db.GetUserByID(oa.UserID)
		if err != nil {
			return nil, false, fmt.Errorf("get user: %w", err)
		}

		// Update user profile if changed
		if user.Name != name || user.AvatarURL != avatarURL {
			if err := db.UpdateUser(user.ID, name, avatarURL); err != nil {
				return nil, false, fmt.Errorf("update user: %w", err)
			}
			user.Name = name
			user.AvatarURL = avatarURL
		}

		return user, false, nil
	}

	// Check if user with this email already exists
	user, err := db.GetUserByEmail(email)
	if err != nil {
		return nil, false, fmt.Errorf("get user by email: %w", err)
	}

	if user != nil {
		// User exists, link OAuth account
		_, err = db.CreateOAuthAccount(user.ID, provider, providerAccountID, accessToken, refreshToken, expiresAt)
		if err != nil {
			return nil, false, fmt.Errorf("create oauth account: %w", err)
		}
		return user, false, nil
	}

	// Create new user
	user, err = db.CreateUser(email, name, avatarURL)
	if err != nil {
		return nil, false, fmt.Errorf("create user: %w", err)
	}

	// Create OAuth account link
	_, err = db.CreateOAuthAccount(user.ID, provider, providerAccountID, accessToken, refreshToken, expiresAt)
	if err != nil {
		return nil, false, fmt.Errorf("create oauth account: %w", err)
	}

	return user, true, nil
}

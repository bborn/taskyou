// Package hostdb provides SQLite database operations for the web host service.
// This database stores only authentication data, user accounts, and sprite mappings.
// User task data is stored separately in each user's Fly Sprite.
package hostdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database connection for the host database.
type DB struct {
	*sql.DB
	path string
}

// Path returns the path to the database file.
func (db *DB) Path() string {
	return db.path
}

// Open opens or creates a SQLite database at the given path.
func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	// Add busy timeout to handle concurrent access
	dsn := path + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent access
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	wrapped := &DB{DB: db, path: path}

	// Run migrations
	if err := wrapped.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return wrapped, nil
}

// migrate runs database migrations.
func (db *DB) migrate() error {
	migrations := []string{
		// Users table - authenticated users
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name TEXT DEFAULT '',
			avatar_url TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// OAuth accounts linked to users
		`CREATE TABLE IF NOT EXISTS oauth_accounts (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			provider_account_id TEXT NOT NULL,
			access_token TEXT DEFAULT '',
			refresh_token TEXT DEFAULT '',
			expires_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, provider_account_id)
		)`,

		// Sprite instances per user
		`CREATE TABLE IF NOT EXISTS sprites (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			sprite_name TEXT NOT NULL UNIQUE,
			status TEXT DEFAULT 'pending',
			region TEXT DEFAULT 'ord',
			ssh_public_key TEXT DEFAULT '',
			last_checkpoint TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Sessions for cookie-based session management
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_oauth_accounts_user ON oauth_accounts(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_accounts_provider ON oauth_accounts(provider, provider_account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sprites_user ON sprites(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	return nil
}

// DefaultPath returns the default database path for the host service.
func DefaultPath() string {
	// Check for explicit path
	if p := os.Getenv("TASKWEB_DATABASE_PATH"); p != "" {
		return p
	}

	// Default to ~/.local/share/taskweb/taskweb.db
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "taskweb", "taskweb.db")
}

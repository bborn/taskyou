package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// SavedView is a named filter query — "the tasks I care about right now".
//
// A view is nothing but its query string, in the grammar internal/taskfilter
// parses ("status:in-progress status:blocked", "is:pinned", "[offerlab]"). That
// keeps every surface honest: the TUI, the CLI and the HTTP API all resolve a
// view name to the same string and run the same matcher over it, so a view
// cannot mean one thing on the board and another in a script.
type SavedView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	SortOrder int       `json:"sort_order"`
	CreatedAt LocalTime `json:"created_at"`
	UpdatedAt LocalTime `json:"updated_at"`
}

// MaxSavedViewName bounds a view name so it stays renderable in the TUI picker
// and in `ty views list`.
const MaxSavedViewName = 40

// NormalizeViewName trims a view name and folds internal whitespace, so
// "  In  Progress " and "In Progress" are the same view rather than two rows
// that look identical in the picker.
func NormalizeViewName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// ValidateViewName reports why a view name is unusable, or nil when it is fine.
func ValidateViewName(name string) error {
	name = NormalizeViewName(name)
	if name == "" {
		return fmt.Errorf("view name cannot be empty")
	}
	if len(name) > MaxSavedViewName {
		return fmt.Errorf("view name is too long (max %d characters)", MaxSavedViewName)
	}
	return nil
}

// ListSavedViews returns every saved view in display order.
func (db *DB) ListSavedViews() ([]*SavedView, error) {
	rows, err := db.Query(`
		SELECT id, name, query, sort_order, created_at, updated_at
		FROM saved_views ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query saved views: %w", err)
	}
	defer rows.Close()

	var views []*SavedView
	for rows.Next() {
		v := &SavedView{}
		if err := rows.Scan(&v.ID, &v.Name, &v.Query, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan saved view: %w", err)
		}
		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saved views: %w", err)
	}
	return views, nil
}

// GetSavedView looks a view up by name, case-insensitively. It returns
// (nil, nil) when no such view exists, so callers can tell "missing" from
// "broken" without inspecting the error.
func (db *DB) GetSavedView(name string) (*SavedView, error) {
	v := &SavedView{}
	err := db.QueryRow(`
		SELECT id, name, query, sort_order, created_at, updated_at
		FROM saved_views WHERE name = ? COLLATE NOCASE
	`, NormalizeViewName(name)).Scan(&v.ID, &v.Name, &v.Query, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query saved view: %w", err)
	}
	return v, nil
}

// ErrViewAlreadyExists is returned by RenameView when the target name is
// already taken by a different saved view. The handler maps it to 409 so the
// caller's source view is preserved untouched.
var ErrViewAlreadyExists = errors.New("a saved view with that name already exists")

// ErrViewNotFound is returned by RenameView when the source view no longer
// exists when the transaction commits — a concurrent request deleted it
// between the handler's lookup and the write. The handler maps it to 404.
var ErrViewNotFound = errors.New("saved view not found")

// SaveView creates or updates a view by name and returns the stored row.
// Saving over an existing name replaces its query, which is what "save current
// filter as <name>" should do when the name is already taken.
func (db *DB) SaveView(name, query string) (*SavedView, error) {
	name = NormalizeViewName(name)
	if err := ValidateViewName(name); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)

	// New views land at the end of the list. A single upsert makes the
	// existence check, the sort_order computation and the write atomic at the
	// statement level: splitting the SELECT COALESCE(MAX(...)) from the INSERT
	// leaves a window in which two concurrent callers both read the same MAX
	// and both insert with the same sort_order, which has no UNIQUE constraint
	// and so is silently stored, making ListSavedViews render the collided
	// subset alphabetically rather than in creation order. SQLite holds the
	// write lock for the whole statement, so the subquery reads within that
	// lock and cannot interleave with another writer, in-process or not.
	if _, err := db.Exec(`
		INSERT INTO saved_views (name, query, sort_order)
		VALUES (?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM saved_views))
		ON CONFLICT(name) DO UPDATE SET
			query = excluded.query,
			updated_at = CURRENT_TIMESTAMP
	`, name, query); err != nil {
		return nil, fmt.Errorf("save saved view: %w", err)
	}
	return db.GetSavedView(name)
}

// RenameView atomically renames (and re-queries) a saved view. The conflict
// check, the insert of the new row, and the deletion of the source all run in
// a single transaction guarded by viewsMu, so two concurrent renames onto the
// same fresh target name cannot both succeed: exactly one wins and returns the
// new row, the others get ErrViewAlreadyExists with their source views left
// intact. This closes the TOCTOU window the old handleUpdateView left between
// its GetSavedView conflict check and the subsequent SaveView/DeleteSavedView,
// which let a loser overwrite the winner's target row and then delete its own
// source — silent, irreversible data loss returned to the client as a 200.
func (db *DB) RenameView(oldName, newName, query string) (*SavedView, error) {
	oldName = NormalizeViewName(oldName)
	newName = NormalizeViewName(newName)
	if err := ValidateViewName(newName); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)

	// Serialize renames within this process regardless of the connection-pool
	// size. Combined with the transaction below, the read-check-write becomes
	// atomic in-process; the conditional INSERT makes it atomic across
	// processes (SQLite serializes writers, so a concurrent rename that
	// commits first makes this one's INSERT match zero rows).
	db.viewsMu.Lock()
	defer db.viewsMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin rename transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Re-check the source under the write lock. The handler's requireView saw
	// it, but a concurrent request may have deleted it since; refuse rather
	// than resurrect a deleted view under a new name.
	var srcID int64
	err = tx.QueryRow(`SELECT id FROM saved_views WHERE name = ? COLLATE NOCASE`, oldName).Scan(&srcID)
	if err == sql.ErrNoRows {
		return nil, ErrViewNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load source view: %w", err)
	}

	// Insert the target only if the name is free. ON CONFLICT DO UPDATE would
	// silently upsert (and so overwrite the conflicting row); the
	// WHERE NOT EXISTS form reports the collision via RowsAffected, which is
	// the 409 the rename guard is supposed to return. Held under the same
	// transaction lock as the eventual commit, a concurrent rename cannot
	// slip a row in between this check and the write.
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) + 1 FROM saved_views`).Scan(&next); err != nil {
		return nil, fmt.Errorf("next saved view order: %w", err)
	}
	res, err := tx.Exec(`
		INSERT INTO saved_views (name, query, sort_order)
		SELECT ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM saved_views WHERE name = ? COLLATE NOCASE)
	`, newName, query, next, newName)
	if err != nil {
		return nil, fmt.Errorf("insert renamed view: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("check renamed insert: %w", err)
	}
	if inserted == 0 {
		return nil, ErrViewAlreadyExists
	}

	// Drop the original only after the replacement exists, inside the same
	// transaction so a crash between them cannot leave both rows.
	if _, err := tx.Exec(`DELETE FROM saved_views WHERE id = ?`, srcID); err != nil {
		return nil, fmt.Errorf("delete source view: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit rename transaction: %w", err)
	}
	return db.GetSavedView(newName)
}

// DeleteSavedView removes a view by name. Deleting a view that does not exist
// is not an error — the end state the caller asked for is already true.
func (db *DB) DeleteSavedView(name string) error {
	if _, err := db.Exec(`DELETE FROM saved_views WHERE name = ? COLLATE NOCASE`, NormalizeViewName(name)); err != nil {
		return fmt.Errorf("delete saved view: %w", err)
	}
	return nil
}

// savedViewSeedKey guards the one-time seeding of starter views below. It is a
// settings marker rather than a "is the table empty?" check so a user who
// deletes the starters does not get them back on the next launch.
const savedViewSeedKey = "migration:seed_saved_views_v1"

// defaultSavedViews are the starter views seeded on first run. They exist to
// make the feature discoverable: an empty picker teaches nobody the grammar.
var defaultSavedViews = []SavedView{
	{Name: "Active", Query: "status:in-progress status:blocked", SortOrder: 1},
	{Name: "Pinned", Query: "is:pinned", SortOrder: 2},
	{Name: "In review", Query: "has:pr status:blocked", SortOrder: 3},
}

// seedDefaultSavedViews inserts the starter views exactly once.
func (db *DB) seedDefaultSavedViews() error {
	if done, _ := db.GetSetting(savedViewSeedKey); done != "" {
		return nil
	}
	for _, v := range defaultSavedViews {
		if _, err := db.Exec(`
			INSERT OR IGNORE INTO saved_views (name, query, sort_order) VALUES (?, ?, ?)
		`, v.Name, v.Query, v.SortOrder); err != nil {
			return fmt.Errorf("seed saved view %q: %w", v.Name, err)
		}
	}
	return db.SetSetting(savedViewSeedKey, "done")
}

package db

import (
	"database/sql"
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

// SaveView creates or updates a view by name and returns the stored row.
// Saving over an existing name replaces its query, which is what "save current
// filter as <name>" should do when the name is already taken.
func (db *DB) SaveView(name, query string) (*SavedView, error) {
	name = NormalizeViewName(name)
	if err := ValidateViewName(name); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)

	existing, err := db.GetSavedView(name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if _, err := db.Exec(`
			UPDATE saved_views SET query = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
		`, query, existing.ID); err != nil {
			return nil, fmt.Errorf("update saved view: %w", err)
		}
		return db.GetSavedView(name)
	}

	// New views land at the end of the list.
	var next int
	if err := db.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) + 1 FROM saved_views`).Scan(&next); err != nil {
		return nil, fmt.Errorf("next saved view order: %w", err)
	}
	if _, err := db.Exec(`
		INSERT INTO saved_views (name, query, sort_order) VALUES (?, ?, ?)
	`, name, query, next); err != nil {
		return nil, fmt.Errorf("insert saved view: %w", err)
	}
	return db.GetSavedView(name)
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

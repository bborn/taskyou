package db

import (
	"path/filepath"
	"testing"
)

func openViewTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestSavedViewCRUD(t *testing.T) {
	database := openViewTestDB(t)

	v, err := database.SaveView("Offerlab", "[offerlab] status:open")
	if err != nil {
		t.Fatalf("save view: %v", err)
	}
	if v.Name != "Offerlab" || v.Query != "[offerlab] status:open" {
		t.Fatalf("stored view = %+v", v)
	}

	got, err := database.GetSavedView("offerlab")
	if err != nil || got == nil {
		t.Fatalf("lookup must be case-insensitive: %v %+v", err, got)
	}

	// Saving over a name replaces the query rather than creating a twin.
	if _, err := database.SaveView("offerlab", "[offerlab] is:pinned"); err != nil {
		t.Fatalf("resave: %v", err)
	}
	got, _ = database.GetSavedView("Offerlab")
	if got.Query != "[offerlab] is:pinned" {
		t.Errorf("query not replaced: %q", got.Query)
	}
	if got.ID != v.ID {
		t.Errorf("resave created a new row (%d -> %d)", v.ID, got.ID)
	}

	if err := database.DeleteSavedView("OFFERLAB"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = database.GetSavedView("offerlab")
	if err != nil {
		t.Fatalf("lookup after delete: %v", err)
	}
	if got != nil {
		t.Error("view should be gone")
	}

	// Deleting again is a no-op, not an error: the requested end state holds.
	if err := database.DeleteSavedView("offerlab"); err != nil {
		t.Errorf("second delete should be a no-op: %v", err)
	}
}

func TestSavedViewMissingIsNotAnError(t *testing.T) {
	database := openViewTestDB(t)
	v, err := database.GetSavedView("nope")
	if err != nil {
		t.Fatalf("missing view must not error: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil view, got %+v", v)
	}
}

func TestSavedViewNameValidation(t *testing.T) {
	database := openViewTestDB(t)

	if _, err := database.SaveView("   ", "is:pinned"); err == nil {
		t.Error("blank name should be rejected")
	}
	long := make([]byte, MaxSavedViewName+1)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := database.SaveView(string(long), "is:pinned"); err == nil {
		t.Error("over-long name should be rejected")
	}

	// Internal whitespace folds, so a name cannot be duplicated by spacing.
	if _, err := database.SaveView("Hot   Fixes", "has:pr"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := database.GetSavedView("Hot Fixes")
	if got == nil || got.Name != "Hot Fixes" {
		t.Errorf("whitespace not folded: %+v", got)
	}
}

func TestDefaultSavedViewsSeededOnce(t *testing.T) {
	database := openViewTestDB(t)

	views, err := database.ListSavedViews()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != len(defaultSavedViews) {
		t.Fatalf("expected %d starter views, got %d", len(defaultSavedViews), len(views))
	}
	if views[0].Name != "Active" {
		t.Errorf("starter views should list in sort order, got %q first", views[0].Name)
	}

	// A user who deletes a starter must not have it resurrected by the next
	// migrate() — that is what the settings marker is for.
	if err := database.DeleteSavedView("Pinned"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := database.seedDefaultSavedViews(); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if v, _ := database.GetSavedView("Pinned"); v != nil {
		t.Error("deleted starter view came back")
	}
}

func TestSavedViewsListInInsertionOrder(t *testing.T) {
	database := openViewTestDB(t)
	for _, name := range []string{"Zulu", "Alpha"} {
		if _, err := database.SaveView(name, "is:pinned"); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	views, err := database.ListSavedViews()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	last := views[len(views)-2:]
	if last[0].Name != "Zulu" || last[1].Name != "Alpha" {
		t.Errorf("new views should append in creation order, got %q then %q", last[0].Name, last[1].Name)
	}
}

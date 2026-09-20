package db

import (
	"sync"
	"testing"
)

// TestSaveViewConcurrentDifferentNamesSortOrderRace confirms that concurrent
// SaveView calls with distinct names each land a unique sort_order. The
// SELECT COALESCE(MAX(sort_order),0)+1 then INSERT pattern is non-atomic: two
// goroutines can both read the same MAX and both insert with the same next
// value. sort_order has no UNIQUE constraint, so SQLite silently accepts the
// duplicate and ListSavedViews' ORDER BY sort_order, name then renders the
// collided subset alphabetically rather than in creation order. This test
// would have caught the bug; it fails on the un-atomic implementation.
func TestSaveViewConcurrentDifferentNamesSortOrderRace(t *testing.T) {
	database := openViewTestDB(t)
	const goroutines = 30
	names := make([]string, goroutines)
	for i := range names {
		names[i] = "View" + string(rune('A'+i))
	}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for _, n := range names {
		go func(name string) {
			defer wg.Done()
			if _, err := database.SaveView(name, "is:pinned"); err != nil {
				t.Errorf("save %s: %v", name, err)
			}
		}(n)
	}
	wg.Wait()

	views, err := database.ListSavedViews()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[int]int{}
	created := map[string]bool{}
	for i := range names {
		created[names[i]] = true
	}
	for _, v := range views {
		seen[v.SortOrder]++
	}
	dups := 0
	for _, c := range seen {
		if c > 1 {
			dups += c - 1
		}
	}
	if dups > 0 {
		t.Errorf("concurrent saves produced %d duplicate sort_order values", dups)
	}
	present := 0
	for _, v := range views {
		if created[v.Name] {
			present++
		}
	}
	if present != goroutines {
		t.Errorf("expected all %d created views present, got %d", goroutines, present)
	}
}

// TestSaveViewConcurrentSameNameUpsertsRace confirms the second non-atomic
// surface the read-modify-write opened: two goroutines saving the same name
// could both observe existing == nil and both attempt the INSERT, with the
// loser receiving a "UNIQUE constraint failed: saved_views.name" error. The
// single upsert converts that into a clean insert-or-update: no caller errors
// and exactly one row for the name remains, holding one of the two queries
// (last writer wins, which is the documented SaveView semantic for an
// existing name).
func TestSaveViewConcurrentSameNameUpsertsRace(t *testing.T) {
	database := openViewTestDB(t)
	const goroutines = 30
	const name = "Same"
	queries := make([]string, goroutines)
	for i := range queries {
		queries[i] = "is:pinned prio:" + string(rune('A'+i%26))
	}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for _, q := range queries {
		go func(query string) {
			defer wg.Done()
			if _, err := database.SaveView(name, query); err != nil {
				t.Errorf("save %q: %v", query, err)
			}
		}(q)
	}
	wg.Wait()

	got, err := database.GetSavedView(name)
	if err != nil {
		t.Fatalf("lookup %q: %v", name, err)
	}
	if got == nil {
		t.Fatalf("view %q is missing", name)
	}
	if got.Name != name {
		t.Errorf("stored name = %q, want %q", got.Name, name)
	}
	matched := false
	for _, q := range queries {
		if got.Query == q {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("stored query %q is not one of the %d concurrent writers", got.Query, goroutines)
	}
}

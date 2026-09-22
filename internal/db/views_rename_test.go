package db

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRenameViewRefusesOntoExisting mirrors the handler contract: renaming
// onto an existing view must return ErrViewAlreadyExists and leave BOTH rows
// intact with their original queries.
func TestRenameViewRefusesOntoExisting(t *testing.T) {
	database := openViewTestDB(t)
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := database.SaveView(name, "is:pinned"); err != nil {
			t.Fatal(err)
		}
	}

	_, err := database.RenameView("Alpha", "Beta", "status:done")
	if !errors.Is(err, ErrViewAlreadyExists) {
		t.Fatalf("expected ErrViewAlreadyExists, got %v", err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		v, _ := database.GetSavedView(name)
		if v == nil {
			t.Errorf("%s should still exist", name)
		} else if v.Query != "is:pinned" {
			t.Errorf("%s query changed to %q", name, v.Query)
		}
	}
}

// TestRenameViewSourceGone returns ErrViewNotFound when the source was deleted
// between the handler's lookup and the transactional write — refusing to
// resurrect a deleted view under a new name.
func TestRenameViewSourceGone(t *testing.T) {
	database := openViewTestDB(t)
	if _, err := database.SaveView("Doomed", "is:pinned"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteSavedView("Doomed"); err != nil {
		t.Fatal(err)
	}
	_, err := database.RenameView("Doomed", "Phantom", "status:done")
	if !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("expected ErrViewNotFound, got %v", err)
	}
	if v, _ := database.GetSavedView("Phantom"); v != nil {
		t.Error("a deleted source must not be resurrected under the new name")
	}
}

// TestRenameViewConcurrentNoDataLoss is the db-level form of the race in the
// bug report: many goroutines rename distinct sources onto the same fresh
// target name. Exactly one must win, the rest must get ErrViewAlreadyExists,
// every source view must survive, and no row count other than (sources-1
// survivors + 1 target) may result.
//
// Iterations share one database (opened once, then cleared between iterations)
// so the ~0.7s migration + git-init cost of Open is paid once, not 200x. The
// views are reset before each iteration so every race starts from the same
// blank state.
func TestRenameViewConcurrentNoDataLoss(t *testing.T) {
	const target = "CollidingName"
	const racers = 8
	const iters = 200

	database := openViewTestDB(t)
	reset := func(t *testing.T) {
		t.Helper()
		if _, err := database.Exec(`DELETE FROM saved_views`); err != nil {
			t.Fatalf("reset saved_views: %v", err)
		}
	}

	for iter := 0; iter < iters; iter++ {
		reset(t)

		sources := make([]string, racers)
		for i := 0; i < racers; i++ {
			name := "Source" + string(rune('A'+i))
			if _, err := database.SaveView(name, "is:pinned"); err != nil {
				t.Fatal(err)
			}
			sources[i] = name
		}

		type result struct {
			err error
			src string
		}
		resCh := make(chan result, racers)
		var wg sync.WaitGroup
		var release sync.WaitGroup
		release.Add(1)
		var ready sync.WaitGroup
		ready.Add(racers)

		for _, src := range sources {
			wg.Add(1)
			go func(src string) {
				defer wg.Done()
				ready.Done()
				release.Wait()
				_, err := database.RenameView(src, target, "from:"+src)
				resCh <- result{err: err, src: src}
			}(src)
		}
		ready.Wait()
		release.Done()
		wg.Wait()
		close(resCh)

		var ok, conflict, other int
		otherErrs := map[string]string{}
		for r := range resCh {
			switch {
			case r.err == nil:
				ok++
			case errors.Is(r.err, ErrViewAlreadyExists):
				conflict++
			default:
				other++
				otherErrs[r.src] = fmt.Sprintf("%v", r.err)
			}
		}

		targetView, _ := database.GetSavedView(target)
		var lost []string
		for _, src := range sources {
			if v, _ := database.GetSavedView(src); v == nil {
				if targetView != nil && targetView.Query != "from:"+src {
					lost = append(lost, src)
				}
			}
		}

		if ok != 1 || conflict != racers-1 || other != 0 || len(lost) != 0 {
			t.Fatalf("iter %d: ok=%d conflict=%d other=%d otherErrs=%v lost=%v target=%+v",
				iter, ok, conflict, other, otherErrs, lost, targetView)
		}
	}
}

// TestSaveViewConcurrentUpsertNoError fires many concurrent saves of the same
// fresh name. The old form (GetSavedView then INSERT) let both see "no row"
// and the second INSERT blew up on the UNIQUE COLLATE NOCASE constraint as a
// 500. The atomic ON CONFLICT upsert must let every call succeed (no errors)
// and leave exactly one row.
//
// Iterations share one database (see TestRenameViewConcurrentNoDataLoss for
// why).
func TestSaveViewConcurrentUpsertNoError(t *testing.T) {
	const name = "Fresh"
	const racers = 16
	const iters = 200

	database := openViewTestDB(t)
	for iter := 0; iter < iters; iter++ {
		if _, err := database.Exec(`DELETE FROM saved_views`); err != nil {
			t.Fatalf("reset saved_views: %v", err)
		}

		var wg sync.WaitGroup
		errCh := make(chan error, racers)
		var release sync.WaitGroup
		release.Add(1)
		var ready sync.WaitGroup
		ready.Add(racers)

		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ready.Done()
				release.Wait()
				_, err := database.SaveView(name, fmt.Sprintf("from:%d", i))
				errCh <- err
			}(i)
		}
		ready.Wait()
		release.Done()
		wg.Wait()
		close(errCh)

		for err := range errCh {
			if err != nil {
				t.Fatalf("iter %d: concurrent SaveView errored: %v", iter, err)
			}
		}
		views, err := database.ListSavedViews()
		if err != nil {
			t.Fatal(err)
		}
		var fresh []*SavedView
		for _, v := range views {
			if v.Name == name {
				fresh = append(fresh, v)
			}
		}
		if len(fresh) != 1 {
			t.Fatalf("iter %d: expected exactly one %q row, got %d", iter, name, len(fresh))
		}
	}
}

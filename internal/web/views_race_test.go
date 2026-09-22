package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestHandleUpdateViewRenameRaceNoDataLoss is the inverse of the bug-report
// reproduction: it asserts, under 8 concurrent PATCHes that each rename a
// distinct source view onto the same fresh target name, that the rename guard
// holds under concurrency. Before the fix, the GetSavedView check sat outside
// any lock or transaction, so multiple requests observed "no existing view",
// all returned 200, the target row was overwritten by whichever SaveView
// landed last, and each 200'd request then unconditionally deleted its source
// (silent, irreversible data loss). The fix routes renames through
// db.RenameView, which wraps the conflict check and the writes in a single
// transaction, so exactly one request wins (200), the rest get 409, every
// source survives, and no request sees a 500.
//
// Iterations share one server/database (opened once, then cleared between
// iterations) so the ~0.7s migration + git-init cost of Open is paid once,
// not 200x — and real cross-iteration contention ensues on a shared DB.
func TestHandleUpdateViewRenameRaceNoDataLoss(t *testing.T) {
	const target = "CollidingName"
	const racers = 8
	const iters = 200

	srv, database, _ := setupServer(t)
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
			code int
			src  string
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
				body := strings.NewReader(`{"name":"` + target + `","query":"from:` + src + `"}`)
				req := httptest.NewRequest("PATCH", "/api/views/"+src, body)
				req.SetPathValue("name", src)
				w := httptest.NewRecorder()
				srv.handleUpdateView(w, req)
				resCh <- result{code: w.Code, src: src}
			}(src)
		}

		ready.Wait()
		release.Done()
		wg.Wait()
		close(resCh)

		var okCount, conflictCount, errCount int
		results := make(map[string]int)
		for r := range resCh {
			results[r.src] = r.code
			switch r.code {
			case http.StatusOK:
				okCount++
			case http.StatusConflict:
				conflictCount++
			default:
				errCount++
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

		if okCount != 1 || conflictCount != racers-1 || errCount != 0 || len(lost) != 0 {
			t.Fatalf("iter %d: rename race not closed: ok=%d conflict=%d other=%d lost=%v target=%+v per-src=%v",
				iter, okCount, conflictCount, errCount, lost, targetView, results)
		}
	}
}

// TestHandleUpdateViewRenameOntoExistingConcurrentIs409 is the concurrent form
// of the sequential TestHandleUpdateViewRefusesToRenameOntoAnother: firing
// concurrent renames onto a name that already existed before any request in the
// batch started must return 409 for every racer, and the pre-existing target
// plus every source must survive with its original query. The bug report
// called out that TestHandleUpdateViewRefusesToRenameOntoAnother's contract
// held only for sequential requests; this test pins it under concurrency.
func TestHandleUpdateViewRenameOntoExistingConcurrentIs409(t *testing.T) {
	const target = "Taken"
	const racers = 4
	const iters = 100

	srv, database, _ := setupServer(t)
	for iter := 0; iter < iters; iter++ {
		if _, err := database.Exec(`DELETE FROM saved_views`); err != nil {
			t.Fatalf("reset saved_views: %v", err)
		}
		if _, err := database.SaveView(target, "status:blocked"); err != nil {
			t.Fatal(err)
		}
		sources := make([]string, racers)
		for i := 0; i < racers; i++ {
			name := "Source" + string(rune('A'+i))
			if _, err := database.SaveView(name, "is:pinned"); err != nil {
				t.Fatal(err)
			}
			sources[i] = name
		}

		resCh := make(chan int, racers)
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
				body := strings.NewReader(`{"name":"` + target + `","query":"from:` + src + `"}`)
				req := httptest.NewRequest("PATCH", "/api/views/"+src, body)
				req.SetPathValue("name", src)
				w := httptest.NewRecorder()
				srv.handleUpdateView(w, req)
				resCh <- w.Code
			}(src)
		}
		ready.Wait()
		release.Done()
		wg.Wait()
		close(resCh)

		for code := range resCh {
			if code != http.StatusConflict {
				t.Fatalf("iter %d: rename onto existing view returned %d, want 409", iter, code)
			}
		}
		// The pre-existing target kept its query, and every source survived.
		if v, _ := database.GetSavedView(target); v == nil || v.Query != "status:blocked" {
			t.Fatalf("iter %d: target row changed: %+v", iter, v)
		}
		for _, src := range sources {
			if v, _ := database.GetSavedView(src); v == nil || v.Query != "is:pinned" {
				t.Fatalf("iter %d: source %s changed: %+v", iter, src, v)
			}
		}
	}
}

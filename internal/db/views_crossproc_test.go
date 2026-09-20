package db

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// TestSaveViewCrossProcessSortOrderRace is the cross-process analogue of
// TestSaveViewConcurrentDifferentNamesSortOrderRace. The bug report notes the
// race also spans processes: the CLI, TUI and daemon all open the same WAL
// file, and a deferred BEGIN does not acquire the write lock before the
// INSERT, so two processes can both read the same MAX(sort_order) and both
// insert a duplicate. The single-statement upsert holds the SQLite write
// lock for the whole statement (subquery included), which is process-
// independent. This test launches two real child processes (the test binary
// re-invoked via -test.run) sharing one WAL file and asserts the resulting
// rows have unique sort_order values.
//
// This also guards the specific footgun the bug report calls out: a plain
// db.Begin()/tx.Commit() wrapper would close the in-process race (because
// SetMaxOpenConns(1) serializes the connection) but NOT the cross-process
// one, since modernc.org/sqlite issues a deferred BEGIN that does not take
// the write lock until the first write. Only the single-statement upsert is
// atomic across processes, and only this test catches a regression to the
// tx-wrapper form.
func TestSaveViewCrossProcessSortOrderRace(t *testing.T) {
	if os.Getenv("TASKYOU_CROSSPROC_WORKER") == "1" {
		runCrossProcWorker(t)
		return
	}

	dbPath := filepath.Join(t.TempDir(), "shared.db")

	const procCount = 2
	const perProc = 20
	var wg sync.WaitGroup
	wg.Add(procCount)
	errs := make(chan error, procCount)
	for p := 0; p < procCount; p++ {
		go func(prefix string) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestSaveViewCrossProcessSortOrderRace")
			cmd.Env = append(os.Environ(),
				"TASKYOU_CROSSPROC_WORKER=1",
				"TASKYOU_CROSSPROC_DBPATH="+dbPath,
				"TASKYOU_CROSSPROC_PREFIX="+prefix,
				"TASKYOU_CROSSPROC_COUNT="+strconv.Itoa(perProc),
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				errs <- err
				return
			}
			errs <- nil
		}("P" + strconv.Itoa(p))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("worker failed: %v", err)
		}
	}

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open for verify: %v", err)
	}
	defer database.Close()

	views, err := database.ListSavedViews()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[int]int{}
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
		t.Errorf("cross-process saves produced %d duplicate sort_order values", dups)
	}
}

func runCrossProcWorker(t *testing.T) {
	dbPath := os.Getenv("TASKYOU_CROSSPROC_DBPATH")
	prefix := os.Getenv("TASKYOU_CROSSPROC_PREFIX")
	if dbPath == "" || prefix == "" {
		t.Fatal("worker env not set")
	}
	n, err := strconv.Atoi(os.Getenv("TASKYOU_CROSSPROC_COUNT"))
	if err != nil || n <= 0 {
		n = 20
	}

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("worker open: %v", err)
	}
	defer database.Close()

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			name := prefix + "-" + strconv.Itoa(i)
			if _, err := database.SaveView(name, "is:pinned"); err != nil {
				t.Errorf("save %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()
}

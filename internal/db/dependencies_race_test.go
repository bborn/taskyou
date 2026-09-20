package db

import (
	"sync"
	"testing"
)

// TestAddDependencyRejectsTwoCycle is the deterministic contract test for the
// exact edge case the bug report describes: a 2-cycle (A->B and B->A). The
// schema (UNIQUE(blocker_id, blocked_id), CHECK(blocker_id != blocked_id))
// permits a 2-cycle, so AddDependency's cycle check is the sole guard. Adding
// A->B must succeed; the inverse B->A must be rejected with the cycle error;
// only the first edge may be persisted.
func TestAddDependencyRejectsTwoCycle(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	a := &Task{Title: "A", Status: StatusBacklog}
	b := &Task{Title: "B", Status: StatusBacklog}
	mustCreate(t, db, a, b)

	if err := db.AddDependency(a.ID, b.ID, false); err != nil {
		t.Fatalf("add A->B: %v", err)
	}

	if err := db.AddDependency(b.ID, a.ID, false); err == nil {
		t.Fatal("add B->A: expected cycle error, got nil")
	}

	if dep, _ := db.GetDependency(a.ID, b.ID); dep == nil {
		t.Error("A->B edge should be persisted")
	}
	if dep, _ := db.GetDependency(b.ID, a.ID); dep != nil {
		t.Error("B->A edge must NOT be persisted (would form a 2-cycle)")
	}
}

// TestAddDependencyConcurrentInverseNeverCycle drives the race the bug report
// reproduces: two goroutines inserting inverse edges concurrently. Under the
// buggy (non-atomic) implementation both could pass the cycle check and both
// insert, persisting a 2-cycle (the report observed this in 15/20 HTTP
// iterations). With the BEGIN IMMEDIATE transaction the two callers serialize:
// the first to take the write lock inserts and commits; the second's cycle
// check then sees the inserted edge and rejects. So for every iteration the
// result must be exactly one persisted edge and at least one success — never
// both edges, never both failures.
//
// This is a deterministic passing test under the fix; it fails the buggy
// implementation with high probability over enough iterations (a 2-cycle is
// both edges persisted, which the loop asserts never happens).
func TestAddDependencyConcurrentInverseNeverCycle(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	const iterations = 100
	var bothInserted, atLeastOneSuccess int
	for i := 0; i < iterations; i++ {
		a := &Task{Title: "A", Status: StatusBacklog}
		b := &Task{Title: "B", Status: StatusBacklog}
		mustCreate(t, db, a, b)

		var wg sync.WaitGroup
		var errAB, errBA error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			errAB = db.AddDependency(a.ID, b.ID, false)
		}()
		go func() {
			defer wg.Done()
			<-start
			errBA = db.AddDependency(b.ID, a.ID, false)
		}()
		close(start)
		wg.Wait()

		depAB, _ := db.GetDependency(a.ID, b.ID)
		depBA, _ := db.GetDependency(b.ID, a.ID)
		inserted := 0
		if depAB != nil {
			inserted++
		}
		if depBA != nil {
			inserted++
		}
		if inserted == 2 {
			bothInserted++
		}
		if errAB == nil || errBA == nil {
			atLeastOneSuccess++
		}

		if inserted == 2 {
			t.Errorf("iter %d: 2-cycle persisted (errAB=%v errBA=%v)", i, errAB, errBA)
		}
		if errAB == nil && errBA == nil {
			t.Errorf("iter %d: both calls succeeded — cycle check not serializing", i)
		}
		// The empty graph always admits one of the two edges, so at least one
		// call must succeed; both failing would mean valid input was rejected.
		if errAB != nil && errBA != nil {
			t.Errorf("iter %d: both calls failed (errAB=%v errBA=%v)", i, errAB, errBA)
		}
	}
	t.Logf("bothInserted=%d atLeastOneSuccess=%d over %d iterations",
		bothInserted, atLeastOneSuccess, iterations)
	if bothInserted > 0 {
		t.Errorf("%d/%d iterations persisted a 2-cycle", bothInserted, iterations)
	}
}

// TestAddDependencyConcurrentDisjointBothSucceed is the non-regression guard:
// concurrent dependencies on disjoint task pairs are legitimate and must both
// succeed despite the serialization the fix adds. Confirms that locking the
// write path for cycle-safety does not break the common case.
func TestAddDependencyConcurrentDisjointBothSucceed(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	a := &Task{Title: "A", Status: StatusBacklog}
	b := &Task{Title: "B", Status: StatusBacklog}
	c := &Task{Title: "C", Status: StatusBacklog}
	d := &Task{Title: "D", Status: StatusBacklog}
	mustCreate(t, db, a, b, c, d)

	var wg sync.WaitGroup
	var errAB, errCD error
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errAB = db.AddDependency(a.ID, b.ID, false)
	}()
	go func() {
		defer wg.Done()
		<-start
		errCD = db.AddDependency(c.ID, d.ID, false)
	}()
	close(start)
	wg.Wait()

	if errAB != nil {
		t.Errorf("add A->B: %v", errAB)
	}
	if errCD != nil {
		t.Errorf("add C->D: %v", errCD)
	}
	if dep, _ := db.GetDependency(a.ID, b.ID); dep == nil {
		t.Error("A->B edge should be persisted")
	}
	if dep, _ := db.GetDependency(c.ID, d.ID); dep == nil {
		t.Error("C->D edge should be persisted")
	}
}

// TestAddDependencyAutoQueueSurvivesTransaction confirms the auto_queue flag
// is still persisted when AddDependency now writes through a transaction,
// since the cascade (ProcessCompletedBlocker / RequeueReadyTasks) keys off it.
func TestAddDependencyAutoQueueSurvivesTransaction(t *testing.T) {
	db, cleanup := setupDepsTestDB(t)
	defer cleanup()

	blocker := &Task{Title: "blocker", Status: StatusBacklog}
	dependent := &Task{Title: "dependent", Status: StatusBacklog}
	mustCreate(t, db, blocker, dependent)

	if err := db.AddDependency(blocker.ID, dependent.ID, true); err != nil {
		t.Fatalf("add with auto_queue: %v", err)
	}
	dep, err := db.GetDependency(blocker.ID, dependent.ID)
	if err != nil {
		t.Fatalf("get dependency: %v", err)
	}
	if dep == nil {
		t.Fatal("dependency not persisted")
	}
	if !dep.AutoQueue {
		t.Errorf("auto_queue flag lost in the transaction: got %v, want true", dep.AutoQueue)
	}

	// The auto_queue flag must drive the release cascade: completing the
	// blocker queues the dependent (not just drops it to backlog).
	if err := db.SetTaskStatus(dependent.ID, StatusBlocked, ActorCLI,
		"staged behind its blocker", ByHuman("test fixture")); err != nil {
		t.Fatalf("stage dependent: %v", err)
	}
	if err := db.SetTaskStatus(blocker.ID, StatusDone, ActorCLI,
		"blocker complete", ByHuman("test fixture")); err != nil {
		t.Fatalf("complete blocker: %v", err)
	}
	got, err := db.GetTask(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent: %v", err)
	}
	if got.Status != StatusQueued {
		t.Errorf("auto_queue cascade after completion: dependent status = %s, want %s",
			got.Status, StatusQueued)
	}
}

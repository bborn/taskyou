package db

import (
	"os"
	"testing"
)

func setupTeamsTestDB(t *testing.T) (*DB, func()) {
	tmpFile, err := os.CreateTemp("", "test-teams-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

func TestGetChildTasks(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	// Create parent task
	parent := &Task{Title: "Parent Task", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create child tasks
	child1 := &Task{Title: "Child 1", Status: StatusQueued, ParentID: parent.ID}
	child2 := &Task{Title: "Child 2", Status: StatusQueued, ParentID: parent.ID}
	child3 := &Task{Title: "Child 3", Status: StatusQueued, ParentID: parent.ID}
	if err := db.CreateTask(child1); err != nil {
		t.Fatalf("Failed to create child1: %v", err)
	}
	if err := db.CreateTask(child2); err != nil {
		t.Fatalf("Failed to create child2: %v", err)
	}
	if err := db.CreateTask(child3); err != nil {
		t.Fatalf("Failed to create child3: %v", err)
	}

	// Create unrelated task (should not appear)
	unrelated := &Task{Title: "Unrelated", Status: StatusBacklog}
	if err := db.CreateTask(unrelated); err != nil {
		t.Fatalf("Failed to create unrelated: %v", err)
	}

	// Get children
	children, err := db.GetChildTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get child tasks: %v", err)
	}

	if len(children) != 3 {
		t.Errorf("Expected 3 children, got %d", len(children))
	}

	// Verify they're in creation order
	if children[0].Title != "Child 1" {
		t.Errorf("Expected first child to be 'Child 1', got %q", children[0].Title)
	}
	if children[2].Title != "Child 3" {
		t.Errorf("Expected last child to be 'Child 3', got %q", children[2].Title)
	}

	// Verify parent has no children fetched via wrong ID
	noChildren, err := db.GetChildTasks(unrelated.ID)
	if err != nil {
		t.Fatalf("Failed to get child tasks for unrelated: %v", err)
	}
	if len(noChildren) != 0 {
		t.Errorf("Expected 0 children for unrelated task, got %d", len(noChildren))
	}
}

func TestGetTeamStatus(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	// Create parent task
	parent := &Task{Title: "Parent Task", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create children with different statuses
	tasks := []*Task{
		{Title: "Queued 1", Status: StatusQueued, ParentID: parent.ID},
		{Title: "Queued 2", Status: StatusQueued, ParentID: parent.ID},
		{Title: "Processing", Status: StatusProcessing, ParentID: parent.ID},
		{Title: "Done 1", Status: StatusDone, ParentID: parent.ID},
		{Title: "Done 2", Status: StatusDone, ParentID: parent.ID},
	}
	for _, task := range tasks {
		if err := db.CreateTask(task); err != nil {
			t.Fatalf("Failed to create task %q: %v", task.Title, err)
		}
	}

	status, err := db.GetTeamStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get team status: %v", err)
	}

	if status.Total != 5 {
		t.Errorf("Expected total=5, got %d", status.Total)
	}
	if status.Queued != 2 {
		t.Errorf("Expected queued=2, got %d", status.Queued)
	}
	if status.Processing != 1 {
		t.Errorf("Expected processing=1, got %d", status.Processing)
	}
	if status.Done != 2 {
		t.Errorf("Expected done=2, got %d", status.Done)
	}
	if status.IsComplete() {
		t.Error("Expected IsComplete to be false")
	}
}

func TestTeamStatusComplete(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Parent", Status: StatusProcessing}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// All children done
	for i := 0; i < 3; i++ {
		child := &Task{Title: "Done child", Status: StatusDone, ParentID: parent.ID}
		if err := db.CreateTask(child); err != nil {
			t.Fatalf("Failed to create child: %v", err)
		}
	}

	status, err := db.GetTeamStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get team status: %v", err)
	}

	if !status.IsComplete() {
		t.Error("Expected IsComplete to be true when all children are done")
	}
	if status.ActiveCount() != 0 {
		t.Errorf("Expected ActiveCount=0, got %d", status.ActiveCount())
	}
}

func TestTeamStatusEmpty(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	parent := &Task{Title: "No Team", Status: StatusBacklog}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	status, err := db.GetTeamStatus(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get team status: %v", err)
	}

	if status.Total != 0 {
		t.Errorf("Expected total=0, got %d", status.Total)
	}
	if status.IsComplete() {
		t.Error("Expected IsComplete to be false for empty team")
	}
}

func TestHasChildTasks(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	parent := &Task{Title: "Parent", Status: StatusBacklog}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// No children yet
	has, err := db.HasChildTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check child tasks: %v", err)
	}
	if has {
		t.Error("Expected no children initially")
	}

	// Add a child
	child := &Task{Title: "Child", Status: StatusQueued, ParentID: parent.ID}
	if err := db.CreateTask(child); err != nil {
		t.Fatalf("Failed to create child: %v", err)
	}

	has, err = db.HasChildTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check child tasks: %v", err)
	}
	if !has {
		t.Error("Expected child tasks to exist")
	}
}

func TestGetTeamStatusMap(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	// Create two parent tasks
	parent1 := &Task{Title: "Parent 1", Status: StatusProcessing}
	parent2 := &Task{Title: "Parent 2", Status: StatusProcessing}
	if err := db.CreateTask(parent1); err != nil {
		t.Fatalf("Failed to create parent1: %v", err)
	}
	if err := db.CreateTask(parent2); err != nil {
		t.Fatalf("Failed to create parent2: %v", err)
	}

	// Children for parent1
	db.CreateTask(&Task{Title: "P1 Child 1", Status: StatusDone, ParentID: parent1.ID})
	db.CreateTask(&Task{Title: "P1 Child 2", Status: StatusQueued, ParentID: parent1.ID})

	// Children for parent2
	db.CreateTask(&Task{Title: "P2 Child 1", Status: StatusDone, ParentID: parent2.ID})
	db.CreateTask(&Task{Title: "P2 Child 2", Status: StatusDone, ParentID: parent2.ID})
	db.CreateTask(&Task{Title: "P2 Child 3", Status: StatusDone, ParentID: parent2.ID})

	statusMap, err := db.GetTeamStatusMap()
	if err != nil {
		t.Fatalf("Failed to get team status map: %v", err)
	}

	// Check parent1
	s1, ok := statusMap[parent1.ID]
	if !ok {
		t.Fatal("Expected parent1 in status map")
	}
	if s1.Total != 2 {
		t.Errorf("Parent1: expected total=2, got %d", s1.Total)
	}
	if s1.Done != 1 {
		t.Errorf("Parent1: expected done=1, got %d", s1.Done)
	}
	if s1.IsComplete() {
		t.Error("Parent1: expected IsComplete=false")
	}

	// Check parent2
	s2, ok := statusMap[parent2.ID]
	if !ok {
		t.Fatal("Expected parent2 in status map")
	}
	if s2.Total != 3 {
		t.Errorf("Parent2: expected total=3, got %d", s2.Total)
	}
	if !s2.IsComplete() {
		t.Error("Parent2: expected IsComplete=true")
	}
}

func TestParentIDInTaskCRUD(t *testing.T) {
	db, cleanup := setupTeamsTestDB(t)
	defer cleanup()

	// Create parent
	parent := &Task{Title: "Parent", Status: StatusBacklog}
	if err := db.CreateTask(parent); err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	// Create child with parent_id
	child := &Task{Title: "Child", Status: StatusQueued, ParentID: parent.ID}
	if err := db.CreateTask(child); err != nil {
		t.Fatalf("Failed to create child: %v", err)
	}

	// Fetch child and verify parent_id
	fetched, err := db.GetTask(child.ID)
	if err != nil {
		t.Fatalf("Failed to get child: %v", err)
	}
	if fetched.ParentID != parent.ID {
		t.Errorf("Expected ParentID=%d, got %d", parent.ID, fetched.ParentID)
	}

	// Fetch parent and verify no parent_id
	fetchedParent, err := db.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get parent: %v", err)
	}
	if fetchedParent.ParentID != 0 {
		t.Errorf("Expected parent ParentID=0, got %d", fetchedParent.ParentID)
	}
}

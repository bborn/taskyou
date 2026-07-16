package db

import (
	"path/filepath"
	"testing"
)

func openArtifactTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestPipelineArtifactRoundTrip(t *testing.T) {
	database := openArtifactTestDB(t)

	if err := database.SetPipelineArtifact("pipeline/1-foo", "research", "hello world"); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := database.GetPipelineArtifact("pipeline/1-foo", "research")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "hello world" {
		t.Errorf("get = %q, want %q", got, "hello world")
	}

	// A missing artifact returns empty, no error.
	missing, err := database.GetPipelineArtifact("pipeline/1-foo", "nope")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != "" {
		t.Errorf("missing artifact = %q, want empty", missing)
	}
}

func TestPipelineArtifactUpsertOverwrites(t *testing.T) {
	database := openArtifactTestDB(t)

	if err := database.SetPipelineArtifact("br", "plan", "v1"); err != nil {
		t.Fatalf("set v1: %v", err)
	}
	if err := database.SetPipelineArtifact("br", "plan", "v2"); err != nil {
		t.Fatalf("set v2: %v", err)
	}

	got, err := database.GetPipelineArtifact("br", "plan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "v2" {
		t.Errorf("get = %q, want %q (overwrite)", got, "v2")
	}

	// Overwrite must not create a duplicate row.
	list, err := database.ListPipelineArtifacts("br")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1 after overwrite", len(list))
	}
}

func TestPipelineArtifactBranchScoping(t *testing.T) {
	database := openArtifactTestDB(t)

	if err := database.SetPipelineArtifact("branch-a", "research", "a-content"); err != nil {
		t.Fatalf("set a: %v", err)
	}
	if err := database.SetPipelineArtifact("branch-b", "research", "b-content"); err != nil {
		t.Fatalf("set b: %v", err)
	}

	a, err := database.GetPipelineArtifact("branch-a", "research")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	b, err := database.GetPipelineArtifact("branch-b", "research")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	if a != "a-content" || b != "b-content" {
		t.Errorf("branch scoping failed: a=%q b=%q", a, b)
	}

	listA, err := database.ListPipelineArtifacts("branch-a")
	if err != nil {
		t.Fatalf("list a: %v", err)
	}
	if len(listA) != 1 || listA[0].Name != "research" || listA[0].Content != "a-content" {
		t.Errorf("list branch-a = %+v, want single research/a-content", listA)
	}
}

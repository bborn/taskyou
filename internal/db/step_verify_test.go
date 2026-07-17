package db

import (
	"path/filepath"
	"testing"
)

func openStepVerifyTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestStepVerifyRoundTrip(t *testing.T) {
	database := openStepVerifyTestDB(t)

	if err := database.SetStepVerify(42, "go build ./... && go test ./..."); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := database.GetStepVerify(42)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "go build ./... && go test ./..." {
		t.Errorf("get = %q, want the command", got)
	}
}

func TestStepVerifyMissingIsEmpty(t *testing.T) {
	database := openStepVerifyTestDB(t)

	got, err := database.GetStepVerify(999)
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if got != "" {
		t.Errorf("missing = %q, want empty (no gate)", got)
	}
}

func TestStepVerifyUpsertOverwrites(t *testing.T) {
	database := openStepVerifyTestDB(t)

	if err := database.SetStepVerify(7, "v1"); err != nil {
		t.Fatalf("set v1: %v", err)
	}
	if err := database.SetStepVerify(7, "v2"); err != nil {
		t.Fatalf("set v2: %v", err)
	}
	got, err := database.GetStepVerify(7)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "v2" {
		t.Errorf("get = %q, want v2 (overwrite)", got)
	}
}

package ui

import (
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestInterruptKeyEnabled(t *testing.T) {
	// Create a test database
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer database.Close()

	// Create app model with nil executor (we'll test the keys directly)
	keys := DefaultKeyMap()

	// By default, the interrupt key should be enabled
	if !keys.Interrupt.Enabled() {
		t.Error("interrupt key should be enabled by default")
	}

	// After disabling, it should be disabled
	keys.Interrupt.SetEnabled(false)
	if keys.Interrupt.Enabled() {
		t.Error("interrupt key should be disabled after SetEnabled(false)")
	}

	// Re-enable
	keys.Interrupt.SetEnabled(true)
	if !keys.Interrupt.Enabled() {
		t.Error("interrupt key should be enabled after SetEnabled(true)")
	}
}


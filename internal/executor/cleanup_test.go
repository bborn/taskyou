package executor

import "testing"

func TestDefaultWorktreeCleanupMaxAgeIsShort(t *testing.T) {
	// Regression test: the default must be short enough that heavy task batches
	// can't accumulate stale worktrees for a week. 7 days was the prior value
	// and caused a disk-fill incident.
	hours := int(DefaultWorktreeCleanupMaxAge.Hours())
	if hours > 48 {
		t.Errorf("DefaultWorktreeCleanupMaxAge is %d hours - must be <= 48h to keep cleanup prompt", hours)
	}
}

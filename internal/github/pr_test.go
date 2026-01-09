package github

import (
	"testing"
)

func TestPRStateConstants(t *testing.T) {
	// Verify state constants are defined
	states := []PRState{PRStateOpen, PRStateClosed, PRStateMerged, PRStateDraft}
	for _, s := range states {
		if s == "" {
			t.Errorf("PR state constant should not be empty")
		}
	}
}

func TestCheckStateConstants(t *testing.T) {
	// Verify check state constants
	states := []CheckState{CheckStatePending, CheckStatePassing, CheckStateFailing, CheckStateNone}
	// CheckStateNone is intentionally empty
	if CheckStatePending == "" {
		t.Errorf("CheckStatePending should not be empty")
	}
	if CheckStatePassing == "" {
		t.Errorf("CheckStatePassing should not be empty")
	}
	if CheckStateFailing == "" {
		t.Errorf("CheckStateFailing should not be empty")
	}
	if CheckStateNone != "" {
		t.Errorf("CheckStateNone should be empty")
	}
	_ = states
}

func TestNewPRCache(t *testing.T) {
	cache := NewPRCache()
	if cache == nil {
		t.Fatal("NewPRCache returned nil")
	}
	if cache.cache == nil {
		t.Error("cache map should be initialized")
	}
}

func TestPRCacheGetMissingBranch(t *testing.T) {
	cache := NewPRCache()

	// Empty branch name should return nil
	info := cache.GetPRForBranch("/some/repo", "")
	if info != nil {
		t.Error("empty branch name should return nil")
	}
}

func TestPRInfoStatusIcon(t *testing.T) {
	tests := []struct {
		name     string
		prInfo   *PRInfo
		expected string
	}{
		{
			name:     "nil PR",
			prInfo:   nil,
			expected: "",
		},
		{
			name:     "merged PR",
			prInfo:   &PRInfo{State: PRStateMerged},
			expected: "M",
		},
		{
			name:     "closed PR",
			prInfo:   &PRInfo{State: PRStateClosed},
			expected: "X",
		},
		{
			name:     "draft PR",
			prInfo:   &PRInfo{State: PRStateDraft},
			expected: "D",
		},
		{
			name:     "open PR with passing checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing},
			expected: "P",
		},
		{
			name:     "open PR with failing checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateFailing},
			expected: "F",
		},
		{
			name:     "open PR with pending checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePending},
			expected: "R",
		},
		{
			name:     "open PR with no checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateNone},
			expected: "O",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.prInfo.StatusIcon()
			if got != tt.expected {
				t.Errorf("StatusIcon() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPRInfoStatusDescription(t *testing.T) {
	tests := []struct {
		name     string
		prInfo   *PRInfo
		expected string
	}{
		{
			name:     "nil PR",
			prInfo:   nil,
			expected: "",
		},
		{
			name:     "merged PR",
			prInfo:   &PRInfo{State: PRStateMerged},
			expected: "Merged",
		},
		{
			name:     "closed PR",
			prInfo:   &PRInfo{State: PRStateClosed},
			expected: "Closed",
		},
		{
			name:     "draft PR",
			prInfo:   &PRInfo{State: PRStateDraft},
			expected: "Draft PR",
		},
		{
			name:     "open PR ready to merge",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE"},
			expected: "Ready to merge",
		},
		{
			name:     "open PR with conflicts",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "CONFLICTING"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with failing checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateFailing},
			expected: "Checks failing",
		},
		{
			name:     "open PR with pending checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePending},
			expected: "Checks running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.prInfo.StatusDescription()
			if got != tt.expected {
				t.Errorf("StatusDescription() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseCheckState(t *testing.T) {
	tests := []struct {
		name     string
		checks   []ghCheck
		expected CheckState
	}{
		{
			name:     "no checks",
			checks:   nil,
			expected: CheckStateNone,
		},
		{
			name:     "empty checks",
			checks:   []ghCheck{},
			expected: CheckStateNone,
		},
		{
			name: "all passing",
			checks: []ghCheck{
				{Conclusion: "SUCCESS"},
				{Conclusion: "SUCCESS"},
			},
			expected: CheckStatePassing,
		},
		{
			name: "one failing",
			checks: []ghCheck{
				{Conclusion: "SUCCESS"},
				{Conclusion: "FAILURE"},
			},
			expected: CheckStateFailing,
		},
		{
			name: "one pending",
			checks: []ghCheck{
				{Conclusion: "SUCCESS"},
				{Status: "IN_PROGRESS"},
			},
			expected: CheckStatePending,
		},
		{
			name: "pending and failing - failure wins",
			checks: []ghCheck{
				{Status: "IN_PROGRESS"},
				{Conclusion: "FAILURE"},
			},
			expected: CheckStateFailing,
		},
		{
			name: "error state",
			checks: []ghCheck{
				{Conclusion: "ERROR"},
			},
			expected: CheckStateFailing,
		},
		{
			name: "timed out",
			checks: []ghCheck{
				{Conclusion: "TIMED_OUT"},
			},
			expected: CheckStateFailing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCheckState(tt.checks)
			if got != tt.expected {
				t.Errorf("parseCheckState() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPRCacheInvalidate(t *testing.T) {
	cache := NewPRCache()

	// Add an entry manually
	cache.cache["test:branch"] = &cacheEntry{
		info: &PRInfo{Number: 1},
	}

	// Verify it exists
	if len(cache.cache) != 1 {
		t.Error("cache should have 1 entry")
	}

	// Invalidate it
	cache.InvalidateCache("test", "branch")

	// Verify it's gone
	if len(cache.cache) != 0 {
		t.Error("cache should be empty after invalidation")
	}
}

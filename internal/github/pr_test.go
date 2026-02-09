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
			name:     "open PR with conflicts and passing checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "CONFLICTING"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with conflicts and failing checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateFailing, Mergeable: "CONFLICTING"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with conflicts and pending checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePending, Mergeable: "CONFLICTING"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with conflicts and no checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateNone, Mergeable: "CONFLICTING"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with failing checks (no conflicts)",
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

func TestMarshalUnmarshalPRInfo(t *testing.T) {
	// Test nil
	if got := MarshalPRInfo(nil); got != "" {
		t.Errorf("MarshalPRInfo(nil) = %q, want empty", got)
	}
	if got := UnmarshalPRInfo(""); got != nil {
		t.Errorf("UnmarshalPRInfo(\"\") = %v, want nil", got)
	}

	// Test round-trip
	original := &PRInfo{
		Number:     42,
		URL:        "https://github.com/test/repo/pull/42",
		State:      PRStateOpen,
		IsDraft:    false,
		Title:      "Fix things",
		CheckState: CheckStatePassing,
		Mergeable:  "MERGEABLE",
		Additions:  10,
		Deletions:  5,
	}

	jsonStr := MarshalPRInfo(original)
	if jsonStr == "" {
		t.Fatal("MarshalPRInfo returned empty string")
	}

	restored := UnmarshalPRInfo(jsonStr)
	if restored == nil {
		t.Fatal("UnmarshalPRInfo returned nil")
	}

	if restored.Number != original.Number {
		t.Errorf("Number = %d, want %d", restored.Number, original.Number)
	}
	if restored.URL != original.URL {
		t.Errorf("URL = %q, want %q", restored.URL, original.URL)
	}
	if restored.State != original.State {
		t.Errorf("State = %q, want %q", restored.State, original.State)
	}
	if restored.CheckState != original.CheckState {
		t.Errorf("CheckState = %q, want %q", restored.CheckState, original.CheckState)
	}
	if restored.Mergeable != original.Mergeable {
		t.Errorf("Mergeable = %q, want %q", restored.Mergeable, original.Mergeable)
	}
	if restored.Title != original.Title {
		t.Errorf("Title = %q, want %q", restored.Title, original.Title)
	}

	// Test invalid JSON
	if got := UnmarshalPRInfo("not json"); got != nil {
		t.Errorf("UnmarshalPRInfo(invalid) = %v, want nil", got)
	}

	// Test merged state round-trip
	merged := &PRInfo{
		Number: 42,
		State:  PRStateMerged,
	}
	mergedJSON := MarshalPRInfo(merged)
	restoredMerged := UnmarshalPRInfo(mergedJSON)
	if restoredMerged.State != PRStateMerged {
		t.Errorf("State = %q, want MERGED", restoredMerged.State)
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

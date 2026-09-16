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
			name:     "dirty merge state counts as conflicts",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, MergeStateStatus: "DIRTY"},
			expected: "Has conflicts",
		},
		{
			name:     "open PR with failing checks (no conflicts)",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateFailing},
			expected: "Checks failing",
		},
		{
			name:     "failing checks outrank requested changes",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStateFailing, ReviewDecision: "CHANGES_REQUESTED"},
			expected: "Checks failing",
		},
		{
			name:     "changes requested outrank running checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePending, ReviewDecision: "CHANGES_REQUESTED"},
			expected: "Changes requested",
		},
		{
			name:     "open PR with pending checks",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePending},
			expected: "Checks running",
		},
		{
			name:     "green but awaiting a required review",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE", ReviewDecision: "REVIEW_REQUIRED"},
			expected: "Awaiting review",
		},
		{
			name:     "green and mergeable but blocked by branch protection",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED"},
			expected: "Checks passing",
		},
		{
			name:     "approved, green and clean",
			prInfo:   &PRInfo{State: PRStateOpen, CheckState: CheckStatePassing, MergeStateStatus: "CLEAN", ReviewDecision: "APPROVED"},
			expected: "Ready to merge",
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
		Number:           42,
		URL:              "https://github.com/test/repo/pull/42",
		State:            PRStateOpen,
		IsDraft:          false,
		Title:            "Fix things",
		CheckState:       CheckStatePassing,
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "BLOCKED",
		ReviewDecision:   "REVIEW_REQUIRED",
		Additions:        10,
		Deletions:        5,
	}

	jsonStr := MarshalPRInfo(original)
	if jsonStr == "" {
		t.Fatal("MarshalPRInfo returned empty string")
	}

	restored := UnmarshalPRInfo(jsonStr)
	if restored == nil {
		t.Fatal("UnmarshalPRInfo returned nil")
	}
	if *restored != *original {
		t.Errorf("round trip = %+v, want %+v", restored, original)
	}

	// Test invalid JSON
	if got := UnmarshalPRInfo("not json"); got != nil {
		t.Errorf("UnmarshalPRInfo(invalid) = %v, want nil", got)
	}

	// Rows stored before the review fields existed still decode.
	legacy := UnmarshalPRInfo(`{"number":42,"state":"MERGED","checkState":"SUCCESS"}`)
	if legacy == nil || legacy.State != PRStateMerged || legacy.ReviewDecision != "" {
		t.Errorf("legacy row = %+v", legacy)
	}
}

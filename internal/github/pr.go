// Package github provides GitHub integration for querying PR status.
package github

import (
	"encoding/json"
	"time"
)

// PRState represents the state of a pull request.
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
	PRStateDraft  PRState = "DRAFT"
)

// CheckState represents the state of CI checks.
type CheckState string

const (
	CheckStatePending CheckState = "PENDING"
	CheckStatePassing CheckState = "SUCCESS"
	CheckStateFailing CheckState = "FAILURE"
	CheckStateNone    CheckState = ""
)

// PRInfo contains information about a pull request.
type PRInfo struct {
	Number     int        `json:"number"`
	URL        string     `json:"url"`
	State      PRState    `json:"state"`
	IsDraft    bool       `json:"isDraft"`
	Title      string     `json:"title"`
	CheckState CheckState `json:"checkState"`
	Mergeable  string     `json:"mergeable"` // "MERGEABLE", "CONFLICTING", "UNKNOWN"
	// MergeStateStatus is GitHub's fuller merge verdict: CLEAN, BLOCKED, BEHIND,
	// DIRTY, UNSTABLE, HAS_HOOKS, DRAFT or UNKNOWN.
	MergeStateStatus string `json:"mergeStateStatus,omitempty"`
	// ReviewDecision is APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or "".
	ReviewDecision string    `json:"reviewDecision,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Additions      int       `json:"additions"` // Lines added
	Deletions      int       `json:"deletions"` // Lines deleted
}

// StatusIcon returns a unicode icon representing the PR state.
func (p *PRInfo) StatusIcon() string {
	if p == nil {
		return ""
	}

	switch p.State {
	case PRStateMerged:
		return "M" // Merged
	case PRStateClosed:
		return "X" // Closed
	case PRStateDraft:
		return "D" // Draft
	case PRStateOpen:
		switch p.CheckState {
		case CheckStatePassing:
			return "P" // PR open, checks passing
		case CheckStateFailing:
			return "F" // PR open, checks failing
		case CheckStatePending:
			return "R" // PR open, checks running
		default:
			return "O" // PR open, no checks
		}
	}
	return ""
}

// StatusDescription returns a human-readable description. For an open PR it
// names the one thing most in the way of merging, in the order a human has to
// deal with them: conflicts, then red checks, then requested changes.
func (p *PRInfo) StatusDescription() string {
	if p == nil {
		return ""
	}

	switch p.State {
	case PRStateMerged:
		return "Merged"
	case PRStateClosed:
		return "Closed"
	case PRStateDraft:
		return "Draft PR"
	case PRStateOpen:
		switch {
		case p.Mergeable == "CONFLICTING" || p.MergeStateStatus == "DIRTY":
			return "Has conflicts"
		case p.CheckState == CheckStateFailing:
			return "Checks failing"
		case p.ReviewDecision == "CHANGES_REQUESTED":
			return "Changes requested"
		case p.CheckState == CheckStatePending:
			return "Checks running"
		case p.ReviewDecision == "REVIEW_REQUIRED":
			return "Awaiting review"
		case p.CheckState == CheckStatePassing:
			if (p.Mergeable == "MERGEABLE" || p.MergeStateStatus == "CLEAN") && p.MergeStateStatus != "BLOCKED" {
				return "Ready to merge"
			}
			return "Checks passing"
		default:
			return "Open PR"
		}
	}
	return ""
}

// MarshalPRInfo converts a PRInfo to JSON string for database storage.
func MarshalPRInfo(info *PRInfo) string {
	if info == nil {
		return ""
	}
	data, err := json.Marshal(info)
	if err != nil {
		return ""
	}
	return string(data)
}

// UnmarshalPRInfo converts a JSON string from database back to PRInfo.
func UnmarshalPRInfo(data string) *PRInfo {
	if data == "" {
		return nil
	}
	var info PRInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return nil
	}
	return &info
}

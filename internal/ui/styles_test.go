package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/github"
)

func TestPRStatusBadge(t *testing.T) {
	tests := []struct {
		name        string
		prInfo      *github.PRInfo
		expectEmpty bool
		expectIcon  string
	}{
		{
			name:        "nil PR returns empty",
			prInfo:      nil,
			expectEmpty: true,
		},
		{
			name:       "merged PR shows M",
			prInfo:     &github.PRInfo{State: github.PRStateMerged},
			expectIcon: "M",
		},
		{
			name:       "closed PR shows X",
			prInfo:     &github.PRInfo{State: github.PRStateClosed},
			expectIcon: "X",
		},
		{
			name:       "draft PR shows D",
			prInfo:     &github.PRInfo{State: github.PRStateDraft},
			expectIcon: "D",
		},
		{
			name:       "open PR ready to merge shows R",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing, Mergeable: "MERGEABLE"},
			expectIcon: "R",
		},
		{
			name:       "open PR with conflicts and passing checks shows C",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing, Mergeable: "CONFLICTING"},
			expectIcon: "C",
		},
		{
			name:       "open PR with conflicts and failing checks shows C",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateFailing, Mergeable: "CONFLICTING"},
			expectIcon: "C",
		},
		{
			name:       "open PR with conflicts and pending checks shows C",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePending, Mergeable: "CONFLICTING"},
			expectIcon: "C",
		},
		{
			name:       "open PR with conflicts and no checks shows C",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateNone, Mergeable: "CONFLICTING"},
			expectIcon: "C",
		},
		{
			name:       "open PR with failing checks (no conflicts) shows F",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateFailing},
			expectIcon: "F",
		},
		{
			name:       "open PR with pending checks shows W",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePending},
			expectIcon: "W",
		},
		{
			name:       "open PR with passing checks shows P",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing},
			expectIcon: "P",
		},
		{
			name:       "open PR with no checks shows O",
			prInfo:     &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateNone},
			expectIcon: "O",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PRStatusBadge(tt.prInfo)

			if tt.expectEmpty {
				if got != "" {
					t.Errorf("PRStatusBadge() = %q, want empty", got)
				}
				return
			}

			// The badge includes ANSI escape codes for styling, so we check if it contains the icon
			if !strings.Contains(got, tt.expectIcon) {
				t.Errorf("PRStatusBadge() = %q, want icon %q", got, tt.expectIcon)
			}
		})
	}
}

func TestPRStatusIcon(t *testing.T) {
	tests := []struct {
		name     string
		prInfo   *github.PRInfo
		expected string
	}{
		{
			name:     "nil PR returns empty",
			prInfo:   nil,
			expected: "",
		},
		{
			name:     "merged PR shows M",
			prInfo:   &github.PRInfo{State: github.PRStateMerged},
			expected: "M",
		},
		{
			name:     "closed PR shows X",
			prInfo:   &github.PRInfo{State: github.PRStateClosed},
			expected: "X",
		},
		{
			name:     "draft PR shows D",
			prInfo:   &github.PRInfo{State: github.PRStateDraft},
			expected: "D",
		},
		{
			name:     "open PR ready to merge shows R",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing, Mergeable: "MERGEABLE"},
			expected: "R",
		},
		{
			name:     "open PR with conflicts shows C",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing, Mergeable: "CONFLICTING"},
			expected: "C",
		},
		{
			name:     "open PR with failing checks shows F",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateFailing},
			expected: "F",
		},
		{
			name:     "open PR with pending checks shows W",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePending},
			expected: "W",
		},
		{
			name:     "open PR with passing checks shows P",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStatePassing},
			expected: "P",
		},
		{
			name:     "open PR with no checks shows O",
			prInfo:   &github.PRInfo{State: github.PRStateOpen, CheckState: github.CheckStateNone},
			expected: "O",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PRStatusIcon(tt.prInfo)
			if got != tt.expected {
				t.Errorf("PRStatusIcon() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPRStatusDescription(t *testing.T) {
	tests := []struct {
		name     string
		prInfo   *github.PRInfo
		expected string
	}{
		{
			name:     "nil PR returns empty",
			prInfo:   nil,
			expected: "",
		},
		{
			name:     "delegates to PRInfo.StatusDescription",
			prInfo:   &github.PRInfo{State: github.PRStateMerged},
			expected: "Merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PRStatusDescription(tt.prInfo)
			if got != tt.expected {
				t.Errorf("PRStatusDescription() = %q, want %q", got, tt.expected)
			}
		})
	}
}

package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/github"
)

func TestIsGlobalDangerousMode(t *testing.T) {
	// Save original value to restore after test
	original := os.Getenv("WORKTREE_DANGEROUS_MODE")
	defer os.Setenv("WORKTREE_DANGEROUS_MODE", original)

	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"disabled when env not set", "", false},
		{"disabled when env is 0", "0", false},
		{"enabled when env is 1", "1", true},
		{"disabled when env is random", "foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue == "" {
				os.Unsetenv("WORKTREE_DANGEROUS_MODE")
			} else {
				os.Setenv("WORKTREE_DANGEROUS_MODE", tt.envValue)
			}

			got := IsGlobalDangerousMode()
			if got != tt.expected {
				t.Errorf("IsGlobalDangerousMode() = %v, want %v when env=%q", got, tt.expected, tt.envValue)
			}
		})
	}
}

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

func TestGetDefaultProjectColor(t *testing.T) {
	// There are 8 default project colors in the palette
	paletteSize := len(DefaultProjectColors)
	if paletteSize != 8 {
		t.Fatalf("Expected 8 default colors, got %d", paletteSize)
	}

	tests := []struct {
		name     string
		index    int
		expected string
	}{
		// First cycle through the palette (0-7)
		{"index 0 returns purple", 0, "#C678DD"},
		{"index 1 returns blue", 1, "#61AFEF"},
		{"index 2 returns cyan", 2, "#56B6C2"},
		{"index 3 returns green", 3, "#98C379"},
		{"index 4 returns yellow", 4, "#E5C07B"},
		{"index 5 returns red/pink", 5, "#E06C75"},
		{"index 6 returns orange", 6, "#D19A66"},
		{"index 7 returns gray", 7, "#ABB2BF"},

		// Second cycle (8-15) - colors repeat
		{"index 8 wraps to purple", 8, "#C678DD"},
		{"index 9 wraps to blue", 9, "#61AFEF"},
		{"index 15 wraps to gray", 15, "#ABB2BF"},

		// Large numbers work correctly
		{"index 100 uses modulo correctly", 100, "#E5C07B"}, // 100 % 8 = 4 (yellow)
		{"index 1000 uses modulo correctly", 1000, "#C678DD"}, // 1000 % 8 = 0 (purple)

		// Edge cases
		{"negative index becomes 0", -1, "#C678DD"},
		{"negative large number becomes 0", -100, "#C678DD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDefaultProjectColor(tt.index)
			if got != tt.expected {
				t.Errorf("GetDefaultProjectColor(%d) = %q, want %q", tt.index, got, tt.expected)
			}
		})
	}
}

func TestGetDefaultProjectColor_WorksForAnyNumberOfProjects(t *testing.T) {
	// Verify that the color selection works for a large number of projects
	// by checking that colors cycle predictably
	paletteSize := len(DefaultProjectColors)

	// Test with 100 projects
	for i := 0; i < 100; i++ {
		color := GetDefaultProjectColor(i)
		expectedColor := DefaultProjectColors[i%paletteSize]
		if color != expectedColor {
			t.Errorf("Project %d: got color %q, want %q", i, color, expectedColor)
		}
	}
}

func TestProjectColorCache(t *testing.T) {
	// Clear cache before testing
	SetProjectColors(make(map[string]string))

	// Test that unknown projects return muted color
	color := ProjectColor("unknown-project")
	if string(color) != string(ColorMuted) {
		t.Errorf("Unknown project should return ColorMuted, got %q", color)
	}

	// Test setting and getting a project color
	SetProjectColor("test-project", "#FF5500")
	color = ProjectColor("test-project")
	if string(color) != "#FF5500" {
		t.Errorf("ProjectColor('test-project') = %q, want #FF5500", color)
	}

	// Test bulk setting colors
	colors := map[string]string{
		"project-a": "#AA0000",
		"project-b": "#BB0000",
		"project-c": "#CC0000",
	}
	SetProjectColors(colors)

	// Verify all colors are set
	for name, expected := range colors {
		got := ProjectColor(name)
		if string(got) != expected {
			t.Errorf("ProjectColor(%q) = %q, want %q", name, got, expected)
		}
	}

	// Verify the cache was replaced (test-project should now return muted)
	color = ProjectColor("test-project")
	if string(color) != string(ColorMuted) {
		t.Errorf("After SetProjectColors, 'test-project' should return ColorMuted, got %q", color)
	}
}

func TestDefaultProjectColors_AreValidHexColors(t *testing.T) {
	// Verify that all default colors are valid hex color strings
	for i, color := range DefaultProjectColors {
		if len(color) != 7 {
			t.Errorf("DefaultProjectColors[%d] = %q: should be 7 characters (#RRGGBB)", i, color)
		}
		if color[0] != '#' {
			t.Errorf("DefaultProjectColors[%d] = %q: should start with #", i, color)
		}
		// Check that remaining characters are valid hex
		for j := 1; j < len(color); j++ {
			c := color[j]
			if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')) {
				t.Errorf("DefaultProjectColors[%d] = %q: contains invalid hex character at position %d", i, color, j)
			}
		}
	}
}

func TestDefaultProjectColors_AreDistinct(t *testing.T) {
	// Verify that all default colors are unique (no duplicates)
	seen := make(map[string]int)
	for i, color := range DefaultProjectColors {
		if prev, exists := seen[color]; exists {
			t.Errorf("DefaultProjectColors[%d] = %q: duplicate of DefaultProjectColors[%d]", i, color, prev)
		}
		seen[color] = i
	}
}

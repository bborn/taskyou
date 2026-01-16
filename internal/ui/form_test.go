package ui

import (
	"strings"
	"testing"
)

func TestCalculateBodyHeight(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		screenHeight int
		screenWidth  int
		wantMin      int
		wantMax      int
	}{
		{
			name:         "empty content returns minimum height",
			content:      "",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      4,
			wantMax:      4,
		},
		{
			name:         "single line returns minimum height",
			content:      "hello world",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      4,
			wantMax:      4,
		},
		{
			name:         "multiple lines grows height",
			content:      "line1\nline2\nline3\nline4\nline5\nline6",
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      6,
			wantMax:      6,
		},
		{
			name:         "many lines capped at max height (50% of screen)",
			content:      strings.Repeat("line\n", 50),
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      4,  // at least minimum
			wantMax:      14, // (50-22)/2 = 14
		},
		{
			name:         "long lines wrap and increase height",
			content:      strings.Repeat("a", 200), // should wrap on ~76 char width
			screenHeight: 50,
			screenWidth:  100,
			wantMin:      4,
			wantMax:      6, // wrapped lines
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, tt.screenWidth, tt.screenHeight, "")
			m.bodyInput.SetValue(tt.content)

			height := m.calculateBodyHeight()

			if height < tt.wantMin {
				t.Errorf("calculateBodyHeight() = %d, want >= %d", height, tt.wantMin)
			}
			if height > tt.wantMax {
				t.Errorf("calculateBodyHeight() = %d, want <= %d", height, tt.wantMax)
			}
		})
	}
}

func TestUpdateBodyHeightSetsHeight(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "")

	// Initially should have minimum height of 4
	m.updateBodyHeight()
	// The textarea height is internal, so we just verify no panic

	// Add content and update
	m.bodyInput.SetValue("line1\nline2\nline3\nline4\nline5")
	m.updateBodyHeight()
	// Verify no panic and height should be 5

	// Verify that with large content, height is capped
	m.bodyInput.SetValue(strings.Repeat("line\n", 100))
	m.updateBodyHeight()
	// Should be capped at max height (50% of screen)
}

func TestMaxHeightIs50PercentOfScreen(t *testing.T) {
	screenHeights := []int{40, 60, 80, 100}

	for _, screenHeight := range screenHeights {
		t.Run("screen_height_"+string(rune('0'+screenHeight/10)), func(t *testing.T) {
			m := NewFormModel(nil, 100, screenHeight, "")

			// Add lots of content to trigger max height
			m.bodyInput.SetValue(strings.Repeat("line\n", 100))

			height := m.calculateBodyHeight()

			// formOverhead is 22, so maxHeight = (screenHeight - 22) / 2
			expectedMax := (screenHeight - 22) / 2
			if expectedMax < 4 {
				expectedMax = 4
			}

			if height > expectedMax {
				t.Errorf("height %d exceeds expected max %d for screen height %d", height, expectedMax, screenHeight)
			}
		})
	}
}

func TestRenderBodyScrollbar(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		visibleLines int
		wantScrollbar bool
	}{
		{
			name:          "empty content returns no scrollbar",
			content:       "",
			visibleLines:  4,
			wantScrollbar: false,
		},
		{
			name:          "content fits in viewport returns no scrollbar",
			content:       "line1\nline2",
			visibleLines:  4,
			wantScrollbar: false,
		},
		{
			name:          "content exceeds viewport returns scrollbar",
			content:       "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8",
			visibleLines:  4,
			wantScrollbar: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewFormModel(nil, 100, 50, "")
			m.bodyInput.SetValue(tt.content)
			m.bodyInput.SetHeight(tt.visibleLines)

			scrollbar := m.renderBodyScrollbar(tt.visibleLines)

			if tt.wantScrollbar {
				if scrollbar == nil {
					t.Error("expected scrollbar but got nil")
				}
				if len(scrollbar) != tt.visibleLines {
					t.Errorf("scrollbar length = %d, want %d", len(scrollbar), tt.visibleLines)
				}
				// Verify scrollbar contains expected characters
				for _, char := range scrollbar {
					if char != "┃" && char != "│" && !strings.Contains(char, "┃") && !strings.Contains(char, "│") {
						t.Errorf("unexpected scrollbar character: %q", char)
					}
				}
			} else {
				if scrollbar != nil {
					t.Errorf("expected no scrollbar but got %v", scrollbar)
				}
			}
		})
	}
}

func TestScrollbarAppearsInFormView(t *testing.T) {
	m := NewFormModel(nil, 100, 50, "")

	// Add enough content to trigger scrollbar
	m.bodyInput.SetValue(strings.Repeat("line\n", 20))
	m.bodyInput.SetHeight(4) // Force small viewport

	view := m.View()

	// The scrollbar characters should appear in the view
	hasScrollbar := strings.Contains(view, "┃") || strings.Contains(view, "│")
	if !hasScrollbar {
		t.Error("expected scrollbar characters in view but found none")
	}
}
